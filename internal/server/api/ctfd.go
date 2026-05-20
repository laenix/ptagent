package api

import (
	"fmt"
	"net/http"
	"path"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/ctfd"
	"github.com/ptagent/ptagent/internal/models"
)

// --- CTFd Instance Management ---

// ListCTFdInstances GET /api/ctfd/instances
func (h *Handler) ListCTFdInstances(c *gin.Context) {
	instances, err := h.store.ListCTFdInstances(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if instances == nil {
		instances = []models.CTFdInstance{}
	}
	c.JSON(http.StatusOK, instances)
}

// AddCTFdInstance POST /api/ctfd/instances
func (h *Handler) AddCTFdInstance(c *gin.Context) {
	var req models.AddCTFdInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 验证连通性
	client := ctfd.NewClient(req.URL, req.Token)
	if err := client.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot connect to CTFd: " + err.Error()})
		return
	}

	inst, err := h.store.AddCTFdInstance(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, inst)
}

// DeleteCTFdInstance DELETE /api/ctfd/instances/:inst_id
func (h *Handler) DeleteCTFdInstance(c *gin.Context) {
	id := c.Param("inst_id")
	if err := h.store.DeleteCTFdInstance(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- CTFd Challenge Operations ---

func (h *Handler) ctfdClient(c *gin.Context, instID string) (*ctfd.Client, error) {
	inst, err := h.store.GetCTFdInstance(c.Request.Context(), instID)
	if err != nil {
		return nil, fmt.Errorf("instance not found")
	}
	return ctfd.NewClient(inst.URL, inst.Token), nil
}

// ListCTFdChallenges GET /api/ctfd/instances/:inst_id/challenges
func (h *Handler) ListCTFdChallenges(c *gin.Context) {
	instID := c.Param("inst_id")
	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	challenges, err := client.ListChallenges(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, challenges)
}

// GetCTFdChallenge GET /api/ctfd/instances/:inst_id/challenges/:chall_id
func (h *Handler) GetCTFdChallenge(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	ch, err := client.GetChallenge(c.Request.Context(), challID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ch)
}

// SubmitCTFdFlag POST /api/ctfd/instances/:inst_id/challenges/:chall_id/submit
func (h *Handler) SubmitCTFdFlag(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	var req struct {
		Flag string `json:"flag" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	result, err := client.SubmitFlag(c.Request.Context(), challID, req.Flag)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ProxyCTFdFile GET /api/ctfd/instances/:inst_id/files/*filepath
// 附件下载中转
func (h *Handler) ProxyCTFdFile(c *gin.Context) {
	instID := c.Param("inst_id")
	filePath := c.Param("filepath")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file path required"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	body, contentType, contentLen, err := client.ProxyFileDownload(c.Request.Context(), filePath)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer body.Close()

	// 提取文件名
	filename := path.Base(filePath)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", contentType)
	if contentLen > 0 {
		c.Header("Content-Length", strconv.FormatInt(contentLen, 10))
	}
	c.Status(http.StatusOK)
	// Stream the body
	buf := make([]byte, 32*1024)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			c.Writer.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	c.Writer.Flush()
}

// --- CTFd Challenge Instance (靶机) Management ---

// GetCTFdInstance GET /api/ctfd/instances/:inst_id/challenges/:chall_id/instance
// 查询靶机实例状态
func (h *Handler) GetCTFdInstanceStatus(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status, err := client.GetInstance(c.Request.Context(), challID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if status == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"running": true, "instance": status})
}

// StartCTFdInstance POST /api/ctfd/instances/:inst_id/challenges/:chall_id/instance/start
// 启动靶机实例
func (h *Handler) StartCTFdInstance(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status, err := client.StartInstance(c.Request.Context(), challID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"running": true, "instance": status})
}

// StopCTFdInstance POST /api/ctfd/instances/:inst_id/challenges/:chall_id/instance/stop
// 停止靶机实例
func (h *Handler) StopCTFdInstance(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if err := client.StopInstance(c.Request.Context(), challID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"running": false})
}

// RenewCTFdInstance POST /api/ctfd/instances/:inst_id/challenges/:chall_id/instance/renew
// 续期靶机实例
func (h *Handler) RenewCTFdInstance(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if err := client.RenewInstance(c.Request.Context(), challID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"renewed": true})
}

// ImportCTFdChallenge POST /api/ctfd/instances/:inst_id/challenges/:chall_id/import
// 将 CTFd 题目导入为 ptagent 项目
func (h *Handler) ImportCTFdChallenge(c *gin.Context) {
	instID := c.Param("inst_id")
	challID, err := strconv.Atoi(c.Param("chall_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}

	client, err := h.ctfdClient(c, instID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	ch, err := client.GetChallenge(c.Request.Context(), challID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// 构造 origin
	origin := fmt.Sprintf("[CTFd] %s (Category: %s, Value: %d pts)\n\n%s", ch.Name, ch.Category, ch.Value, ch.Description)
	if ch.ConnectionInfo != "" {
		origin += "\n\nConnection Info: " + ch.ConnectionInfo
	}

	// 构造 hints
	var hints []models.CreateHintParams
	// 附件信息作为 hint
	if len(ch.Files) > 0 {
		fileList := "附件下载链接:\n"
		for _, f := range ch.Files {
			proxyURL := fmt.Sprintf("/api/ctfd/instances/%s/files/%s", instID, f.Location)
			fileList += fmt.Sprintf("- %s (proxy: %s)\n", path.Base(f.Location), proxyURL)
		}
		hints = append(hints, models.CreateHintParams{
			Content: fileList,
			Creator: "CTFd-Agent",
		})
	}
	if len(ch.Tags) > 0 {
		tagStr := "Tags: "
		for i, t := range ch.Tags {
			if i > 0 {
				tagStr += ", "
			}
			tagStr += t
		}
		hints = append(hints, models.CreateHintParams{
			Content: tagStr,
			Creator: "CTFd-Agent",
		})
	}

	req := &models.CreateProjectRequest{
		Title:  fmt.Sprintf("[%s] %s", ch.Category, ch.Name),
		Origin: origin,
		Goal:   "Find and submit the flag for this challenge.",
		Hints:  hints,
	}

	project, err := h.store.CreateProject(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 建立 project <-> CTFd challenge 关联
	autoSubmit := ch.MaxAttempts == 0 // 无提交次数限制时自动提交
	if err := h.store.LinkProjectCTFd(c.Request.Context(), &models.CTFdProjectLink{
		ProjectID:       project.ID,
		CTFdInstanceID:  instID,
		CTFdChallengeID: challID,
		AutoSubmit:      autoSubmit,
	}); err != nil {
		// 关联失败不影响项目创建
		fmt.Printf("[ctfd] link project %s to challenge %d failed: %v\n", project.ID, challID, err)
	}

	c.JSON(http.StatusCreated, gin.H{
		"project":   project,
		"challenge": ch,
	})
}
