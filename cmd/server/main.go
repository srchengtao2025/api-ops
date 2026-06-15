// api-ops 服务入口
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/api-ops/api-ops/internal/ai"
	kbbatch "github.com/api-ops/api-ops/internal/ai/kb"
	"github.com/api-ops/api-ops/internal/api"
	"github.com/api-ops/api-ops/internal/audit"
	"github.com/api-ops/api-ops/internal/auth"
	"github.com/api-ops/api-ops/internal/billing"
	"github.com/api-ops/api-ops/internal/config"
	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/monitor"
	"github.com/api-ops/api-ops/internal/newapi_client"
	"github.com/api-ops/api-ops/internal/notifier"
	"github.com/api-ops/api-ops/internal/realtime"
	"github.com/api-ops/api-ops/internal/scheduler"
	"github.com/api-ops/api-ops/internal/sync"
)

func main() {
	startTime := time.Now()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[main] config load failed: %v", err)
	}
	log.Printf("[main] api-ops starting... port=%s mode=%s", cfg.Port, cfg.GinMode)

	// 初始化只读 DB（upstream）
	if err := dal.Init(cfg); err != nil {
		log.Fatalf("[main] dal init failed: %v", err)
	}
	defer dal.Close()

	// 初始化 Redis（可选，失败不阻塞）
	if err := dal.InitRedis(cfg); err != nil {
		log.Printf("[main] redis init failed (will degrade to no-cache mode): %v", err)
	} else {
		defer dal.CloseRedis()
	}

	// 迁移自有 DB schema
	if err := dal.MigrateOps(); err != nil {
		log.Fatalf("[main] migrate ops failed: %v", err)
	}
	log.Println("[main] ops schema migrated")

	// 初始化飞书 notifier（Q3 决策体现）
	// 注入到 monitor：触发告警后异步推送
	feishuNotifier := notifier.New()
	monitor.Notifier = feishuNotifier
	// 注入到 api：admin 改 feishu_webhook_* 配置时 reload
	api.ReloadNotifier = feishuNotifier.Reload
	log.Println("[main] feishu notifier wired (alert + report)")

	// P2 实时面板：建 hub，挂到 /api/ws/*
	wsHub := realtime.NewHub()
	realtime.SetGlobal(wsHub)
	// 注入到 monitor：告警触发时同步推 alert 帧
	monitor.SetAlertBroadcaster(wsHub.BroadcastAlert)

	// 启动 API server
	srv := api.New(cfg)
	srv.MountWebSocket(wsHub)
	log.Println("[main] P2 ws hub wired (/api/ws/global, /customer/:id, /channel/:id, /errors, /multiplex)")

	// A 阶段: 账号系统 (JWT) - 注入 service + audit logger
	authSvc := auth.NewService(cfg.JWTSecret, 24*time.Hour)
	srv.SetAuthService(authSvc)
	srv.SetAuditLogger(audit.NewLogger())
	seedBootstrapAdmin(cfg) // 用 ADMIN_PASSWORD 首次建 admin 账号
	log.Println("[main] A-stage auth wired: JWT 24h + admin/finance/viewer RBAC")

	// 启动 hub 主循环 + 5s tick（绑定到 root context，关服时自动停）
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	realtime.Start(rootCtx, wsHub)

	// logs 摘要同步：1min tick 把 RoDB.logs 的 1min 聚合刷到 OPS.cache_logs_summary_5min
	// 用于后续替代 dashboard/monitor/billing 的"大聚合直读 RoDB"
	// 只读账号就能做（SELECT only）；demo 模式 RO 为 nil 时跳过
	if dal.RO != nil {
		go sync.LogsSummaryLoop(rootCtx)
		log.Println("[main] logs_summary cache sync started (interval=1min)")
	} else {
		log.Println("[main] logs_summary cache sync skipped (RO nil = demo mode)")
	}

	// P_newapi cache sync：从 newapi Admin API 周期性拉 channels/users/tokens 到 OPS DB
	// 替代方案：在阿里云 RDS 上 GRANT SELECT —— 本方案不依赖 PG 权限
	if cfg.UpstreamAdminToken != "" && cfg.UpstreamAdminBaseURL != "" {
		client := newapi_client.New(cfg)
		syncer := sync.New(client)
		if syncer != nil {
			// 启动时同步阻塞一次（保证 cache 有数据再接 API）
			startSync := time.Now()
			if err := syncer.RunOnce(rootCtx); err != nil {
				log.Printf("[main] sync initial failed (will retry in background): %v", err)
			} else {
				log.Printf("[main] sync initial done in %s", time.Since(startSync))
			}

			// BILLING v2 异步导出 worker pool (PR #2 / 8, 2026-06-14)
			billing.StartExportWorkerPool(rootCtx)

			// BILLING v2 30 天清理 (PR #7 / 8, 2026-06-14) - 每天 03:00 跑
			billing.StartPruneLoop(rootCtx, 24*time.Hour)
			// 后台定时刷新
			go syncer.Start(rootCtx, sync.DefaultInterval)
		}
		// 同时把 client 注入 Server，供 dashboard handler 调 /api/data/* /api/log/stat
		srv.SetNewapiClient(client)
		log.Println("[main] newapi admin client wired (channels/users sync + dashboard data API)")
	} else {
		log.Println("[main] upstream sync disabled (missing API_OPS_ADMIN_TOKEN / upstream_ADMIN_BASE_URL)")
	}

	// P3 AI 知识库 loader：启动时把 4 个 YAML upsert 到 error_kb_entries
	if n, err := kbbatch.LoadAll(rootCtx); err != nil {
		log.Printf("[main] KB loader failed: %v (LLM-only path still works)", err)
	} else {
		log.Printf("[main] KB loaded: %d entries upserted", n)
	}

	// P1 监控调度器：每 1min 跑 channel_health_5min 聚合 + 告警规则评估
	// (2026-06-15 PR: 之前漏启导致 health/alerts 永远 0 数据, SPA 监控页空)
	scheduler.Run(rootCtx, cfg)
	log.Println("[main] P1 monitor scheduler started (1min tick: 5min aggregate + alert eval)")

	// P3 AI scheduler：5min 1h 错误聚类 + 每日 9:00 错误日报（owner 补）
	// 1) 5min tick 聚类：把最近 1h 的 logs 归一化入 ai_error_clusters
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		// 启动后 30s 先跑一次（等 KB + notifier 启动完毕）
		time.AfterFunc(30*time.Second, func() {
			if n, err := ai.ClusterOneHour(rootCtx); err != nil {
				log.Printf("[ai.scheduler] initial cluster failed: %v", err)
			} else {
				log.Printf("[ai.scheduler] initial cluster upserted=%d", n)
			}
		})
		for {
			select {
			case <-rootCtx.Done():
				log.Println("[ai.scheduler] cluster tick stopped")
				return
			case <-t.C:
				if n, err := ai.ClusterOneHour(rootCtx); err != nil {
					log.Printf("[ai.scheduler] cluster failed: %v", err)
				} else if n > 0 {
					log.Printf("[ai.scheduler] cluster upserted=%d", n)
				}
			}
		}
	}()

	// 2) 每日 9:00 跑错误日报（先用 loop 每分钟检查时:分，演示阶段用更短间隔 demo-friendly）
	go func() {
		t := time.NewTicker(1 * time.Minute)
		defer t.Stop()
		// 启动后 60s 先跑一次（让 cluster + KB 跑稳）
		time.AfterFunc(60*time.Second, func() {
			if rep, err := ai.GenerateErrorDailyReport(rootCtx, time.Now().Unix()); err != nil {
				log.Printf("[ai.scheduler] initial daily report failed: %v", err)
			} else {
				log.Printf("[ai.scheduler] initial daily report id=%d title=%q", rep.ID, rep.Title)
			}
		})
		lastRunDate := ""
		for {
			select {
			case <-rootCtx.Done():
				log.Println("[ai.scheduler] daily report tick stopped")
				return
			case <-t.C:
				now := time.Now()
				if now.Hour() == 9 && now.Minute() == 0 {
					today := now.Format("2006-01-02")
					if today == lastRunDate {
						continue
					}
					lastRunDate = today
					if rep, err := ai.GenerateErrorDailyReport(rootCtx, now.Unix()); err != nil {
						log.Printf("[ai.scheduler] daily report failed: %v", err)
					} else {
						log.Printf("[ai.scheduler] daily report id=%d title=%q", rep.ID, rep.Title)
					}
				}
			}
		}
	}()
	log.Println("[main] P3 AI scheduler started (cluster=5min, daily=09:00)")

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Gin(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 优雅退出
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[main] http listen failed: %v", err)
		}
	}()
	log.Printf("[main] api-ops listening on :%s (startup %s)", cfg.Port, time.Since(startTime))

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[main] shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("[main] shutdown error: %v", err)
	}
	fmt.Println("[main] bye")
}
