// monitor ↔ notifier 桥接层
// monitor 触发告警后异步调飞书；通过 interface 解耦避免循环依赖
package monitor

import (
	"context"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/realtime"
)

// AlertSender 抽象：notifier.Notifier 满足此接口
type AlertSender interface {
	SendForAlert(ctx context.Context, h *dal.AlertHistory)
}

// Notifier 全局告警发送器（cmd/server/main.go 启动时注入）
//   - nil 时 fireAlert 跳过推送（仅写 alert_action 记录）
var Notifier AlertSender

// AlertBroadcaster 实时面板广播器：告警触发时同步推一帧到 ws hub
//   - nil 时 fireAlert 跳过 ws 推送
//   - 由 main.go 启动时注入 realtime.BroadcastAlert
var AlertBroadcaster func(realtime.AlertPayload)

// SetAlertBroadcaster 注入广播器（main.go 启动时调）
func SetAlertBroadcaster(fn func(realtime.AlertPayload)) {
	AlertBroadcaster = fn
}
