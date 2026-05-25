package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/tools"
)

const (
	containerPrefix = "ptagent-worker-"
	defaultTimeout  = 120 // seconds
)

// Manager 管理项目级 Docker 容器 (Cairn-style: sleep infinity + docker exec)
type Manager struct {
	cfg        *config.ContainerConfig
	docker     *client.Client
	httpClient *http.Client
	serverURL  string
	mu         sync.Mutex
	// projectID -> container info
	containers map[string]*ContainerInfo
}

// ContainerInfo 容器信息
type ContainerInfo struct {
	ID              string
	Name            string
	ProjectID      string
	CTFdInstanceID  string
	CTFdChallengeID int
}

// New 创建容器管理器
func New(cfg *config.ContainerConfig, serverURL string) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("docker ping: %w", err)
	}

	return &Manager{
		cfg:        cfg,
		docker:     cli,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		serverURL:  serverURL,
		containers: make(map[string]*ContainerInfo),
	}, nil
}

// Close 关闭 Docker 客户端
func (m *Manager) Close() error {
	return m.docker.Close()
}

// fetchCTFdLink 从 Server 获取项目的 CTFd link 信息
func (m *Manager) fetchCTFdLink(ctx context.Context, projectID string) (*models.CTFdProjectLink, error) {
	if m.serverURL == "" {
		return nil, fmt.Errorf("PTAGENT_SERVER not configured")
	}
	url := fmt.Sprintf("%s/api/projects/%s/ctfd-link", m.serverURL, projectID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var link models.CTFdProjectLink
	if err := json.NewDecoder(resp.Body).Decode(&link); err != nil {
		return nil, err
	}
	return &link, nil
}

// resolveCTFdInfo resolves instanceID and challengeID from args, info, or API fallback.
// Returns (instanceID, challengeID, error). If only instanceID is empty but link is found, it will be filled.
func (m *Manager) resolveCTFdInfo(ctx context.Context, info *ContainerInfo, args map[string]interface{}) (string, int, error) {
	instanceID, _ := args["instance_id"].(string)
	challengeID, _ := args["challenge_id"].(float64)

	// 使用容器信息中的 CTFd 配置作为默认值
	if instanceID == "" {
		instanceID = info.CTFdInstanceID
	}
	if challengeID == 0 {
		challengeID = float64(info.CTFdChallengeID)
	}

	// 如果仍然为空，尝试从 API 获取
	if (instanceID == "" || challengeID == 0) && info.ProjectID != "" && m.serverURL != "" {
		if link := m.fetchProjectCTFdLink(ctx, info.ProjectID); link != nil {
			if instanceID == "" {
				instanceID = link.CTFdInstanceID
			}
			if challengeID == 0 {
				challengeID = float64(link.CTFdChallengeID)
			}
		}
	}

	if instanceID == "" || challengeID == 0 {
		return "", 0, fmt.Errorf("instance_id and challenge_id are required (or PTAGENT_CTFD_INSTANCE_ID and PTAGENT_CTFD_CHALLENGE_ID env vars)")
	}
	return instanceID, int(challengeID), nil
}

// fetchProjectCTFdLink 获取项目的 CTFd link 信息（项目字段优先，回退到 ctfd_project_links 表）
func (m *Manager) fetchProjectCTFdLink(ctx context.Context, projectID string) *models.CTFdProjectLink {
	if m.serverURL == "" {
		return nil
	}
	url := fmt.Sprintf("%s/api/projects/%s/ctfd-link", m.serverURL, projectID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var link models.CTFdProjectLink
	if err := json.NewDecoder(resp.Body).Decode(&link); err != nil {
		return nil
	}
	return &link
}

// EnsureRunning 确保项目容器运行中 (Cairn-style: sleep infinity)
func (m *Manager) EnsureRunning(ctx context.Context, projectID string) (*ContainerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if info, ok := m.containers[projectID]; ok {
		if m.isRunning(ctx, info.ID) {
			return info, nil
		}
		delete(m.containers, projectID)
	}

	name := m.containerName(projectID)

	// 检查是否已存在同名容器
	existing := m.findByName(ctx, name)
	if existing != "" {
		if err := m.docker.ContainerStart(ctx, existing, container.StartOptions{}); err != nil {
			log.Printf("[container] start existing failed: %v, recreating", err)
			m.docker.ContainerRemove(ctx, existing, container.RemoveOptions{Force: true})
		} else {
			info := &ContainerInfo{ID: existing, Name: name, ProjectID: projectID}
			// 获取 CTFd link 信息
			if ctfdLink, err := m.fetchCTFdLink(ctx, projectID); err == nil && ctfdLink != nil {
				info.CTFdInstanceID = ctfdLink.CTFdInstanceID
				info.CTFdChallengeID = ctfdLink.CTFdChallengeID
			}
			m.containers[projectID] = info
			log.Printf("[container] restarted project=%s container=%s", projectID, name)
			return info, nil
		}
	}

	info, err := m.createContainer(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	m.containers[projectID] = info
	return info, nil
}

// ExecuteTool 通过 docker exec 在容器内执行工具 (Cairn-style)
func (m *Manager) ExecuteTool(ctx context.Context, info *ContainerInfo, toolName string, args map[string]interface{}) *tools.ToolResult {
	switch toolName {
	case "shell_exec":
		return m.execShell(ctx, info, args)
	case "python_exec":
		return m.execPython(ctx, info, args)
	case "http_request":
		return m.execHTTPRequest(ctx, info, args)
	case "get_project_ctfd_info":
		return m.getProjectCTFdInfo(ctx, info, args)
	case "start_challenge_instance":
		return m.startChallengeInstance(ctx, info, args)
	case "stop_challenge_instance":
		return m.stopChallengeInstance(ctx, info, args)
	case "get_challenge_instance_status":
		return m.getChallengeInstanceStatus(ctx, info, args)
	case "renew_challenge_instance":
		return m.renewChallengeInstance(ctx, info, args)
	case "submit_ctfd_flag":
		return m.submitCTFdFlag(ctx, info, args)
	default:
		return &tools.ToolResult{Error: fmt.Sprintf("unknown tool: %s", toolName)}
	}
}

// execShell 在容器内执行 shell 命令
func (m *Manager) execShell(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	command, _ := args["command"].(string)
	if command == "" {
		return &tools.ToolResult{Error: "command is required"}
	}

	timeout := defaultTimeout
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = int(t)
	}

	// 使用 timeout 命令包裹，和 Cairn 一样
	cmd := []string{"/bin/bash", "-c", fmt.Sprintf("timeout -k 5s %ds %s", timeout, command)}
	return m.dockerExec(ctx, info, cmd, timeout+10)
}

// execPython 在容器内执行 Python 代码
func (m *Manager) execPython(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	code, _ := args["code"].(string)
	if code == "" {
		return &tools.ToolResult{Error: "code is required"}
	}

	cmd := []string{"python3", "-c", code}
	return m.dockerExec(ctx, info, cmd, defaultTimeout)
}

// execHTTPRequest 在容器内使用 curl 执行 HTTP 请求
func (m *Manager) execHTTPRequest(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return &tools.ToolResult{Error: "url is required"}
	}

	method, _ := args["method"].(string)
	if method == "" {
		method = "GET"
	}

	// 构造 curl 命令
	curlArgs := []string{"curl", "-s", "-S", "-i", "--max-time", "30", "-X", method}

	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if sv, ok := v.(string); ok {
				curlArgs = append(curlArgs, "-H", fmt.Sprintf("%s: %s", k, sv))
			}
		}
	}

	if body, ok := args["body"].(string); ok && body != "" {
		curlArgs = append(curlArgs, "-d", body)
	}

	curlArgs = append(curlArgs, rawURL)
	return m.dockerExec(ctx, info, curlArgs, 35)
}

// dockerExec 在容器内执行命令 (核心方法，类似 Cairn 的 ManagedProcess)
func (m *Manager) dockerExec(ctx context.Context, info *ContainerInfo, cmd []string, timeoutSec int) *tools.ToolResult {
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	// 注入环境变量
	if m.cfg.ProxyEnv != nil {
		env := make([]string, 0, len(m.cfg.ProxyEnv))
		for k, v := range m.cfg.ProxyEnv {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		execCfg.Env = env
	}

	execResp, err := m.docker.ContainerExecCreate(execCtx, info.ID, execCfg)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("exec create: %v", err)}
	}

	attachResp, err := m.docker.ContainerExecAttach(execCtx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("exec attach: %v", err)}
	}
	defer attachResp.Close()

	// 读取输出
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, attachResp.Reader)
		done <- err
	}()

	select {
	case <-execCtx.Done():
		// 关闭连接以解除 io.Copy 阻塞，然后等待 goroutine 退出避免数据竞争
		attachResp.Close()
		<-done
		return &tools.ToolResult{
			Output: output.String(),
			Error:  "execution timed out",
		}
	case err := <-done:
		if err != nil && err != io.EOF {
			return &tools.ToolResult{
				Output: output.String(),
				Error:  fmt.Sprintf("read output: %v", err),
			}
		}
	}

	// 检查退出码
	inspectResp, err := m.docker.ContainerExecInspect(execCtx, execResp.ID)
	if err == nil && inspectResp.ExitCode != 0 {
		result := output.String()
		// 截断过长输出
		if len(result) > 32000 {
			result = result[:32000] + "\n... [truncated]"
		}
		return &tools.ToolResult{
			Output: result,
			Error:  fmt.Sprintf("exit code %d", inspectResp.ExitCode),
		}
	}

	result := output.String()
	if len(result) > 32000 {
		result = result[:32000] + "\n... [truncated]"
	}
	return &tools.ToolResult{Output: result}
}

// StopProject 停止项目容器
func (m *Manager) StopProject(ctx context.Context, projectID string) {
	m.mu.Lock()
	info, ok := m.containers[projectID]
	if ok {
		delete(m.containers, projectID)
	}
	m.mu.Unlock()

	if !ok {
		return
	}

	timeout := 5
	stopOpts := container.StopOptions{Timeout: &timeout}

	switch m.cfg.CompletedAction {
	case "remove":
		m.docker.ContainerStop(ctx, info.ID, stopOpts)
		m.docker.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true})
		log.Printf("[container] removed project=%s container=%s", projectID, info.Name)
	default:
		m.docker.ContainerStop(ctx, info.ID, stopOpts)
		log.Printf("[container] stopped project=%s container=%s", projectID, info.Name)
	}
}

// StopAll 停止所有容器
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.Lock()
	projects := make([]string, 0, len(m.containers))
	for pid := range m.containers {
		projects = append(projects, pid)
	}
	m.mu.Unlock()

	for _, pid := range projects {
		m.StopProject(ctx, pid)
	}
}

// --- internal ---

func (m *Manager) containerName(projectID string) string {
	sanitized := strings.ReplaceAll(projectID, "/", "-")
	return containerPrefix + sanitized
}

// createContainer 创建容器 (Cairn-style: sleep infinity)
func (m *Manager) createContainer(ctx context.Context, projectID, name string) (*ContainerInfo, error) {
	var env []string
	if m.cfg.ProxyEnv != nil {
		for k, v := range m.cfg.ProxyEnv {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	// 注入 Server URL
	if m.serverURL != "" {
		env = append(env, fmt.Sprintf("PTAGENT_SERVER=%s", m.serverURL))
	}
	// 注入项目 ID
	env = append(env, fmt.Sprintf("PTAGENT_PROJECT_ID=%s", projectID))

	// 获取并注入 CTFd link 信息
	if ctfdLink, err := m.fetchCTFdLink(ctx, projectID); err == nil && ctfdLink != nil {
		env = append(env, fmt.Sprintf("PTAGENT_CTFD_INSTANCE_ID=%s", ctfdLink.CTFdInstanceID))
		env = append(env, fmt.Sprintf("PTAGENT_CTFD_CHALLENGE_ID=%d", ctfdLink.CTFdChallengeID))
		log.Printf("[container] CTFd link injected: instance=%s challenge=%d", ctfdLink.CTFdInstanceID, ctfdLink.CTFdChallengeID)
	}

	containerCfg := &container.Config{
		Image: m.cfg.Image,
		Env:   env,
		Cmd:   []string{"sleep", "infinity"},
	}

	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(m.cfg.NetworkMode),
		CapAdd:      m.cfg.CapAdd,
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}

	networkCfg := &network.NetworkingConfig{}

	resp, err := m.docker.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, name)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start container: %w", err)
	}

	info := &ContainerInfo{ID: resp.ID, Name: name, ProjectID: projectID}
	// 获取 CTFd link 信息并返回
	if ctfdLink, err := m.fetchCTFdLink(ctx, projectID); err == nil && ctfdLink != nil {
		info.CTFdInstanceID = ctfdLink.CTFdInstanceID
		info.CTFdChallengeID = ctfdLink.CTFdChallengeID
	}
	log.Printf("[container] created project=%s container=%s (sleep infinity)", projectID, name)
	return info, nil
}

func (m *Manager) isRunning(ctx context.Context, containerID string) bool {
	info, err := m.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return false
	}
	return info.State != nil && info.State.Running
}

func (m *Manager) findByName(ctx context.Context, name string) string {
	containers, err := m.docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return ""
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if n == "/"+name || n == name {
				return c.ID
			}
		}
	}
	return ""
}

// --- CTFd Challenge Instance Tools (called from Worker containers) ---

// getProjectCTFdInfo 获取项目关联的 CTFd 信息（实例ID、题目ID、连接信息）
func (m *Manager) getProjectCTFdInfo(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return &tools.ToolResult{Error: "project_id is required"}
	}

	if m.serverURL == "" {
		return &tools.ToolResult{Error: "PTAGENT_SERVER not configured"}
	}

	url := fmt.Sprintf("%s/api/projects/%s", m.serverURL, projectID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("create request: %v", err)}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &tools.ToolResult{Error: fmt.Sprintf("API returned status %d", resp.StatusCode)}
	}

	body, _ := io.ReadAll(resp.Body)

	// 提取 CTFd link 信息
	var result struct {
		Project struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"project"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("parse response: %v", err)}
	}

	// 查询 CTFd link
	linkURL := fmt.Sprintf("%s/api/projects/%s/ctfd-link", m.serverURL, projectID)
	linkReq, _ := http.NewRequestWithContext(ctx, "GET", linkURL, nil)
	linkResp, err := m.httpClient.Do(linkReq)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("link request failed: %v", err)}
	}
	defer linkResp.Body.Close()

	// 如果没有 link，返回项目基本信息
	if linkResp.StatusCode != http.StatusOK {
		return &tools.ToolResult{Output: fmt.Sprintf(`{"project_id": %q, "title": %q, "ctfd_linked": false}`, result.Project.ID, result.Project.Title)}
	}

	var link struct {
		ProjectID       string `json:"project_id"`
		CTFdInstanceID  string `json:"ctfd_instance_id"`
		CTFdChallengeID int    `json:"ctfd_challenge_id"`
		AutoSubmit      bool   `json:"auto_submit"`
	}
	io.ReadAll(linkResp.Body)
	json.Unmarshal(body, &link) // ignore error

	return &tools.ToolResult{Output: fmt.Sprintf(`{"project_id": %q, "title": %q, "ctfd_linked": true, "ctfd_instance_id": %q, "ctfd_challenge_id": %d, "auto_submit": %v}`,
		result.Project.ID, result.Project.Title, link.CTFdInstanceID, link.CTFdChallengeID, link.AutoSubmit)}
}

// getChallengeInstanceStatus 获取靶机实例状态
func (m *Manager) getChallengeInstanceStatus(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	instanceID, challengeID, err := m.resolveCTFdInfo(ctx, info, args)
	if err != nil {
		return &tools.ToolResult{Error: err.Error()}
	}
	if m.serverURL == "" {
		return &tools.ToolResult{Error: "PTAGENT_SERVER not configured"}
	}

	url := fmt.Sprintf("%s/api/ctfd/instances/%s/challenges/%d/instance",
		m.serverURL, instanceID, challengeID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("create request: %v", err)}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &tools.ToolResult{Output: string(body)}
}

// startChallengeInstance 启动靶机实例
func (m *Manager) startChallengeInstance(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	instanceID, challengeID, err := m.resolveCTFdInfo(ctx, info, args)
	if err != nil {
		return &tools.ToolResult{Error: err.Error()}
	}
	if m.serverURL == "" {
		return &tools.ToolResult{Error: "PTAGENT_SERVER not configured"}
	}

	url := fmt.Sprintf("%s/api/ctfd/instances/%s/challenges/%d/instance/start",
		m.serverURL, instanceID, challengeID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("create request: %v", err)}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &tools.ToolResult{Output: string(body)}
}

// stopChallengeInstance 停止靶机实例
func (m *Manager) stopChallengeInstance(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	instanceID, challengeID, err := m.resolveCTFdInfo(ctx, info, args)
	if err != nil {
		return &tools.ToolResult{Error: err.Error()}
	}
	if m.serverURL == "" {
		return &tools.ToolResult{Error: "PTAGENT_SERVER not configured"}
	}

	url := fmt.Sprintf("%s/api/ctfd/instances/%s/challenges/%d/instance/stop",
		m.serverURL, instanceID, challengeID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("create request: %v", err)}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &tools.ToolResult{Output: string(body)}
}

// renewChallengeInstance 续期靶机实例
func (m *Manager) renewChallengeInstance(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	instanceID, challengeID, err := m.resolveCTFdInfo(ctx, info, args)
	if err != nil {
		return &tools.ToolResult{Error: err.Error()}
	}
	if m.serverURL == "" {
		return &tools.ToolResult{Error: "PTAGENT_SERVER not configured"}
	}

	url := fmt.Sprintf("%s/api/ctfd/instances/%s/challenges/%d/instance/renew",
		m.serverURL, instanceID, challengeID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("create request: %v", err)}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &tools.ToolResult{Output: string(body)}
}

// submitCTFdFlag 提交 flag 到 CTFd
func (m *Manager) submitCTFdFlag(ctx context.Context, info *ContainerInfo, args map[string]interface{}) *tools.ToolResult {
	flag, _ := args["flag"].(string)
	if flag == "" {
		return &tools.ToolResult{Error: "flag is required"}
	}

	instanceID, challengeID, err := m.resolveCTFdInfo(ctx, info, args)
	if err != nil {
		return &tools.ToolResult{Error: err.Error()}
	}
	if m.serverURL == "" {
		return &tools.ToolResult{Error: "PTAGENT_SERVER not configured"}
	}

	url := fmt.Sprintf("%s/api/ctfd/instances/%s/challenges/%d/submit",
		m.serverURL, instanceID, challengeID)

	payload := map[string]string{"flag": flag}
	payloadBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("create request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return &tools.ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &tools.ToolResult{Output: string(body)}
}
