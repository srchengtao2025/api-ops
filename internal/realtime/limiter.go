// Package realtime: Redis 限流器
// 决策（Q 决策 ws 5+100）：
//   - 5 conn / IP（连接级）
//   - 100 msg / min / conn（消息级）
//
// Redis 不可用时降级为内存计数（in-process 限流；多副本部署时每实例独立）
package realtime

import (
	"context"
	"sync"
	"time"

	"github.com/api-ops/api-ops/internal/dal"
)

// Limiter 限流器
type Limiter struct {
	mu         sync.Mutex
	ipConn     map[string]int       // IP -> 当前连接数（连接级）
	ipConnSet  map[string]time.Time // IP -> 首次连接时间（用于过期清理）
	msgPerMin  map[string]int       // key (ip+connID) -> 当前窗口计数
	msgResetAt map[string]time.Time
	nowFn      func() time.Time
}

// NewLimiter 创建限流器
func NewLimiter() *Limiter {
	return &Limiter{
		ipConn:     make(map[string]int),
		ipConnSet:  make(map[string]time.Time),
		msgPerMin:  make(map[string]int),
		msgResetAt: make(map[string]time.Time),
		nowFn:      time.Now,
	}
}

// Limits
const (
	MaxConnPerIP    = 5
	MaxMsgPerMinute = 100
)

// AllowConn 询问是否允许该 IP 新建连接
// 返回 true 表示允许；false 表示超限
func (l *Limiter) AllowConn(ip string) bool {
	if dal.RDB == nil {
		return l.allowConnLocal(ip)
	}
	// Redis INCR：upstream:ops:ws:conn:<ip>，TTL 60s
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	key := "upstream:ops:ws:conn:" + ip
	n, err := dal.RDB.Incr(ctx, key).Result()
	if err != nil {
		return l.allowConnLocal(ip)
	}
	if n == 1 {
		_ = dal.RDB.Expire(ctx, key, 60*time.Second).Err()
	}
	return n <= int64(MaxConnPerIP)
}

// ReleaseConn 连接断开时减计数
func (l *Limiter) ReleaseConn(ip string) {
	if dal.RDB == nil {
		l.releaseConnLocal(ip)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	key := "upstream:ops:ws:conn:" + ip
	_ = dal.RDB.Decr(ctx, key).Err()
}

// AllowMsg 询问是否允许向该 IP 发送下一条消息（100/min）
func (l *Limiter) AllowMsg(ip string) bool {
	if dal.RDB == nil {
		return l.allowMsgLocal(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	key := "upstream:ops:ws:msg:" + ip
	n, err := dal.RDB.Incr(ctx, key).Result()
	if err != nil {
		return l.allowMsgLocal(ip)
	}
	if n == 1 {
		_ = dal.RDB.Expire(ctx, key, 60*time.Second).Err()
	}
	return n <= int64(MaxMsgPerMinute)
}

// ===== 内存降级实现（Redis 不可用） =====

func (l *Limiter) allowConnLocal(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowFn()
	l.gcLocked(now)
	if l.ipConn[ip] >= MaxConnPerIP {
		return false
	}
	l.ipConn[ip]++
	l.ipConnSet[ip] = now
	return true
}

func (l *Limiter) releaseConnLocal(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ipConn[ip]--
	if l.ipConn[ip] <= 0 {
		delete(l.ipConn, ip)
		delete(l.ipConnSet, ip)
	}
}

func (l *Limiter) allowMsgLocal(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowFn()
	key := "m:" + ip
	if resetAt, ok := l.msgResetAt[key]; !ok || now.After(resetAt) {
		l.msgPerMin[key] = 0
		l.msgResetAt[key] = now.Add(time.Minute)
	}
	l.msgPerMin[key]++
	return l.msgPerMin[key] <= MaxMsgPerMinute
}

func (l *Limiter) gcLocked(now time.Time) {
	for ip, t := range l.ipConnSet {
		if now.Sub(t) > 2*time.Minute {
			delete(l.ipConn, ip)
			delete(l.ipConnSet, ip)
		}
	}
	for k, t := range l.msgResetAt {
		if now.After(t.Add(2 * time.Minute)) {
			delete(l.msgPerMin, k)
			delete(l.msgResetAt, k)
		}
	}
}
