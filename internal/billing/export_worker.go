// BILLING v2 异步导出 worker (2026-06-14 RFC PR #2)
//
// 限流策略: 每用户 ≤ 2 个 running (Q3 决策, 2026-06-14)
//   - 注意原话"全系统 ≤ 2 个", 二次确认改"每用户 2 个", 在 RFC §1 标红
//   - 全局 worker pool 也是 2 goroutine, 但每用户独立信号量
//
// 状态机: pending → running → success/failed, 或 pending → cancelled
//
// PR #2 范围: 队列 + 信号量 + 状态机 + Repo 调用
// PR #3 范围: processExportTask 内的账单生成 (HTML + XLSX + ZIP)
//
//	本 PR 用 stub 替代 processExportTask, 等 PR #3 替换
package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// newTaskID 生成 task_id (16 字节随机 hex, 共 32 字符, 等同 uuid 长度但无外部依赖)
// 实际是 RFC 4122 v4 简化: 16 字节随机数, 不用 version/variant 字段
func newTaskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极低概率失败, 用时间戳兜底
		log.Printf("[billing-export] rand.Read failed, use timestamp: %v", err)
		return "fallback-" + time.Now().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(b)
}

// MaxConcurrentPerUser 每用户同时最多 2 个 running (Q3)
const MaxConcurrentPerUser = 2

// WorkerPoolSize 全局 worker goroutine 数
const WorkerPoolSize = 2

// TaskQueueSize 任务队列缓冲
const TaskQueueSize = 100

var (
	userSemsMu sync.Mutex
	userSems   = make(map[int]chan struct{}) // user_id → semaphore(2)

	exportQueue = make(chan *dal.BillingExportTask, TaskQueueSize)
)

// getUserSem 获取某 user_id 的信号量 (懒初始化)
func getUserSem(userID int) chan struct{} {
	userSemsMu.Lock()
	defer userSemsMu.Unlock()
	sem, ok := userSems[userID]
	if !ok {
		sem = make(chan struct{}, MaxConcurrentPerUser)
		userSems[userID] = sem
	}
	return sem
}

// CleanupUserSem 清理某 user_id 信号量 (30 天清理时调用, 防 map 无限增长)
func CleanupUserSem(userID int) {
	userSemsMu.Lock()
	defer userSemsMu.Unlock()
	if sem, ok := userSems[userID]; ok {
		// 仅当无槽位被占才删 (避免删了正在用的)
		if len(sem) == 0 {
			delete(userSems, userID)
		}
	}
}

// EnqueueExportTask 提交任务, 满则返 error
//
// BILLING v3 (PR #4, 2026-06-14) 加 kind + vendorCode 参数:
//   - kind = "customer" (v2) / "upstream" (v3)
//   - vendorCode = "" for v2 客户对账; v3 上游对账时填 (如 "provider_alpha")
//
// 流程:
//  1. 写 DB (status=pending)
//  2. 检查同 user_id 已 running ≤ 2 (查 DB count)
//  3. 入队 exportQueue
//
// 返回值: task_id, error
func EnqueueExportTask(ctx context.Context, userID int, username, period, formats, kind, vendorCode, operator string) (string, error) {
	// 0) 兜底: kind 空时默认 customer (兼容老调用)
	if kind == "" {
		kind = "customer"
	}
	// 1) 限流检查 (同 user_id running 数)
	running, err := dal.CountBillingExportTasksRunningByUser(ctx, userID)
	if err != nil {
		return "", err
	}
	if running >= int64(MaxConcurrentPerUser) {
		return "", errors.New("user already has 2 running tasks, please wait")
	}

	// 2) 写 DB
	taskID := newTaskID()
	t := &dal.BillingExportTask{
		TaskID:     taskID,
		UserID:     userID,
		Username:   username,
		Period:     period,
		Formats:    formats,
		Kind:       kind,
		VendorCode: vendorCode,
		Status:     "pending",
		Operator:   operator,
	}
	if err := dal.CreateBillingExportTask(ctx, t); err != nil {
		return "", err
	}

	// 3) 入队 (非阻塞)
	select {
	case exportQueue <- t:
		log.Printf("[billing-export] enqueued task_id=%s user=%s period=%s", taskID, username, period)
		return taskID, nil
	default:
		// 队列满 (实际不会出现, 队列 100 远大于 2 worker 上限)
		return "", errors.New("export queue full, retry later")
	}
}

// StartExportWorkerPool 启动 worker pool (启动时 main.go 调用)
func StartExportWorkerPool(ctx context.Context) {
	for i := 0; i < WorkerPoolSize; i++ {
		go workerLoop(ctx, i)
	}
	log.Printf("[billing-export] worker pool started, size=%d, per_user_limit=%d", WorkerPoolSize, MaxConcurrentPerUser)
}

func workerLoop(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("[billing-export] worker %d stopped", workerID)
			return
		case task := <-exportQueue:
			processOneTask(ctx, workerID, task)
		}
	}
}

// processOneTask 处理单任务: 占信号量 → 改 running → 调生成器 → 改 success/failed → 释放信号量
func processOneTask(ctx context.Context, workerID int, task *dal.BillingExportTask) {
	sem := getUserSem(task.UserID)
	// 占信号量 (非阻塞, 因为 enqueue 时已限流, 这里不会失败)
	sem <- struct{}{}
	defer func() { <-sem }()

	log.Printf("[billing-export] worker=%d start task_id=%s user=%s", workerID, task.TaskID, task.Username)

	// 1) 改 status=running
	now := time.Now()
	task.Status = "running"
	task.StartedAt = &now
	task.Progress = 0
	if err := dal.UpdateBillingExportTask(ctx, task); err != nil {
		log.Printf("[billing-export] update running failed: %v", err)
		// 不返, 进入 processExportTask 让它自己处理失败
	}

	// 2) 写进度日志
	_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info", "worker started")

	// 3) 调生成器 (PR #3 替换为真实 HTML + XLSX + ZIP)
	//    按 RFC §5 设计: 查 RoDB → 渲染 HTML/XLSX → ZIP 打包
	//    5s ctx timeout 兜底 (防 RoDB 慢查)
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	filePath, fileSize, err := generateStatement(queryCtx, task)
	cancel()

	// 4) 写终态
	finishedAt := time.Now()
	task.FinishedAt = &finishedAt
	if err != nil {
		task.Status = "failed"
		task.ErrorMsg = err.Error()
		task.Progress = 0
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "error", err.Error())
		log.Printf("[billing-export] worker=%d failed task_id=%s err=%v", workerID, task.TaskID, err)
	} else {
		task.Status = "success"
		task.Progress = 100
		task.FilePath = filePath
		task.FileSize = fileSize
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info", "completed")
		log.Printf("[billing-export] worker=%d success task_id=%s size=%d", workerID, task.TaskID, fileSize)
	}
	if err := dal.UpdateBillingExportTask(ctx, task); err != nil {
		log.Printf("[billing-export] update final state failed: %v", err)
	}
}

// generateStatement 真实账单生成 (PR #3 替换 stub)
// 流程: 解析 period → 查 RoDB → 渲染 HTML + XLSX → ZIP 打包
//
// BILLING v3 (PR #4, 2026-06-14) 加 switch kind:
//   - "customer" → 走 v2 路径 (QueryStatement + RenderHTML/XLSX + PackZip)
//   - "upstream" → 走 v3 路径 (CalcUpstreamStatement + RenderUpstreamHTML/XLSX + PackUpstreamZip)
func generateStatement(ctx context.Context, task *dal.BillingExportTask) (string, int64, error) {
	switch task.Kind {
	case "", "customer":
		return generateCustomerStatement(ctx, task)
	case "upstream":
		return generateUpstreamStatementTask(ctx, task)
	default:
		return "", 0, fmt.Errorf("unknown task kind: %s", task.Kind)
	}
}

// generateCustomerStatement v2 客户对账单生成
func generateCustomerStatement(ctx context.Context, task *dal.BillingExportTask) (string, int64, error) {
	// 1) 解析 period '2026-05' → [start, end)
	startTS, endTS, err := PeriodBounds(task.Period)
	if err != nil {
		return "", 0, fmt.Errorf("invalid period: %w", err)
	}

	// 2) 查 RoDB
	stmt, err := QueryStatement(ctx, StatementQueryParams{
		UserID:  task.UserID,
		StartTS: startTS,
		EndTS:   endTS,
	})
	if err != nil {
		return "", 0, fmt.Errorf("query statement: %w", err)
	}

	// 3) 按 formats 渲染
	var htmlBytes, xlsxBytes []byte
	wantHTML, wantXLSX := false, false
	for _, f := range splitFormats(task.Formats) {
		switch f {
		case "html":
			wantHTML = true
		case "xlsx":
			wantXLSX = true
		}
	}

	if wantHTML {
		htmlBytes, err = RenderHTML(stmt)
		if err != nil {
			return "", 0, fmt.Errorf("render html: %w", err)
		}
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info", fmt.Sprintf("html rendered, size=%d", len(htmlBytes)))
	}
	if wantXLSX {
		xlsxBytes, err = RenderXLSX(stmt)
		if err != nil {
			return "", 0, fmt.Errorf("render xlsx: %w", err)
		}
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info", fmt.Sprintf("xlsx rendered, size=%d", len(xlsxBytes)))
	}

	// 4) ZIP 打包
	path, size, err := PackZip(task.TaskID, stmt, task.Formats, htmlBytes, xlsxBytes)
	if err != nil {
		return "", 0, fmt.Errorf("pack zip: %w", err)
	}
	return path, size, nil
}

// generateUpstreamStatementTask v3 上游对账单生成
//
// 入参: task.VendorCode 必填 (e.g. "provider_alpha" / "openai-azure")
// 流程:
//  1. 解析 period → [start, end)
//  2. PR #9 (2026-06-15): 走 GetUpstreamStatementCached, cache 优先
//     - cache hit: 用 cache 的 totals (5min 延迟可接受, 账单场景)
//     - cache miss: fallback 实时算 CalcUpstreamStatement
//     - breakdown (ByDate/ByChannel/ByModel) 仍走 CalcUpstreamStatement (cache 只存 totals)
//  3. 渲染 HTML + XLSX
//  4. PackUpstreamZip
func generateUpstreamStatementTask(ctx context.Context, task *dal.BillingExportTask) (string, int64, error) {
	// 1) 解析 period
	startTS, endTS, err := PeriodBounds(task.Period)
	if err != nil {
		return "", 0, fmt.Errorf("invalid period: %w", err)
	}
	if task.VendorCode == "" {
		return "", 0, fmt.Errorf("upstream task requires vendor_code")
	}

	// 2) PR #9: cache 优先 (5min 延迟可接受, 月对账 1-5 号场景)
	// 把 period (YYYY-MM) 映射到 cache period_label
	periodLabel := mapPeriodToLabel(task.Period, endTS)
	stmt, fromCache, err := GetUpstreamStatementCached(ctx, task.VendorCode, startTS, endTS, periodLabel)
	if err != nil {
		return "", 0, fmt.Errorf("calc upstream statement (cache-aware): %w", err)
	}
	if fromCache {
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info",
			fmt.Sprintf("upstream cache hit vendor=%s period=%s (5min delayed totals)", task.VendorCode, periodLabel))
	} else {
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info",
			fmt.Sprintf("upstream cache miss vendor=%s period=%s, live calc", task.VendorCode, periodLabel))
	}

	// 3) 渲染
	generatedAt := time.Now().Unix()
	var htmlBytes, xlsxBytes []byte
	wantHTML, wantXLSX := false, false
	for _, f := range splitFormats(task.Formats) {
		switch f {
		case "html":
			wantHTML = true
		case "xlsx":
			wantXLSX = true
		}
	}
	if wantHTML {
		htmlBytes, err = RenderUpstreamHTML(stmt, generatedAt)
		if err != nil {
			return "", 0, fmt.Errorf("render upstream html: %w", err)
		}
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info",
			fmt.Sprintf("upstream html rendered, size=%d", len(htmlBytes)))
	}
	if wantXLSX {
		xlsxBytes, err = RenderUpstreamXLSX(stmt)
		if err != nil {
			return "", 0, fmt.Errorf("render upstream xlsx: %w", err)
		}
		_ = dal.AppendBillingExportTaskLog(ctx, task.TaskID, "info",
			fmt.Sprintf("upstream xlsx rendered, size=%d", len(xlsxBytes)))
	}

	// 4) PackUpstreamZip
	path, size, err := PackUpstreamZip(task.TaskID, stmt, generatedAt, task.Formats, htmlBytes, xlsxBytes)
	if err != nil {
		return "", 0, fmt.Errorf("pack upstream zip: %w", err)
	}
	return path, size, nil
}
