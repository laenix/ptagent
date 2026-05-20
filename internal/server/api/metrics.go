package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/models"
)

// ProjectMetrics 单个项目的指标
type ProjectMetrics struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Status               string `json:"status"`
	FactCount            int    `json:"fact_count"`
	IntentCount          int    `json:"intent_count"`
	OpenIntentCount      int    `json:"open_intent_count"`
	ConcludedIntentCount int    `json:"concluded_intent_count"`
	WorkingIntentCount   int    `json:"working_intent_count"`
	UnclaimedIntentCount int    `json:"unclaimed_intent_count"`
	HintCount            int    `json:"hint_count"`
	SuccessFactCount     int    `json:"success_fact_count"`
	FailureFactCount     int    `json:"failure_fact_count"`
	BlockerFactCount     int    `json:"blocker_fact_count"`
}

// MetricsResponse 全局指标响应
type MetricsResponse struct {
	TotalProjects     int              `json:"total_projects"`
	ActiveProjects    int              `json:"active_projects"`
	CompletedProjects int              `json:"completed_projects"`
	StoppedProjects   int              `json:"stopped_projects"`
	TotalFacts        int              `json:"total_facts"`
	TotalIntents      int              `json:"total_intents"`
	TotalOpenIntents  int              `json:"total_open_intents"`
	TotalHints        int              `json:"total_hints"`
	SSEClients        int              `json:"sse_clients"`
	Projects          []ProjectMetrics `json:"projects"`
}

// GetMetrics GET /api/metrics
func (h *Handler) GetMetrics(c *gin.Context) {
	projects, err := h.store.ListProjects(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := MetricsResponse{
		Projects: make([]ProjectMetrics, 0, len(projects)),
	}

	// 批量查询所有项目的 fact 标签统计，避免 N+1
	tagCounts, _ := h.store.CountFactTags(c.Request.Context())
	if tagCounts == nil {
		tagCounts = make(map[string]models.FactTagCounts)
	}

	for _, p := range projects {
		resp.TotalProjects++
		switch p.Status {
		case "active":
			resp.ActiveProjects++
		case "completed":
			resp.CompletedProjects++
		case "stopped":
			resp.StoppedProjects++
		}

		openIntents := p.WorkingIntentCount + p.UnclaimedIntentCount
		concludedIntents := p.IntentCount - openIntents

		pm := ProjectMetrics{
			ID:                   p.ID,
			Title:                p.Title,
			Status:               string(p.Status),
			FactCount:            p.FactCount,
			IntentCount:          p.IntentCount,
			OpenIntentCount:      openIntents,
			ConcludedIntentCount: concludedIntents,
			WorkingIntentCount:   p.WorkingIntentCount,
			UnclaimedIntentCount: p.UnclaimedIntentCount,
			HintCount:            p.HintCount,
		}

		if tc, ok := tagCounts[p.ID]; ok {
			pm.SuccessFactCount = tc.SuccessCount
			pm.FailureFactCount = tc.FailureCount
			pm.BlockerFactCount = tc.BlockerCount
		}

		resp.TotalFacts += p.FactCount
		resp.TotalIntents += p.IntentCount
		resp.TotalOpenIntents += openIntents
		resp.TotalHints += p.HintCount
		resp.Projects = append(resp.Projects, pm)
	}

	if h.sseHub != nil {
		resp.SSEClients = h.sseHub.ClientCount()
	}

	c.JSON(http.StatusOK, resp)
}
