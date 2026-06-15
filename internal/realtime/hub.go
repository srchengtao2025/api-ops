// Package realtime: P2 WebSocket 实时面板
// 设计：Hub 维护连接+主题订阅；5s tick 从 RoDB() 拉 global / customer / channel 指标按订阅 fan-out；
// alert 触发时单独推送；限流 5 conn/IP + 100 msg/min/conn（Redis 计数，降级 in-process）；
// 心跳 30s ping / 60s pong；客户端重连指数退避（前端实现）。
//
// 主题：global | customer:<uid> | channel:<cid> | errors
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/gorilla/websocket"
)

// ===== 消息协议（PRD §6.1.2） =====

// Frame WebSocket 帧
type Frame struct {
	Type    string      `json:"type"`              // tick | error | alert | metric
	Channel string      `json:"channel"`           // 主题名
	Payload interface{} `json:"payload,omitempty"` // 数据
	TS      int64       `json:"ts"`                // unix ms
}

// TickPayload tick 帧的 payload 模板
type TickPayload struct {
	RPM        int     `json:"rpm"`
	TPM        int64   `json:"tpm"`
	ErrorRate  float64 `json:"error_rate"`
	P95Latency int     `json:"p95_latency_ms"`
	RequestCnt int64   `json:"request_count,omitempty"`
	ErrorCnt   int64   `json:"error_count,omitempty"`
	Balance    float64 `json:"balance,omitempty"`
}

// AlertPayload alert 帧的 payload
type AlertPayload struct {
	RuleID      uint64 `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	Severity    string `json:"severity"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	SubjectName string `json:"subject_name"`
	Message     string `json:"message"`
}

// ===== Client =====

// Client 单个 WebSocket 客户端
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	topics map[string]bool // 订阅主题集合
	ip     string
	id     uint64
	mu     sync.Mutex
	closed bool
}

// Hub 中心：注册 / 注销 / 广播
type Hub struct {
	mu         sync.RWMutex
	clients    map[*Client]bool
	byTopic    map[string]map[*Client]bool // topic -> clients
	broadcast  chan []byte                 // 全局广播（如 global tick）
	register   chan *Client
	unregister chan *Client
	nextID     uint64
	tickStop   chan struct{}
	limiter    *Limiter
}

// NewHub 创建 Hub
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		byTopic:    make(map[string]map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		tickStop:   make(chan struct{}),
		limiter:    NewLimiter(),
	}
}

// Global 全局单例（方便跨包调用）
var globalHub *Hub

// SetGlobal 设置全局 Hub（启动时由 server 注入）
func SetGlobal(h *Hub) { globalHub = h }

// GlobalHub 获取全局 Hub（可能为 nil）
func GlobalHub() *Hub { return globalHub }

// Run Hub 主循环（goroutine）
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			for t := range c.topics {
				if _, ok := h.byTopic[t]; !ok {
					h.byTopic[t] = make(map[*Client]bool)
				}
				h.byTopic[t][c] = true
			}
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				for t := range c.topics {
					if subs, ok := h.byTopic[t]; ok {
						delete(subs, c)
						if len(subs) == 0 {
							delete(h.byTopic, t)
						}
					}
				}
				c.closeOnce()
			}
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// send 满，丢消息并标记关闭
					go func(cl *Client) { h.unregister <- cl }(c)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Stats 返回当前连接数（用于调试 / 健康检查）
func (h *Hub) Stats() (int, int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients), len(h.byTopic)
}

// ----- 订阅管理 -----

// Subscribe 添加订阅（连接建立时由 handler 调用）
func (c *Client) Subscribe(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.topics == nil {
		c.topics = make(map[string]bool)
	}
	c.topics[topic] = true
}

// ----- 发送 -----

// sendRaw 投递到 send 通道（非阻塞，溢出时关闭）
func (c *Client) sendRaw(b []byte) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	select {
	case c.send <- b:
	default:
		// 客户端消费过慢 → 强制关闭
		c.hub.unregister <- c
	}
}

// SendFrame 发送一帧（用于 BroadcastTick / BroadcastAlert）
func (c *Client) SendFrame(f Frame) {
	if !c.hub.limiter.AllowMsg(c.ip) {
		return // 限流静默丢弃
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	c.sendRaw(b)
}

// SendError 发送错误帧给单个 client
func (c *Client) SendError(msg string) {
	c.SendFrame(Frame{
		Type:    "error",
		Channel: "system",
		Payload: ginH("message", msg),
		TS:      time.Now().UnixMilli(),
	})
}

func (c *Client) closeOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.send)
	_ = c.conn.Close()
}

// ===== 广播 API（外部调用） =====

// broadcastTo 内部 fan-out：nil clients 集合 = 全局广播；否则按指定 client 列表
func (h *Hub) broadcastTo(frame Frame, clients []*Client) {
	if h == nil {
		return
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return
	}
	if clients == nil {
		h.mu.RLock()
		for c := range h.clients {
			clients = append(clients, c)
		}
		h.mu.RUnlock()
	}
	for _, c := range clients {
		c.sendRaw(b)
	}
}

// BroadcastTick 推一帧给订阅了指定 topic 的 client
func (h *Hub) BroadcastTick(topic string, payload interface{}, msgTS int64) {
	if h == nil {
		return
	}
	h.mu.RLock()
	subs := h.byTopic[topic]
	clients := make([]*Client, 0, len(subs))
	for c := range subs {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	h.broadcastTo(Frame{Type: "tick", Channel: topic, Payload: payload, TS: msgTS}, clients)
}

// BroadcastGlobal 全局广播
func (h *Hub) BroadcastGlobal(payload interface{}, msgTS int64) {
	h.broadcastTo(Frame{Type: "tick", Channel: "global", Payload: payload, TS: msgTS}, nil)
}

// BroadcastAlert 推送告警到 global + errors 主题
func (h *Hub) BroadcastAlert(a AlertPayload) {
	if h == nil {
		return
	}
	now := time.Now().UnixMilli()
	h.broadcastTo(Frame{Type: "alert", Channel: "global", Payload: a, TS: now}, nil)
	h.BroadcastTick("errors", ginH("subject_type", a.SubjectType, "subject_id", a.SubjectID, "message", a.Message), now)
}

// ===== 5s tick 拉取数据 =====

// StartTicker 启动 5s tick goroutine
//   - 拉 global / 错误流 → BroadcastGlobal
//   - 遍历 known channels → BroadcastTick("channel:<id>")
//   - 遍历活跃客户（最近 5min 有 logs 的 user_id，最多 50）→ BroadcastTick("customer:<id>")
func (h *Hub) StartTicker(ctx context.Context) {
	if h == nil {
		return
	}
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-h.tickStop:
				return
			case <-t.C:
				h.runTickOnce(ctx)
			}
		}
	}()
}

// StopTicker 停止 tick（测试 / 关闭时使用）
func (h *Hub) StopTicker() {
	if h == nil {
		return
	}
	select {
	case <-h.tickStop:
	default:
		close(h.tickStop)
	}
}

func (h *Hub) runTickOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[realtime] tick panic: %v", r)
		}
	}()
	now := time.Now()
	ts := now.UnixMilli()
	nowSec := now.Unix()
	win5 := nowSec - 300

	// 优化：原设计是 3 部分 151 query/5s（global 1 + channels 100 loop + customers 50 loop）。
	// 现合并为 3 query 并发（仍读 RoDB 保证实时性）：
	//   1) global: aggregateLogs( scope="" )          —— 1 SQL
	//   2) channels: aggregateChannelsBatch( 100 个 )   —— 1 SQL（GROUP BY channel_id WHERE IN (...)）
	//   3) customers: listActiveUserIDs + aggregateUsersBatch —— 2 SQL（仍是 2 次，但顶层 50 个用户只在 1 个 SQL 中聚合）
	// 总 query: 3 SQL/tick => 从 151 SQL/tick 降为 3 SQL/tick (~50x 减少 RDS 压力)
	type chTick struct {
		ch      dal.ChannelMirror
		payload TickPayload
	}
	type userTick struct {
		uid     uint64
		payload TickPayload
	}
	var (
		globalPayload TickPayload
		globalOK      bool
		chTicks       []chTick
		userTicks     []userTick
	)

	// 并发跑 3 部分
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		globalPayload, globalOK = aggregateLogs(ctx, "", win5, nowSec, 0)
	}()

	go func() {
		defer wg.Done()
		chs, err := dal.ListChannels(ctx, 0)
		if err != nil || len(chs) == 0 {
			return
		}
		limit := 100
		if len(chs) < limit {
			limit = len(chs)
		}
		chs = chs[:limit]
		// 1 SQL 拿全部 channel 聚合
		aggMap, err := aggregateChannelsBatch(ctx, chs, win5, nowSec)
		if err != nil {
			log.Printf("[realtime] aggregateChannelsBatch failed: %v", err)
			return
		}
		for _, ch := range chs {
			p, ok := aggMap[ch.ID]
			if !ok {
				// 5min 窗无数据：发一个空的 tick，让前端能拿到 ch.Balance
				p = TickPayload{Balance: ch.Balance, P95Latency: 100}
			} else {
				p.Balance = ch.Balance
			}
			chTicks = append(chTicks, chTick{ch: ch, payload: p})
		}
	}()

	go func() {
		defer wg.Done()
		users, err := listActiveUserIDs(ctx, win5, nowSec, 50)
		if err != nil || len(users) == 0 {
			return
		}
		aggMap, err := aggregateUsersBatch(ctx, users, win5, nowSec)
		if err != nil {
			log.Printf("[realtime] aggregateUsersBatch failed: %v", err)
			return
		}
		for _, uid := range users {
			p, ok := aggMap[uid]
			if !ok {
				p = TickPayload{P95Latency: 100}
			}
			userTicks = append(userTicks, userTick{uid: uid, payload: p})
		}
	}()

	wg.Wait()

	// 广播
	if globalOK {
		h.BroadcastGlobal(globalPayload, ts)
	}
	for _, ct := range chTicks {
		h.BroadcastTick("channel:"+strconv.Itoa(ct.ch.ID), ct.payload, ts)
	}
	for _, ut := range userTicks {
		h.BroadcastTick("customer:"+strconv.FormatUint(ut.uid, 10), ut.payload, ts)
	}
}

// ===== 聚合 SQL =====
// RoDB() 优先 upstream/newapi logs（只读），demo 模式自动 fallback 到 OPS
// 简化：按时间窗 COUNT / SUM(err) / AVG(use_time) 算 RPM / 错误率 / P95 兜底

// aggRow SQL 聚合中间结果
type aggRow struct {
	ReqCnt   int64
	ErrCnt   int64
	Tokens   int64
	AvgUseMs float64
}

// aggregateLogs 通用聚合：scope = "" (global) | "channel:<id>" | "user:<id>"
// 返回 (payload, ok)；ok=false 表示 SQL 失败 / 无数据
func aggregateLogs(ctx context.Context, scope string, startTS, endTS int64, extra float64) (TickPayload, bool) {
	// A 阶段: RoDB=nil (API_OPS_RO_DSN 未配) → 返回空数据, 不崩
	if !dal.HasRoDB() {
		return TickPayload{}, false
	}
	sqlStr := `SELECT COUNT(*) AS req_cnt,
       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS err_cnt,
       COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens,
       COALESCE(AVG(use_time), 0) AS avg_use_ms
FROM logs WHERE created_at >= ? AND created_at < ?`
	args := []interface{}{dal.LogTypeError, startTS, endTS}
	switch {
	case scope == "":
		// global: 无额外条件
	case len(scope) > 8 && scope[:8] == "channel:":
		cid, _ := strconv.Atoi(scope[8:])
		// new-api logs 表 DB 列名是 channel_id（Go struct ChannelId，GORM 自动转）
		// JSON tag 是 "channel"（前端用），但 SQL 必须用 channel_id
		sqlStr += " AND channel_id = ?"
		args = append(args, cid)
	case len(scope) > 5 && scope[:5] == "user:":
		uid, _ := strconv.ParseUint(scope[5:], 10, 64)
		sqlStr += " AND user_id = ?"
		args = append(args, uid)
	}
	var r aggRow
	if err := dal.RoDB().WithContext(ctx).Raw(sqlStr, args...).Scan(&r).Error; err != nil {
		return TickPayload{}, false
	}
	p95 := int(r.AvgUseMs * 1000 * 1.5)
	if p95 < 100 {
		p95 = 100
	}
	dur := float64(endTS-startTS) / 60.0
	rpm := int(float64(r.ReqCnt) / dur)
	tpm := int64(float64(r.Tokens) / dur)
	er := 0.0
	if r.ReqCnt > 0 {
		er = float64(r.ErrCnt) / float64(r.ReqCnt)
	}
	return TickPayload{
		RPM: rpm, TPM: tpm, ErrorRate: er, P95Latency: p95,
		RequestCnt: r.ReqCnt, ErrorCnt: r.ErrCnt,
		Balance: extra,
	}, true
}

// aggregateChannelsBatch 1 SQL 拿多个 channel 的 5min 聚合
// 返回 map[channel_id]TickPayload（无数据的 channel 不在 map 中）
// 实时性优先：直读 RoDB()（new-api logs 表），与原 100 个 loop 效果等价。
func aggregateChannelsBatch(ctx context.Context, chs []dal.ChannelMirror, startTS, endTS int64) (map[int]TickPayload, error) {
	if len(chs) == 0 {
		return map[int]TickPayload{}, nil
	}
	if !dal.HasRoDB() {
		return map[int]TickPayload{}, nil
	}
	// 动态生成 IN (?, ?, ...) 占位符
	placeholders := make([]string, len(chs))
	args := []interface{}{dal.LogTypeError, startTS, endTS}
	for i, ch := range chs {
		placeholders[i] = "?"
		args = append(args, ch.ID)
	}
	sqlStr := fmt.Sprintf(`SELECT channel_id,
       COUNT(*) AS req_cnt,
       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS err_cnt,
       COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens,
       COALESCE(AVG(use_time), 0) AS avg_use_ms
FROM logs WHERE created_at >= ? AND created_at < ? AND channel_id IN (%s)
GROUP BY channel_id`, strings.Join(placeholders, ","))

	type row struct {
		ChannelID int     `gorm:"column:channel_id"`
		ReqCnt    int64   `gorm:"column:req_cnt"`
		ErrCnt    int64   `gorm:"column:err_cnt"`
		Tokens    int64   `gorm:"column:tokens"`
		AvgUseMs  float64 `gorm:"column:avg_use_ms"`
	}
	var rows []row
	if err := dal.RoDB().WithContext(ctx).Raw(sqlStr, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	dur := float64(endTS-startTS) / 60.0
	out := make(map[int]TickPayload, len(rows))
	for _, r := range rows {
		rpm := int(float64(r.ReqCnt) / dur)
		tpm := int64(float64(r.Tokens) / dur)
		er := 0.0
		if r.ReqCnt > 0 {
			er = float64(r.ErrCnt) / float64(r.ReqCnt)
		}
		p95 := int(r.AvgUseMs * 1000 * 1.5)
		if p95 < 100 {
			p95 = 100
		}
		out[r.ChannelID] = TickPayload{
			RPM: rpm, TPM: tpm, ErrorRate: er, P95Latency: p95,
			RequestCnt: r.ReqCnt, ErrorCnt: r.ErrCnt,
		}
	}
	return out, nil
}

// aggregateUsersBatch 1 SQL 拿多个 user 的 5min 聚合
func aggregateUsersBatch(ctx context.Context, uids []uint64, startTS, endTS int64) (map[uint64]TickPayload, error) {
	if len(uids) == 0 {
		return map[uint64]TickPayload{}, nil
	}
	if !dal.HasRoDB() {
		return map[uint64]TickPayload{}, nil
	}
	placeholders := make([]string, len(uids))
	args := []interface{}{dal.LogTypeError, startTS, endTS}
	for i, uid := range uids {
		placeholders[i] = "?"
		args = append(args, uid)
	}
	sqlStr := fmt.Sprintf(`SELECT user_id,
       COUNT(*) AS req_cnt,
       SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS err_cnt,
       COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens,
       COALESCE(AVG(use_time), 0) AS avg_use_ms
FROM logs WHERE created_at >= ? AND created_at < ? AND user_id IN (%s)
GROUP BY user_id`, strings.Join(placeholders, ","))

	type row struct {
		UserID   int     `gorm:"column:user_id"`
		ReqCnt   int64   `gorm:"column:req_cnt"`
		ErrCnt   int64   `gorm:"column:err_cnt"`
		Tokens   int64   `gorm:"column:tokens"`
		AvgUseMs float64 `gorm:"column:avg_use_ms"`
	}
	var rows []row
	if err := dal.RoDB().WithContext(ctx).Raw(sqlStr, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	dur := float64(endTS-startTS) / 60.0
	out := make(map[uint64]TickPayload, len(rows))
	for _, r := range rows {
		rpm := int(float64(r.ReqCnt) / dur)
		tpm := int64(float64(r.Tokens) / dur)
		er := 0.0
		if r.ReqCnt > 0 {
			er = float64(r.ErrCnt) / float64(r.ReqCnt)
		}
		p95 := int(r.AvgUseMs * 1000 * 1.5)
		if p95 < 100 {
			p95 = 100
		}
		out[uint64(r.UserID)] = TickPayload{
			RPM: rpm, TPM: tpm, ErrorRate: er, P95Latency: p95,
			RequestCnt: r.ReqCnt, ErrorCnt: r.ErrCnt,
		}
	}
	return out, nil
}

func listActiveUserIDs(ctx context.Context, startTS, endTS int64, limit int) ([]uint64, error) {
	if !dal.HasRoDB() {
		return nil, nil
	}
	type row struct {
		UserID uint64 `gorm:"column:user_id"`
	}
	var rows []row
	sqlStr := `SELECT user_id FROM logs WHERE created_at >= ? AND created_at < ?
GROUP BY user_id ORDER BY MAX(created_at) DESC LIMIT ?`
	if err := dal.RoDB().WithContext(ctx).Raw(sqlStr, startTS, endTS, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	return ids, nil
}

// ginH 轻量替代：避免在非 handler 包里强依赖 gin
func ginH(kv ...interface{}) map[string]interface{} {
	m := make(map[string]interface{}, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		m[k] = kv[i+1]
	}
	return m
}
