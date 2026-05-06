package dispatcher

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HeartbeatLease 后台心跳保持租约活跃
type HeartbeatLease struct {
	client     *http.Client
	serverURL  string
	interval   time.Duration
	projectID  string
	workerName string

	// intent 心跳 或 reason 心跳
	intentID string // 非空时为 intent 心跳
	isReason bool   // true 时为 reason 心跳

	mu            sync.Mutex
	failure       *HeartbeatFailure
	lastSuccessAt time.Time // 最近一次心跳成功时间，用于 grace period 判断
	cancel        context.CancelFunc
	done          chan struct{}

	// onFail 在确认失败时调用（可用于取消任务上下文）
	onFail func()
}

// HeartbeatFailure 心跳失败信息
type HeartbeatFailure struct {
	StatusCode int
	Err        error
}

// NewIntentHeartbeat 创建 intent 心跳
func NewIntentHeartbeat(client *http.Client, serverURL, projectID, intentID, workerName string, interval time.Duration) *HeartbeatLease {
	return &HeartbeatLease{
		client:        client,
		serverURL:     serverURL,
		interval:      interval,
		projectID:     projectID,
		intentID:      intentID,
		workerName:    workerName,
		lastSuccessAt: time.Now(),
		done:          make(chan struct{}),
	}
}

// NewReasonHeartbeat 创建 reason 心跳
func NewReasonHeartbeat(client *http.Client, serverURL, projectID, workerName string, interval time.Duration) *HeartbeatLease {
	return &HeartbeatLease{
		client:        client,
		serverURL:     serverURL,
		interval:      interval,
		projectID:     projectID,
		workerName:    workerName,
		isReason:      true,
		lastSuccessAt: time.Now(),
		done:          make(chan struct{}),
	}
}

// SetOnFail 设置心跳失败时的回调，可用于取消任务上下文
func (h *HeartbeatLease) SetOnFail(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onFail = fn
}

// Start 启动后台心跳 goroutine
func (h *HeartbeatLease) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() {
		defer close(h.done)
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := h.sendHeartbeat(ctx); err != nil {
					return
				}
			}
		}
	}()
}

// Stop 停止心跳
func (h *HeartbeatLease) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	<-h.done
}

// Failed 检查心跳是否失败
func (h *HeartbeatLease) Failed() *HeartbeatFailure {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failure
}

func (h *HeartbeatLease) sendHeartbeat(ctx context.Context) error {
	var url string
	if h.isReason {
		url = fmt.Sprintf("%s/api/projects/%s/reason/heartbeat", h.serverURL, h.projectID)
	} else {
		url = fmt.Sprintf("%s/api/projects/%s/intents/%s/heartbeat", h.serverURL, h.projectID, h.intentID)
	}

	body := fmt.Sprintf(`{"worker":"%s"}`, h.workerName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return nil // context cancelled
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		// 暂时网络错误，在 grace period 内重试，超过则认定失败
		elapsed := time.Since(h.lastSuccessAt)
		grace := h.interval * 2
		log.Printf("[heartbeat] transient error project=%s worker=%s elapsed=%.1fs grace=%.1fs: %v",
			h.projectID, h.workerName, elapsed.Seconds(), grace.Seconds(), err)
		if elapsed < grace {
			return nil // 宽限期内继续重试
		}
		h.fail(0)
		return fmt.Errorf("heartbeat grace period exceeded")
	}
	defer resp.Body.Close()

	// 403/409 表示租约不再有效（项目 inactive 或被其他 worker 抢占）
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusConflict {
		h.fail(resp.StatusCode)
		log.Printf("[heartbeat] lease lost project=%s worker=%s status=%d", h.projectID, h.workerName, resp.StatusCode)
		return fmt.Errorf("lease lost")
	}

	// 心跳成功，更新最近成功时间
	h.mu.Lock()
	h.lastSuccessAt = time.Now()
	h.mu.Unlock()
	return nil
}

// fail 记录心跳失败并触发回调
func (h *HeartbeatLease) fail(statusCode int) {
	h.mu.Lock()
	h.failure = &HeartbeatFailure{StatusCode: statusCode}
	onFail := h.onFail
	h.mu.Unlock()
	if onFail != nil {
		onFail()
	}
}
