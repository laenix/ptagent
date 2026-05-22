package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ptagent/ptagent/internal/ctfd"
	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/store"
)

var flagPattern = regexp.MustCompile(`(?i)(?:flag|ctf|htb|picoCTF|DUCTF)\{[^\}]{1,200}\}`)

// PlatformAgent 平台级 AI Agent，衔接 PTAgent 和 CTFd
type PlatformAgent struct {
	store     store.Store
	llmURL    string
	llmAPIKey string
	llmModel  string
	client    *http.Client
}

// Config Agent 配置
type Config struct {
	LLMBaseURL string `json:"llm_base_url" yaml:"llm_base_url"`
	LLMAPIKey  string `json:"llm_api_key" yaml:"llm_api_key"`
	LLMModel   string `json:"llm_model" yaml:"llm_model"`
}

// New 创建平台 Agent
func New(s store.Store, cfg *Config) *PlatformAgent {
	baseURL := cfg.LLMBaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}

	return &PlatformAgent{
		store:     s,
		llmURL:    baseURL + "/chat/completions",
		llmAPIKey: cfg.LLMAPIKey,
		llmModel:  cfg.LLMModel,
		client:    &http.Client{Timeout: 2 * time.Minute},
	}
}

// --- Auto Flag Submission ---

// TryAutoSubmitFlag 检查 explore 结果中是否包含 flag，并尝试自动提交
// 返回提交结果描述（空字符串表示未提交）
func (a *PlatformAgent) TryAutoSubmitFlag(ctx context.Context, projectID, description string) string {
	flags := flagPattern.FindAllString(description, -1)
	if len(flags) == 0 {
		return ""
	}

	// 查找项目关联的 CTFd 题目
	link, err := a.store.GetProjectCTFdLink(ctx, projectID)
	if err != nil {
		return "" // 未关联 CTFd
	}
	if !link.AutoSubmit {
		log.Printf("[agent] flag found in project %s but auto_submit disabled, flags: %v", projectID, flags)
		return fmt.Sprintf("[AUTO-SUBMIT BLOCKED] Found flag(s) but auto-submit is disabled (challenge has attempt limits). Flags: %s", strings.Join(flags, ", "))
	}

	// 获取 CTFd 实例
	inst, err := a.store.GetCTFdInstance(ctx, link.CTFdInstanceID)
	if err != nil {
		log.Printf("[agent] get ctfd instance failed: %v", err)
		return ""
	}

	client := ctfd.NewClient(inst.URL, inst.Token)

	// 检查题目是否已解决
	ch, err := client.GetChallenge(ctx, link.CTFdChallengeID)
	if err != nil {
		log.Printf("[agent] get challenge failed: %v", err)
		return ""
	}
	if ch.Solved {
		return "[AUTO-SUBMIT SKIP] Challenge already solved"
	}
	if ch.MaxAttempts > 0 && ch.Attempts >= ch.MaxAttempts {
		return "[AUTO-SUBMIT SKIP] No attempts remaining"
	}

	// 尝试每个匹配到的 flag
	var results []string
	for _, flag := range flags {
		result, err := client.SubmitFlag(ctx, link.CTFdChallengeID, flag)
		if err != nil {
			results = append(results, fmt.Sprintf("%s → error: %v", flag, err))
			continue
		}
		results = append(results, fmt.Sprintf("%s → %s: %s", flag, result.Status, result.Message))
		log.Printf("[agent] auto-submit flag for project %s: %s → %s", projectID, flag, result.Status)
		if result.Status == "correct" {
			return fmt.Sprintf("[AUTO-SUBMIT SUCCESS] %s", strings.Join(results, "; "))
		}
	}

	return fmt.Sprintf("[AUTO-SUBMIT ATTEMPTED] %s", strings.Join(results, "; "))
}

// --- Chat Agent ---

// Chat 处理用户的自然语言对话
func (a *PlatformAgent) Chat(ctx context.Context, message string) (*models.AgentChatResponse, error) {
	if a.llmAPIKey == "" {
		return a.fallbackChat(ctx, message)
	}

	systemPrompt := `You are PTAgent Platform Assistant, an AI that helps manage CTF (Capture The Flag) competitions.
You have access to functions to interact with the PTAgent platform and CTFd instances.

Capabilities:
- List/query projects and their status (facts, intents, progress)
- Stop, reopen projects
- List CTFd instances and challenges
- Submit flags to CTFd challenges
- Check challenge solve status and attempt limits
- Toggle auto-submit for projects
- Provide summaries and analysis

Always be concise. When the user asks about "which challenges are solved" or project status, call the appropriate function first, then summarize the results.
When asked to submit a flag, always check attempt limits first. If max_attempts > 0, warn the user before submitting.
Respond in the same language as the user (Chinese or English).`

	// 初始消息
	messages := []chatMsg{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: message},
	}

	var actions []models.AgentAction

	// 多轮 function calling
	for round := 0; round < 10; round++ {
		resp, err := a.llmChat(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("LLM error: %w", err)
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("no response from LLM")
		}

		choice := resp.Choices[0]

		if len(choice.Message.ToolCalls) > 0 {
			messages = append(messages, choice.Message)

			for _, tc := range choice.Message.ToolCalls {
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					messages = append(messages, chatMsg{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    fmt.Sprintf("ERROR: invalid arguments JSON: %v", err),
					})
					continue
				}

				result, action := a.executeFunction(ctx, tc.Function.Name, args)
				if action != nil {
					actions = append(actions, *action)
				}

				messages = append(messages, chatMsg{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result,
				})
			}
			continue
		}

		// 最终回答
		return &models.AgentChatResponse{
			Reply:   choice.Message.Content,
			Actions: actions,
		}, nil
	}

	return &models.AgentChatResponse{
		Reply:   "I've reached the maximum number of function calls. Please try a more specific request.",
		Actions: actions,
	}, nil
}

// --- LLM Communication ---

type chatMsg struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
}

func (a *PlatformAgent) llmChat(ctx context.Context, messages []chatMsg) (*chatResp, error) {
	reqBody := map[string]interface{}{
		"model":       a.llmModel,
		"messages":    messages,
		"tools":       agentTools(),
		"tool_choice": "auto",
		"temperature": 0.1,
		"max_tokens":  4096,
	}

	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", a.llmURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.llmAPIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API status %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 500)]))
	}

	var result chatResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}
	return &result, nil
}

// --- Function Execution ---

func (a *PlatformAgent) executeFunction(ctx context.Context, name string, args map[string]interface{}) (string, *models.AgentAction) {
	switch name {
	case "list_projects":
		return a.fnListProjects(ctx, args)
	case "get_project":
		return a.fnGetProject(ctx, args)
	case "stop_project":
		return a.fnStopProject(ctx, args)
	case "reopen_project":
		return a.fnReopenProject(ctx, args)
	case "list_ctfd_challenges":
		return a.fnListCTFdChallenges(ctx, args)
	case "submit_flag":
		return a.fnSubmitFlag(ctx, args)
	case "set_auto_submit":
		return a.fnSetAutoSubmit(ctx, args)
	case "get_project_ctfd_link":
		return a.fnGetProjectCTFdLink(ctx, args)
	case "import_all_challenges":
		return a.fnImportAllChallenges(ctx, args)
	default:
		return fmt.Sprintf("unknown function: %s", name), nil
	}
}

func (a *PlatformAgent) fnListProjects(ctx context.Context, _ map[string]interface{}) (string, *models.AgentAction) {
	projects, err := a.store.ListProjects(ctx)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	var lines []string
	for _, p := range projects {
		line := fmt.Sprintf("- %s: \"%s\" [%s] facts=%d intents=%d(open=%d)",
			p.ID, p.Title, p.Status, p.FactCount, p.IntentCount, p.UnclaimedIntentCount+p.WorkingIntentCount)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "No projects found.", nil
	}
	return strings.Join(lines, "\n"), nil
}

func (a *PlatformAgent) fnGetProject(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return "Error: project_id is required", nil
	}

	detail, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project: %s \"%s\" [%s]\n", detail.Project.ID, detail.Project.Title, detail.Project.Status))
	sb.WriteString(fmt.Sprintf("Facts: %d, Intents: %d, Hints: %d\n", len(detail.Facts), len(detail.Intents), len(detail.Hints)))

	// 列出关键 facts
	sb.WriteString("\nFacts:\n")
	for _, f := range detail.Facts {
		desc := f.Description
		if len(desc) > 150 {
			desc = desc[:150] + "..."
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", f.ID, desc))
	}

	// 列出 open intents
	sb.WriteString("\nOpen Intents:\n")
	openCount := 0
	for _, i := range detail.Intents {
		if i.IsOpen() {
			worker := "unclaimed"
			if i.IsClaimed() {
				worker = *i.Worker
			}
			sb.WriteString(fmt.Sprintf("  [%s] %s (worker: %s)\n", i.ID, i.Description, worker))
			openCount++
		}
	}
	if openCount == 0 {
		sb.WriteString("  (none)\n")
	}

	// CTFd link
	link, err := a.store.GetProjectCTFdLink(ctx, projectID)
	if err == nil {
		sb.WriteString(fmt.Sprintf("\nCTFd Link: instance=%s challenge=%d auto_submit=%v\n", link.CTFdInstanceID, link.CTFdChallengeID, link.AutoSubmit))
	}

	return sb.String(), nil
}

func (a *PlatformAgent) fnStopProject(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return "Error: project_id is required", nil
	}

	_, err := a.store.UpdateProjectStatus(ctx, projectID, models.ProjectStatusStopped)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	action := &models.AgentAction{Type: "stop_project", Detail: projectID, Result: "stopped"}
	return fmt.Sprintf("Project %s has been stopped.", projectID), action
}

func (a *PlatformAgent) fnReopenProject(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return "Error: project_id is required", nil
	}

	_, err := a.store.UpdateProjectStatus(ctx, projectID, models.ProjectStatusActive)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	action := &models.AgentAction{Type: "reopen_project", Detail: projectID, Result: "active"}
	return fmt.Sprintf("Project %s has been reopened.", projectID), action
}

func (a *PlatformAgent) fnListCTFdChallenges(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	instanceID, _ := args["instance_id"].(string)
	if instanceID == "" {
		// 自动选第一个 instance
		instances, err := a.store.ListCTFdInstances(ctx)
		if err != nil || len(instances) == 0 {
			return "No CTFd instances configured.", nil
		}
		instanceID = instances[0].ID
	}

	inst, err := a.store.GetCTFdInstance(ctx, instanceID)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	client := ctfd.NewClient(inst.URL, inst.Token)
	challenges, err := client.ListChallenges(ctx)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	var lines []string
	solved := 0
	total := 0
	for _, ch := range challenges {
		status := "❌"
		if ch.Solved {
			status = "✅"
			solved++
		}
		total++
		attempts := ""
		if ch.MaxAttempts > 0 {
			attempts = fmt.Sprintf(" [%d/%d attempts]", ch.Attempts, ch.MaxAttempts)
		}
		lines = append(lines, fmt.Sprintf("%s #%d %s (%s, %dpts, %d solves)%s",
			status, ch.ID, ch.Name, ch.Category, ch.Value, ch.Solves, attempts))
	}

	header := fmt.Sprintf("CTFd: %s — %d/%d solved\n", inst.Name, solved, total)
	return header + strings.Join(lines, "\n"), nil
}

func (a *PlatformAgent) fnSubmitFlag(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	flag, _ := args["flag"].(string)
	if flag == "" {
		return "Error: flag is required", nil
	}

	// 获取目标 — 可以通过 project_id 或 instance_id+challenge_id
	var instID string
	var challID int

	if projectID, ok := args["project_id"].(string); ok && projectID != "" {
		link, err := a.store.GetProjectCTFdLink(ctx, projectID)
		if err != nil {
			return "Error: project not linked to CTFd challenge", nil
		}
		instID = link.CTFdInstanceID
		challID = link.CTFdChallengeID
	} else {
		instID, _ = args["instance_id"].(string)
		if cid, ok := args["challenge_id"].(float64); ok {
			challID = int(cid)
		}
	}

	if instID == "" || challID == 0 {
		return "Error: need project_id or (instance_id + challenge_id) to submit flag", nil
	}

	inst, err := a.store.GetCTFdInstance(ctx, instID)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	client := ctfd.NewClient(inst.URL, inst.Token)

	// 先检查状态
	ch, err := client.GetChallenge(ctx, challID)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	if ch.Solved {
		return fmt.Sprintf("Challenge #%d \"%s\" is already solved.", challID, ch.Name), nil
	}
	if ch.MaxAttempts > 0 && ch.Attempts >= ch.MaxAttempts {
		return fmt.Sprintf("Challenge #%d \"%s\" has no remaining attempts (%d/%d).", challID, ch.Name, ch.Attempts, ch.MaxAttempts), nil
	}

	result, err := client.SubmitFlag(ctx, challID, flag)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	action := &models.AgentAction{
		Type:   "submit_flag",
		Detail: fmt.Sprintf("challenge #%d, flag: %s", challID, flag),
		Result: result.Status + ": " + result.Message,
	}
	return fmt.Sprintf("Submit result for challenge #%d \"%s\": %s — %s", challID, ch.Name, result.Status, result.Message), action
}

func (a *PlatformAgent) fnSetAutoSubmit(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return "Error: project_id is required", nil
	}
	enabled, _ := args["enabled"].(bool)

	if err := a.store.SetProjectAutoSubmit(ctx, projectID, enabled); err != nil {
		return "Error: " + err.Error(), nil
	}

	action := &models.AgentAction{Type: "set_auto_submit", Detail: projectID, Result: fmt.Sprintf("%v", enabled)}
	return fmt.Sprintf("Auto-submit for project %s set to %v.", projectID, enabled), action
}

func (a *PlatformAgent) fnGetProjectCTFdLink(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return "Error: project_id is required", nil
	}

	link, err := a.store.GetProjectCTFdLink(ctx, projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Sprintf("Project %s is not linked to any CTFd challenge.", projectID), nil
		}
		return "Error: " + err.Error(), nil
	}

	return fmt.Sprintf("Project %s → CTFd instance %s, challenge #%d, auto_submit=%v",
		link.ProjectID, link.CTFdInstanceID, link.CTFdChallengeID, link.AutoSubmit), nil
}

func (a *PlatformAgent) fnImportAllChallenges(ctx context.Context, args map[string]interface{}) (string, *models.AgentAction) {
	instanceID, _ := args["instance_id"].(string)
	if instanceID == "" {
		// 自动选第一个 instance
		instances, err := a.store.ListCTFdInstances(ctx)
		if err != nil || len(instances) == 0 {
			return "No CTFd instances configured.", nil
		}
		instanceID = instances[0].ID
	}

	inst, err := a.store.GetCTFdInstance(ctx, instanceID)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	client := ctfd.NewClient(inst.URL, inst.Token)
	challenges, err := client.ListChallenges(ctx)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	var successCount, skipCount, failCount int
	var results []string

	for _, ch := range challenges {
		if ch.Solved {
			skipCount++
			results = append(results, fmt.Sprintf("  ⏭ #%d %s (already solved)", ch.ID, ch.Name))
			continue
		}

		// 幂等导入：已导入的 challenge 跳过
		if existing, err := a.store.GetCTFdProjectLinkByChallenge(ctx, instanceID, ch.ID); err == nil {
			skipCount++
			results = append(results, fmt.Sprintf("  ⏭ #%d %s (already imported as %s)", ch.ID, ch.Name, existing.ProjectID))
			continue
		} else if err != nil && err != sql.ErrNoRows {
			failCount++
			results = append(results, fmt.Sprintf("  ❌ #%d %s (lookup failed: %v)", ch.ID, ch.Name, err))
			continue
		}

		// 构造 origin
		origin := fmt.Sprintf("[CTFd] %s (Category: %s, Value: %d pts)\n\n%s", ch.Name, ch.Category, ch.Value, ch.Description)
		if ch.ConnectionInfo != "" {
			origin += "\n\nConnection Info: " + ch.ConnectionInfo
		}

		// 构造 hints
		var hints []models.CreateHintParams
		if len(ch.Files) > 0 {
			fileList := "附件下载链接:\n"
			for _, f := range ch.Files {
				proxyURL := fmt.Sprintf("/api/ctfd/instances/%s/files/%s", instanceID, f.Location)
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

		project, err := a.store.CreateProject(ctx, req)
		if err != nil {
			failCount++
			results = append(results, fmt.Sprintf("  ❌ #%d %s (create failed: %v)", ch.ID, ch.Name, err))
			continue
		}

		// 建立关联
		autoSubmit := ch.MaxAttempts == 0
		if err := a.store.LinkProjectCTFd(ctx, &models.CTFdProjectLink{
			ProjectID:       project.ID,
			CTFdInstanceID:  instanceID,
			CTFdChallengeID: ch.ID,
			AutoSubmit:      autoSubmit,
		}); err != nil {
			results = append(results, fmt.Sprintf("  ⚠️ #%d %s (project created but link failed: %v)", ch.ID, ch.Name, err))
		} else {
			results = append(results, fmt.Sprintf("  ✅ #%d %s → %s", ch.ID, ch.Name, project.ID))
		}
		successCount++
	}

	summary := fmt.Sprintf("Import completed: %d created, %d skipped (solved), %d failed\n\n%s",
		successCount, skipCount, failCount, strings.Join(results, "\n"))
	action := &models.AgentAction{
		Type:   "import_all_challenges",
		Detail: fmt.Sprintf("instance=%s total=%d created=%d skipped=%d failed=%d", instanceID, len(challenges), successCount, skipCount, failCount),
		Result: "completed",
	}
	return summary, action
}

// fallbackChat 无 LLM 时的简单关键词匹配
func (a *PlatformAgent) fallbackChat(ctx context.Context, message string) (*models.AgentChatResponse, error) {
	msg := strings.ToLower(message)
	switch {
	case strings.Contains(msg, "project") && (strings.Contains(msg, "list") || strings.Contains(msg, "列表") || strings.Contains(msg, "有哪些")):
		result, _ := a.fnListProjects(ctx, nil)
		return &models.AgentChatResponse{Reply: result}, nil
	case strings.Contains(msg, "challenge") || strings.Contains(msg, "题目"):
		result, _ := a.fnListCTFdChallenges(ctx, nil)
		return &models.AgentChatResponse{Reply: result}, nil
	default:
		return &models.AgentChatResponse{
			Reply: "Platform Agent is running without LLM. Supported commands: 'list projects', 'list challenges'. Configure LLM for natural language interaction.",
		}, nil
	}
}

// --- Tool Definitions ---

func agentTools() []map[string]interface{} {
	return []map[string]interface{}{
		fnDef("list_projects", "List all PTAgent projects with status summary.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{},
		}),
		fnDef("get_project", "Get detailed project info including facts, intents, and CTFd link.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project ID, e.g. proj_001"},
			},
			"required": []string{"project_id"},
		}),
		fnDef("stop_project", "Stop a project (pause all dispatching).", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project ID"},
			},
			"required": []string{"project_id"},
		}),
		fnDef("reopen_project", "Reopen a stopped or completed project.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project ID"},
			},
			"required": []string{"project_id"},
		}),
		fnDef("list_ctfd_challenges", "List all challenges from a CTFd instance with solve status.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{"type": "string", "description": "CTFd instance ID. If empty, uses the first available instance."},
			},
		}),
		fnDef("submit_flag", "Submit a flag to a CTFd challenge. Specify either project_id or (instance_id + challenge_id).", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"flag":         map[string]interface{}{"type": "string", "description": "The flag to submit"},
				"project_id":   map[string]interface{}{"type": "string", "description": "Project ID (will use linked CTFd challenge)"},
				"instance_id":  map[string]interface{}{"type": "string", "description": "CTFd instance ID"},
				"challenge_id": map[string]interface{}{"type": "number", "description": "CTFd challenge ID"},
			},
			"required": []string{"flag"},
		}),
		fnDef("set_auto_submit", "Enable or disable auto flag submission for a project.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project ID"},
				"enabled":    map[string]interface{}{"type": "boolean", "description": "true to enable auto-submit, false to disable"},
			},
			"required": []string{"project_id", "enabled"},
		}),
		fnDef("get_project_ctfd_link", "Check if a project is linked to a CTFd challenge.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project ID"},
			},
			"required": []string{"project_id"},
		}),
		fnDef("import_all_challenges", "Import all unsolved challenges from a CTFd instance as PTAgent projects. This creates a project for each unsolved challenge and links them to CTFd.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{"type": "string", "description": "CTFd instance ID. If empty, uses the first available instance."},
			},
		}),
	}
}

func fnDef(name, description string, params map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        name,
			"description": description,
			"parameters":  params,
		},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
