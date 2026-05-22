package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/agent"
	"github.com/ptagent/ptagent/internal/models"
)

// AgentChat POST /api/agent/chat
func (h *Handler) AgentChat(c *gin.Context) {
	h.agentMu.RLock()
	a := h.agent
	h.agentMu.RUnlock()
	if a == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Platform agent not configured. Set LLM config via PUT /api/agent/config."})
		return
	}

	var req models.AgentChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := a.Chat(c.Request.Context(), req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetAgentConfig GET /api/agent/config
func (h *Handler) GetAgentConfig(c *gin.Context) {
	cfg, err := h.store.GetAgentConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"configured": false})
		return
	}
	// 不返回 API key
	c.JSON(http.StatusOK, gin.H{
		"configured":    cfg.LLMAPIKey != "",
		"llm_base_url":  cfg.LLMBaseURL,
		"llm_model":     cfg.LLMModel,
	})
}

// UpdateAgentConfig PUT /api/agent/config
func (h *Handler) UpdateAgentConfig(c *gin.Context) {
	var req models.AgentConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 持久化到数据库
	if err := h.store.UpdateAgentConfig(c.Request.Context(), &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 更新内存中的 agent
	agentCfg := &agent.Config{
		LLMBaseURL: req.LLMBaseURL,
		LLMAPIKey:  req.LLMAPIKey,
		LLMModel:   req.LLMModel,
	}
	h.agentMu.Lock()
	h.agent = agent.New(h.store, agentCfg)
	h.agentMu.Unlock()

	c.JSON(http.StatusOK, gin.H{"configured": true})
}

// GetProjectCTFdLink GET /api/projects/:id/ctfd-link
func (h *Handler) GetProjectCTFdLink(c *gin.Context) {
	projectID := c.Param("id")
	link, err := h.store.GetProjectCTFdLink(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CTFd link not found"})
		return
	}
	c.JSON(http.StatusOK, link)
}
