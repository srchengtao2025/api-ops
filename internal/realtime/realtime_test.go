// Package realtime: WebSocket 端到端测试
// 流程：
//  1. 启动 in-memory HTTP test server（挂 ws 路由）
//  2. 用 gorilla websocket client 连 /api/ws/global
//  3. 5s 内必须收到至少 1 个 type=tick 的帧
//  4. 解析 payload 验证含 rpm / tpm / error_rate 字段
//
// 注意：本测试不依赖真实 DB / Redis（hub 在测试中 ticker 拉 DB 失败时 fan-out 静默跳过）。
// 为确保 5s 内必出 tick，测试中 hub.StartTicker 不调用，改用 Hub.BroadcastGlobal 直接推一帧。
package realtime

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func init() { gin.SetMode(gin.TestMode) }

func TestWSGlobal_ReceivesTickFrame(t *testing.T) {
	// 1) 启 hub
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// 2) 启 http test server
	srv := NewServer(hub)
	r := gin.New()
	srv.Mount(r, nil) // 不挂 auth 中间件
	ts := httptest.NewServer(r)
	defer ts.Close()

	// 3) 客户端连 ws
	wsURL := "ws" + ts.URL[4:] + "/api/ws/global"
	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v (status=%v)", err, resp)
	}
	defer conn.Close()

	// 4) 收 hello 帧（确认连接 OK）
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hello Frame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("read hello failed: %v", err)
	}
	if hello.Channel != "system" {
		t.Errorf("hello channel=%q want=system", hello.Channel)
	}

	// 5) 推一帧 tick（模拟 5s tick）
	hub.BroadcastGlobal(TickPayload{
		RPM: 1262, TPM: 234567, ErrorRate: 0.008, P95Latency: 1200,
		RequestCnt: 6310, ErrorCnt: 50,
	}, time.Now().UnixMilli())

	// 6) 收 tick 帧（5s 截止）
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var tick Frame
	if err := conn.ReadJSON(&tick); err != nil {
		t.Fatalf("read tick failed: %v", err)
	}
	if tick.Type != "tick" {
		t.Errorf("frame type=%q want=tick", tick.Type)
	}
	if tick.Channel != "global" {
		t.Errorf("frame channel=%q want=global", tick.Channel)
	}
	if tick.TS == 0 {
		t.Errorf("frame ts=0 (want non-zero)")
	}
	// 解析 payload
	raw, _ := json.Marshal(tick.Payload)
	var p TickPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("payload unmarshal failed: %v (raw=%s)", err, raw)
	}
	if p.RPM != 1262 {
		t.Errorf("payload rpm=%d want=1262", p.RPM)
	}
	if p.ErrorRate != 0.008 {
		t.Errorf("payload error_rate=%v want=0.008", p.ErrorRate)
	}
	if p.P95Latency != 1200 {
		t.Errorf("payload p95_latency_ms=%d want=1200", p.P95Latency)
	}
}

func TestWSChannel_SubscribeCorrect(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	srv := NewServer(hub)
	r := gin.New()
	srv.Mount(r, nil)
	ts := httptest.NewServer(r)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/api/ws/channel/5"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 收 hello（含 topics）
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hello Frame
	_ = conn.ReadJSON(&hello)
	raw, _ := json.Marshal(hello.Payload)
	if !contains(string(raw), "channel:5") {
		t.Errorf("hello payload missing channel:5 (got=%s)", raw)
	}

	// 推 channel:5 tick
	hub.BroadcastTick("channel:5", TickPayload{RPM: 100, ErrorRate: 0.01, P95Latency: 800}, time.Now().UnixMilli())
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var tick Frame
	if err := conn.ReadJSON(&tick); err != nil {
		t.Fatalf("read tick failed: %v", err)
	}
	if tick.Channel != "channel:5" {
		t.Errorf("frame channel=%q want=channel:5", tick.Channel)
	}

	// 推 channel:99（不应收到）
	hub.BroadcastTick("channel:99", TickPayload{RPM: 999}, time.Now().UnixMilli())
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if err := conn.ReadJSON(&tick); err == nil {
		t.Errorf("received tick for non-subscribed topic channel:99 (should not arrive)")
	}
}

func TestLimiter_BasicAllow(t *testing.T) {
	l := NewLimiter()
	if !l.AllowConn("1.2.3.4") {
		t.Fatal("first conn should be allowed")
	}
	if !l.AllowConn("1.2.3.4") {
		t.Fatal("2nd conn should be allowed")
	}
	// 第 6 个 IP "1.2.3.4" 应被拒
	for i := 0; i < 4; i++ {
		_ = l.AllowConn("1.2.3.4")
	}
	if l.AllowConn("1.2.3.4") {
		t.Fatal("6th conn should be denied (limit=5)")
	}
	// 不同 IP 不受影响
	if !l.AllowConn("5.6.7.8") {
		t.Fatal("other ip first conn should be allowed")
	}
	// 释放一个后再试
	l.ReleaseConn("1.2.3.4")
	if !l.AllowConn("1.2.3.4") {
		t.Fatal("after release, conn should be allowed again")
	}
}

func TestParseTopicID(t *testing.T) {
	if id, ok := ParseTopicID("customer:123", "customer"); !ok || id != 123 {
		t.Errorf("customer:123 → id=123, ok=true; got id=%d ok=%v", id, ok)
	}
	if _, ok := ParseTopicID("channel:5", "customer"); ok {
		t.Error("channel:5 should not parse as customer")
	}
	if _, ok := ParseTopicID("customer:abc", "customer"); ok {
		t.Error("customer:abc should not parse (non-numeric)")
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
