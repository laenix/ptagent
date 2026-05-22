package api

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/agent"
	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/dispatcher"
	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/store"
	"github.com/ptagent/ptagent/internal/toollogger"
)

// Handler API 处理器
type Handler struct {
	store       store.Store
	dispatchers *dispatcher.Manager
	sseHub      *SSEHub
	agentMu     sync.RWMutex
	agent       *agent.PlatformAgent
	toolLogger  *toollogger.Logger
}

// NewHandler 创建 API handler
func NewHandler(s store.Store) *Handler {
	return &Handler{store: s, sseHub: NewSSEHub()}
}

// SSEHub 返回 SSE hub 引用（供外部广播事件）
func (h *Handler) SSEHub() *SSEHub {
	return h.sseHub
}

// SetDispatcherManager 设置 dispatcher 管理器（可选）
func (h *Handler) SetDispatcherManager(m *dispatcher.Manager) {
	h.dispatchers = m
}

// SetPlatformAgent 设置平台 Agent（可选）
func (h *Handler) SetPlatformAgent(a *agent.PlatformAgent) {
	h.agentMu.Lock()
	h.agent = a
	h.agentMu.Unlock()
}

// SetToolLogger 设置工具事件日志记录器
func (h *Handler) SetToolLogger(logger *toollogger.Logger) {
	h.toolLogger = logger
}

// RegisterRoutes 注册所有路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		// Settings
		api.GET("/settings", h.GetSettings)
		api.PUT("/settings", h.UpdateSettings)

		// Projects
		api.GET("/projects", h.ListProjects)
		api.POST("/projects", h.CreateProject)
		api.GET("/projects/:id", h.GetProject)
		api.DELETE("/projects/:id", h.DeleteProject)
		api.PUT("/projects/:id/title", h.UpdateProjectTitle)
		api.PUT("/projects/:id/status", h.UpdateProjectStatus)

		// Reason lease
		api.POST("/projects/:id/reason/claim", h.ClaimReason)
		api.POST("/projects/:id/reason/heartbeat", h.HeartbeatReason)
		api.POST("/projects/:id/reason/release", h.ReleaseReason)

		// Intents
		api.POST("/projects/:id/intents", h.CreateIntent)
		api.POST("/projects/:id/intents/:intent_id/heartbeat", h.HeartbeatIntent)
		api.POST("/projects/:id/intents/:intent_id/release", h.ReleaseIntent)
		api.POST("/projects/:id/intents/:intent_id/conclude", h.ConcludeIntent)

		// Complete / Reopen
		api.POST("/projects/:id/complete", h.CompleteProject)
		api.POST("/projects/:id/reopen", h.ReopenProject)

		// Hints
		api.POST("/projects/:id/hints", h.CreateHint)

		// Export
		api.GET("/projects/:id/export", h.ExportProject)

		// Task Events (timeline replay)
		api.POST("/projects/:id/events", h.RecordTaskEvent)
		api.GET("/projects/:id/events", h.ListTaskEvents)
		api.GET("/projects/:id/events/:event_id", h.GetTaskEvent)

		// Tool Events (tool call logging)
		api.POST("/projects/:id/tools", h.RecordToolEvent)
		api.GET("/projects/:id/tools", h.ListToolEvents)

		// Dispatcher management
		api.GET("/dispatchers", h.ListDispatchers)
		api.POST("/dispatchers", h.CreateDispatcher)
		api.GET("/dispatchers/:disp_id", h.GetDispatcher)
		api.POST("/dispatchers/:disp_id/start", h.StartDispatcher)
		api.POST("/dispatchers/:disp_id/stop", h.StopDispatcher)
		api.DELETE("/dispatchers/:disp_id", h.DeleteDispatcher)

		// SSE streaming
		api.GET("/events/stream", h.StreamEvents)
		api.POST("/events/report", h.ReportProgress)

		// Metrics
		api.GET("/metrics", h.GetMetrics)

		// Platform Agent
		api.POST("/agent/chat", h.AgentChat)
		api.GET("/agent/config", h.GetAgentConfig)
		api.PUT("/agent/config", h.UpdateAgentConfig)

		// Project CTFd Link
		api.GET("/projects/:id/ctfd-link", h.GetProjectCTFdLink)

		// CTFd Integration
		ctfdGroup := api.Group("/ctfd")
		{
			ctfdGroup.GET("/instances", h.ListCTFdInstances)
			ctfdGroup.POST("/instances", h.AddCTFdInstance)
			ctfdGroup.DELETE("/instances/:inst_id", h.DeleteCTFdInstance)
			ctfdGroup.GET("/instances/:inst_id/challenges", h.ListCTFdChallenges)
			ctfdGroup.GET("/instances/:inst_id/challenges/:chall_id", h.GetCTFdChallenge)
			ctfdGroup.POST("/instances/:inst_id/challenges/:chall_id/submit", h.SubmitCTFdFlag)
			ctfdGroup.POST("/instances/:inst_id/challenges/:chall_id/import", h.ImportCTFdChallenge)
			ctfdGroup.GET("/instances/:inst_id/challenges/:chall_id/instance", h.GetCTFdInstanceStatus)
			ctfdGroup.POST("/instances/:inst_id/challenges/:chall_id/instance/start", h.StartCTFdInstance)
			ctfdGroup.POST("/instances/:inst_id/challenges/:chall_id/instance/stop", h.StopCTFdInstance)
			ctfdGroup.POST("/instances/:inst_id/challenges/:chall_id/instance/renew", h.RenewCTFdInstance)
			ctfdGroup.GET("/instances/:inst_id/files/*filepath", h.ProxyCTFdFile)
		}
	}
}

// --- Settings ---

func (h *Handler) GetSettings(c *gin.Context) {
	settings, err := h.store.GetSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	var req models.Settings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.store.UpdateSettings(c.Request.Context(), &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, req)
}

// --- Projects ---

func (h *Handler) ListProjects(c *gin.Context) {
	projects, err := h.store.ListProjects(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, projects)
}

func (h *Handler) CreateProject(c *gin.Context) {
	var req models.CreateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	project, err := h.store.CreateProject(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, project)
}

func (h *Handler) GetProject(c *gin.Context) {
	id := c.Param("id")
	detail, err := h.store.GetProject(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) DeleteProject(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteProject(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *Handler) UpdateProjectTitle(c *gin.Context) {
	id := c.Param("id")
	var req models.TitleUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.store.UpdateProjectTitle(c.Request.Context(), id, req.Title)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) UpdateProjectStatus(c *gin.Context) {
	id := c.Param("id")
	var req models.StatusUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Status != models.ProjectStatusActive && req.Status != models.ProjectStatusStopped {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only active/stopped allowed via this endpoint"})
		return
	}
	p, err := h.store.UpdateProjectStatus(c.Request.Context(), id, req.Status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// --- Reason lease ---

func (h *Handler) ClaimReason(c *gin.Context) {
	id := c.Param("id")
	var req models.ReasonClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.store.ClaimReason(c.Request.Context(), id, &req)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) HeartbeatReason(c *gin.Context) {
	id := c.Param("id")
	var req models.HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.store.HeartbeatReason(c.Request.Context(), id, req.Worker)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) ReleaseReason(c *gin.Context) {
	id := c.Param("id")
	var req models.HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.store.ReleaseReason(c.Request.Context(), id, req.Worker)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// --- Intents ---

func (h *Handler) CreateIntent(c *gin.Context) {
	projectID := c.Param("id")
	var req models.CreateIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	intent, err := h.store.CreateIntent(c.Request.Context(), projectID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.sseHub.Broadcast(SSEEvent{Type: "intent_created", ProjectID: projectID, Data: intent})
	c.JSON(http.StatusCreated, intent)
}

func (h *Handler) HeartbeatIntent(c *gin.Context) {
	projectID := c.Param("id")
	intentID := c.Param("intent_id")
	var req models.HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	intent, err := h.store.HeartbeatIntent(c.Request.Context(), projectID, intentID, req.Worker)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, intent)
}

func (h *Handler) ReleaseIntent(c *gin.Context) {
	projectID := c.Param("id")
	intentID := c.Param("intent_id")
	var req models.HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	intent, err := h.store.ReleaseIntent(c.Request.Context(), projectID, intentID, req.Worker)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, intent)
}

func (h *Handler) ConcludeIntent(c *gin.Context) {
	projectID := c.Param("id")
	intentID := c.Param("intent_id")
	var req models.ConcludeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.store.ConcludeIntent(c.Request.Context(), projectID, intentID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.sseHub.Broadcast(SSEEvent{Type: "intent_concluded", ProjectID: projectID, Data: resp})
	h.sseHub.Broadcast(SSEEvent{Type: "fact_created", ProjectID: projectID, Data: resp.Fact})
	c.JSON(http.StatusOK, resp)
}

// --- Complete / Reopen ---

func (h *Handler) CompleteProject(c *gin.Context) {
	projectID := c.Param("id")
	var req models.CompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.store.CompleteProject(c.Request.Context(), projectID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) ReopenProject(c *gin.Context) {
	projectID := c.Param("id")
	var req models.ReopenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.store.ReopenProject(c.Request.Context(), projectID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// --- Hints ---

func (h *Handler) CreateHint(c *gin.Context) {
	projectID := c.Param("id")
	var req models.CreateHintParams
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hint, err := h.store.CreateHint(c.Request.Context(), projectID, req.Content, req.Creator)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, hint)
}

// --- Export ---

func (h *Handler) ExportProject(c *gin.Context) {
	projectID := c.Param("id")
	format := c.DefaultQuery("format", "yaml")

	switch format {
	case "yaml":
		data, err := h.store.ExportYAML(c.Request.Context(), projectID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "text/yaml", data)
	case "timeline":
		data, err := h.store.ExportTimeline(c.Request.Context(), projectID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.String(http.StatusOK, data)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported format"})
	}
}

// --- Dispatcher Management ---

func (h *Handler) RecordTaskEvent(c *gin.Context) {
	projectID := c.Param("id")
	var event models.TaskEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	event.ProjectID = projectID
	if err := h.store.RecordTaskEvent(c.Request.Context(), &event); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 广播任务事件
	eventType := "task_update"
	switch event.Phase {
	case "dispatched":
		eventType = "task_dispatched"
	case "succeed":
		eventType = "task_completed"
	case "failed":
		eventType = "task_failed"
	}
	h.sseHub.Broadcast(SSEEvent{Type: eventType, ProjectID: projectID, Data: event})
	c.JSON(http.StatusCreated, event)
}

func (h *Handler) ListTaskEvents(c *gin.Context) {
	projectID := c.Param("id")
	var filter models.TaskEventFilter
	_ = c.ShouldBindQuery(&filter)
	events, err := h.store.ListTaskEvents(c.Request.Context(), projectID, &filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if events == nil {
		events = []models.TaskEvent{}
	}
	c.JSON(http.StatusOK, events)
}

func (h *Handler) GetTaskEvent(c *gin.Context) {
	projectID := c.Param("id")
	eventIDStr := c.Param("event_id")
	eventID, err := strconv.ParseInt(eventIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event_id"})
		return
	}
	event, err := h.store.GetTaskEvent(c.Request.Context(), projectID, eventID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, event)
}

func (h *Handler) ListDispatchers(c *gin.Context) {
	if h.dispatchers == nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}
	c.JSON(http.StatusOK, h.dispatchers.List())
}

func (h *Handler) GetDispatcher(c *gin.Context) {
	if h.dispatchers == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dispatcher management not available"})
		return
	}
	id := c.Param("disp_id")
	info, err := h.dispatchers.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

func (h *Handler) CreateDispatcher(c *gin.Context) {
	if h.dispatchers == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dispatcher management not available"})
		return
	}

	var req struct {
		Name       string `json:"name"`
		ConfigPath string `json:"config_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ConfigPath == "" {
		req.ConfigPath = "./configs/dispatch.yaml"
	}
	if req.Name == "" {
		req.Name = "dispatcher"
	}

	cfg, err := config.LoadDispatchConfig(req.ConfigPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "load config: " + err.Error()})
		return
	}

	info, err := h.dispatchers.Create(cfg, req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, info)
}

func (h *Handler) StartDispatcher(c *gin.Context) {
	if h.dispatchers == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dispatcher management not available"})
		return
	}
	id := c.Param("disp_id")
	if err := h.dispatchers.Start(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	info, _ := h.dispatchers.Get(id)
	c.JSON(http.StatusOK, info)
}

func (h *Handler) StopDispatcher(c *gin.Context) {
	if h.dispatchers == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dispatcher management not available"})
		return
	}
	id := c.Param("disp_id")
	if err := h.dispatchers.Stop(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	info, _ := h.dispatchers.Get(id)
	c.JSON(http.StatusOK, info)
}

func (h *Handler) DeleteDispatcher(c *gin.Context) {
	if h.dispatchers == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dispatcher management not available"})
		return
	}
	id := c.Param("disp_id")
	if err := h.dispatchers.Delete(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Tool Events ---

func (h *Handler) RecordToolEvent(c *gin.Context) {
	projectID := c.Param("id")
	var event models.ToolEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	event.ProjectID = projectID
	if err := h.store.RecordToolEvent(c.Request.Context(), &event); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, event)
}

func (h *Handler) ListToolEvents(c *gin.Context) {
	projectID := c.Param("id")
	var filter models.ToolEventFilter
	_ = c.ShouldBindQuery(&filter)

	if h.toolLogger == nil {
		c.JSON(http.StatusOK, []models.ToolEvent{})
		return
	}

	limit := 100
	if filter.Limit > 0 && filter.Limit <= 500 {
		limit = filter.Limit
	}

	events, err := h.toolLogger.ReadByProject(projectID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if events == nil {
		events = []models.ToolEvent{}
	}
	c.JSON(http.StatusOK, events)
}
