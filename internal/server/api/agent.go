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
	// 返回当前 agent 配置（不返回 API key）
	h.agentMu.RLock()
	configured := h.agent != nil
	h.agentMu.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"configured": configured,
	})
}

// UpdateAgentConfig PUT /api/agent/config
func (h *Handler) UpdateAgentConfig(c *gin.Context) {
	var cfg agent.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.agentMu.Lock()
	h.agent = agent.New(h.store, &cfg)
	h.agentMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"configured": true})
}
