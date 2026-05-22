package ctfd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ptagent/ptagent/internal/models"
)

// Client CTFd API 客户端
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// NewClient 创建 CTFd 客户端
func NewClient(baseURL, token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
	}
}

// --- API wrappers ---

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

// ctfdResp 通用响应包装
type ctfdResp struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

// Ping 测试连通性
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, "GET", "/api/v1/challenges", nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CTFd returned %d", resp.StatusCode)
	}
	return nil
}

// ListChallenges 获取所有题目列表
func (c *Client) ListChallenges(ctx context.Context) ([]models.CTFdChallenge, error) {
	resp, err := c.do(ctx, "GET", "/api/v1/challenges", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("CTFd error: %s", raw.Message)
	}

	var challenges []models.CTFdChallenge
	if err := json.Unmarshal(raw.Data, &challenges); err != nil {
		return nil, fmt.Errorf("parse challenges: %w", err)
	}
	return challenges, nil
}

// GetChallenge 获取单个题目详情（含附件列表）
func (c *Client) GetChallenge(ctx context.Context, id int) (*models.CTFdChallenge, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/challenges/%d", id), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("CTFd error: %s", raw.Message)
	}

	var ch models.CTFdChallenge
	if err := json.Unmarshal(raw.Data, &ch); err != nil {
		return nil, fmt.Errorf("parse challenge: %w", err)
	}

	// 获取附件
	files, err := c.getFiles(ctx, id)
	if err == nil {
		ch.Files = files
	}

	return &ch, nil
}

// getFiles 获取题目附件列表
func (c *Client) getFiles(ctx context.Context, challengeID int) ([]models.CTFdFile, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/challenges/%d/files", challengeID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if !raw.Success {
		return nil, fmt.Errorf("CTFd error: %s", raw.Message)
	}

	var files []models.CTFdFile
	if err := json.Unmarshal(raw.Data, &files); err != nil {
		return nil, fmt.Errorf("parse files: %w", err)
	}
	return files, nil
}

// SubmitFlag 提交 flag
func (c *Client) SubmitFlag(ctx context.Context, challengeID int, flag string) (*models.CTFdSubmitResponse, error) {
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"challenge_id": challengeID,
		"submission":   flag,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := c.do(ctx, "POST", "/api/v1/challenges/attempt", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("CTFd error: %s", raw.Message)
	}

	// CTFd attempt 的响应格式
	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw.Data, &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}

	return &models.CTFdSubmitResponse{
		Status:  result.Status,
		Message: result.Message,
	}, nil
}

// --- Challenge Instance (靶机) Management ---

// InstanceStatus 靶机实例状态
type InstanceStatus struct {
	// CTFd-Whale 返回字段
	UserID    int    `json:"user_id"`
	ChalID    int    `json:"challenge_id"`
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	StartTime int64  `json:"start_time"`
	RenewTime int64  `json:"renew_count"`
	Status    string `json:"status"` // running / stopped / creating
	LanAddr   string `json:"lan_address"`
	Message   string `json:"message"`
}

// GetInstance 查询靶机实例状态 (CTFd-Whale)
func (c *Client) GetInstance(ctx context.Context, challengeID int) (*InstanceStatus, error) {
	resp, err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/plugins/ctfd-whale/container?challenge_id=%d", challengeID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// 404 或空 data 表示没有运行中的实例
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		// "no container" 不算错误
		if strings.Contains(strings.ToLower(raw.Message), "no container") ||
			strings.Contains(strings.ToLower(raw.Message), "not found") {
			return nil, nil
		}
		return nil, fmt.Errorf("CTFd error: %s", raw.Message)
	}

	var status InstanceStatus
	if err := json.Unmarshal(raw.Data, &status); err != nil {
		return nil, fmt.Errorf("parse instance: %w", err)
	}
	return &status, nil
}

// StartInstance 启动靶机实例 (CTFd-Whale)
func (c *Client) StartInstance(ctx context.Context, challengeID int) (*InstanceStatus, error) {
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"challenge_id": challengeID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := c.do(ctx, "POST", "/api/v1/plugins/ctfd-whale/container", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("CTFd error: %s", raw.Message)
	}

	var status InstanceStatus
	if err := json.Unmarshal(raw.Data, &status); err != nil {
		// 某些版本 data 为 null 但 success=true
		return &InstanceStatus{ChalID: challengeID, Status: "creating"}, nil
	}
	return &status, nil
}

// StopInstance 停止靶机实例 (CTFd-Whale)
func (c *Client) StopInstance(ctx context.Context, challengeID int) error {
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"challenge_id": challengeID,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := c.do(ctx, "DELETE", "/api/v1/plugins/ctfd-whale/container", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		return fmt.Errorf("CTFd error: %s", raw.Message)
	}
	return nil
}

// RenewInstance 续期靶机实例 (CTFd-Whale)
func (c *Client) RenewInstance(ctx context.Context, challengeID int) error {
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"challenge_id": challengeID,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := c.do(ctx, "PATCH", "/api/v1/plugins/ctfd-whale/container", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw ctfdResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if !raw.Success {
		return fmt.Errorf("CTFd error: %s", raw.Message)
	}
	return nil
}

// ProxyFileDownload 代理附件下载，返回原始 response body 和 content-type
func (c *Client) ProxyFileDownload(ctx context.Context, filePath string) (io.ReadCloser, string, int64, error) {
	// filePath 是 CTFd 返回的相对路径，如 "/files/xxxx/flag.zip"
	url := c.baseURL + "/" + strings.TrimLeft(filePath, "/")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", 0, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return resp.Body, contentType, resp.ContentLength, nil
}
