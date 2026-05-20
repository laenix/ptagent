package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/gin-gonic/gin"
)

// SSEEvent 服务端推送事件
type SSEEvent struct {
	Type      string      `json:"type"`       // project_update | task_dispatched | task_completed | task_failed | intent_created | intent_concluded | fact_created | metrics
	ProjectID string      `json:"project_id"` // 关联项目
	Data      interface{} `json:"data"`       // 事件数据
}

// SSEHub 管理所有 SSE 客户端连接
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

// NewSSEHub 创建 SSE hub
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe 新增客户端
func (h *SSEHub) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 移除客户端
func (h *SSEHub) Unsubscribe(ch chan SSEEvent) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

// Broadcast 向所有客户端推送事件
func (h *SSEHub) Broadcast(event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- event:
		default:
			// 客户端消费太慢，丢弃
			log.Printf("[sse] dropping event for slow client, type=%s", event.Type)
		}
	}
}

// ClientCount 当前连接数
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// StreamEvents SSE 长连接端点 GET /api/events/stream
func (h *Handler) StreamEvents(c *gin.Context) {
	if h.sseHub == nil {
		c.JSON(500, gin.H{"error": "SSE not available"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	ch := h.sseHub.Subscribe()
	defer h.sseHub.Unsubscribe(ch)

	// Flush 初始连接
	c.Writer.Flush()

	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(c.Writer, "event: %s\n", event.Type)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			c.Writer.Flush()
		}
	}
}

// ReportProgress 接收 dispatcher 进度上报 POST /api/events/report
func (h *Handler) ReportProgress(c *gin.Context) {
	if h.sseHub == nil {
		c.JSON(500, gin.H{"error": "SSE not available"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var event SSEEvent
	if err := json.Unmarshal(body, &event); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	h.sseHub.Broadcast(event)
	c.JSON(200, gin.H{"ok": true})
}
