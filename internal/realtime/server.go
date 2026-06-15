// Package realtime: WebSocket 升级 + handler
//
// 路由：
//
//	GET /api/ws/global                  —— 订阅 global
//	GET /api/ws/customer/:id            —— 订阅 customer:<id>
//	GET /api/ws/channel/:id             —— 订阅 channel:<id>
//	GET /api/ws/errors                  —— 订阅 errors
//	GET /api/ws/multiplex               —— 一次性订阅 ?topics=global,customer:1,channel:5
//
// 客户端断线重连（指数退避）—— 由前端实现，建议：
//
//	退避序列：1s, 2s, 4s, 8s, ... 上限 30s
//	收到 tick/error/alert 帧后 reset 退避
//	服务端不感知重连；新连接建立后会重新订阅
//
// 心跳：
//   - 服务端每 30s 发 ping 帧
//   - 客户端 60s 内未响应 pong → 主动断开
package realtime

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 30 * time.Second
	maxMessageSize = 1024 // 客户端不发业务消息；只允许 pong/ping
)

// P0-2 修: WebSocket CheckOrigin 限制到白名单
// 同步 /api/* 的 CORS 白名单 (从 env CORS_ALLOWED_ORIGINS 解析)
// 注意: same-origin (无 Origin 头) 放行 (curl / 工具直连); 跨域必须命中白名单
func isWSOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// same-origin (浏览器同源请求不带 Origin) 或非浏览器客户端
		return true
	}
	raw := os.Getenv("CORS_ALLOWED_ORIGINS")
	defaults := []string{
		"http://localhost:8088",
		"http://127.0.0.1:8088",
		"http://api-ops.example.com:8088",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
	}
	allowed := defaults
	if raw != "" {
		parts := strings.Split(raw, ",")
		allowed = make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				allowed = append(allowed, p)
			}
		}
	}
	for _, a := range allowed {
		if a == "*" || a == origin {
			return true
		}
		// 子域通配
		if strings.Contains(a, "*.") {
			prefix := strings.SplitN(a, "*.", 2)[0]
			suffix := strings.SplitN(a, "*.", 2)[1]
			if strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     isWSOriginAllowed,
}

// WSHandler 接受 (topicFn func(c *gin.Context) []string) —— 由路由分发
// 返回的 []string 是该连接要订阅的主题列表
type WSHandler func(c *gin.Context) []string

// Server 持有 hub 引用
type Server struct {
	hub *Hub
}

// NewServer 创建 WS server
func NewServer(hub *Hub) *Server {
	return &Server{hub: hub}
}

// Mount 把 ws 路由挂到 gin
func (s *Server) Mount(r *gin.Engine, authMW gin.HandlerFunc) {
	g := r.Group("/api/ws")
	if authMW != nil {
		g.Use(authMW)
	}
	g.GET("/global", s.serveTopic(func(c *gin.Context) []string {
		return []string{"global"}
	}))
	g.GET("/errors", s.serveTopic(func(c *gin.Context) []string {
		return []string{"global", "errors"}
	}))
	g.GET("/customer/:id", s.serveTopic(func(c *gin.Context) []string {
		return []string{"global", "customer:" + c.Param("id")}
	}))
	g.GET("/channel/:id", s.serveTopic(func(c *gin.Context) []string {
		return []string{"global", "channel:" + c.Param("id")}
	}))
	g.GET("/multiplex", s.serveTopic(func(c *gin.Context) []string {
		raw := c.Query("topics")
		if raw == "" {
			return []string{"global"}
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts)+1)
		out = append(out, "global")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			out = append(out, p)
		}
		return out
	}))
}

// serveTopic 单个 ws 端点
func (s *Server) serveTopic(topicFn WSHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()

		// 连接级限流
		if !s.hub.limiter.AllowConn(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   gin.H{"message": "too many ws connections from this ip", "code": "WS_CONN_LIMIT"},
			})
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			s.hub.limiter.ReleaseConn(ip)
			log.Printf("[realtime] upgrade failed ip=%s err=%v", ip, err)
			return
		}

		// 注册 client
		s.hub.mu.Lock()
		s.hub.nextID++
		id := s.hub.nextID
		s.hub.mu.Unlock()

		topics := topicFn(c)
		client := &Client{
			hub:    s.hub,
			conn:   conn,
			send:   make(chan []byte, 64),
			topics: make(map[string]bool, len(topics)),
			ip:     ip,
			id:     id,
		}
		for _, t := range topics {
			client.Subscribe(t)
		}

		s.hub.register <- client

		// 推一帧 hello 让客户端确认连接 OK（包含订阅 topic 列表）
		client.SendFrame(Frame{
			Type:    "tick",
			Channel: "system",
			Payload: ginH("hello", true, "topics", topics, "client_id", id),
			TS:      time.Now().UnixMilli(),
		})

		// 启动读写 goroutine
		go client.writePump(s.hub)
		go client.readPump(s.hub)
	}
}

// ===== Client pumps =====

func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.hub.limiter.ReleaseConn(c.ip)
	}()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		// 我们不读客户端消息；仅等待 conn 关闭 / 错误
		if _, _, err := c.conn.NextReader(); err != nil {
			return
		}
	}
}

func (c *Client) writePump(h *Hub) {
	tick := time.NewTicker(pingPeriod)
	defer func() {
		tick.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-tick.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ===== 工具 =====

// ParseTopicID 从 "customer:123" 提取 id
func ParseTopicID(topic, prefix string) (uint64, bool) {
	if !strings.HasPrefix(topic, prefix+":") {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(topic, prefix+":"), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Start 启动 hub 主循环 + tick（一次性）
//   - main.go 在启动时调一次
func Start(ctx context.Context, hub *Hub) {
	go hub.Run(ctx)
	hub.StartTicker(ctx)
}
