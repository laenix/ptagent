package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/tools"
	"github.com/ptagent/ptagent/internal/worker"
)

const maxToolRounds = 50 // 最多工具调用轮次

// ToolExecutorFunc 工具执行函数签名（支持本地和容器两种模式）
type ToolExecutorFunc func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult

// OpenAIDriver OpenAI 兼容的 Worker 驱动
type OpenAIDriver struct {
	name          string
	baseURL       string
	apiKey        string
	model         string
	llmClient     *http.Client
	toolExecutor  *tools.Executor  // 本地工具执行器（fallback）
	containerExec ToolExecutorFunc // 容器内工具执行器（优先）
}

// NewOpenAIDriver 创建 OpenAI 驱动
func NewOpenAIDriver(name string, env map[string]string, proxyCfg *config.ProxyConfig) *OpenAIDriver {
	var llmClient *http.Client
	var proxyURL string
	if proxyCfg != nil {
		llmClient = proxyCfg.BuildHTTPClient(true, 10*time.Minute)
		// 靶场流量走代理
		if proxyCfg.HTTPSProxy != "" {
			proxyURL = proxyCfg.HTTPSProxy
		} else if proxyCfg.HTTPProxy != "" {
			proxyURL = proxyCfg.HTTPProxy
		}
	} else {
		llmClient = &http.Client{Timeout: 10 * time.Minute}
	}

	return &OpenAIDriver{
		name:         name,
		baseURL:      env["OPENAI_BASE_URL"],
		apiKey:       env["OPENAI_API_KEY"],
		model:        env["OPENAI_MODEL"],
		llmClient:    llmClient,
		toolExecutor: tools.NewExecutor(proxyURL),
	}
}

// SetContainerExecutor 设置容器内工具执行器
func (d *OpenAIDriver) SetContainerExecutor(exec ToolExecutorFunc) {
	d.containerExec = exec
}

func (d *OpenAIDriver) Name() string { return d.name }

// chatURL 构建 chat completions 端点 URL
func (d *OpenAIDriver) chatURL() string {
	base := d.baseURL
	if len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v3") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

func (d *OpenAIDriver) Healthcheck(ctx context.Context) error {
	reqBody := map[string]interface{}{
		"model":      d.model,
		"max_tokens": 10,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "ping"},
		},
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", d.chatURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	resp, err := d.llmClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned status %d", resp.StatusCode)
	}
	return nil
}

// chatMessage 表示一条消息
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatResponse 聊天响应
type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

func (d *OpenAIDriver) Execute(ctx context.Context, task *worker.Task) (*worker.TaskResult, error) {
	// 构建初始消息
	messages := []chatMessage{
		{Role: "system", Content: "You are an autonomous penetration testing agent. You have access to tools to interact with target systems. Use tools to gather information and exploit vulnerabilities. IMPORTANT: After using tools for exploration, you MUST return a JSON result with your findings. If you found a flag, include it in the result. If you cannot solve it, return what you found with description=\"partial findings\" field. NEVER call tools indefinitely - you have limited rounds. Return JSON: {\"description\": \"your findings\"}"},
		{Role: "user", Content: task.Prompt},
	}

	// 判断是否启用工具（只有 explore 类型的任务启用工具）
	useTools := (task.Type == worker.TaskExplore)

	// 多轮工具调用循环
	for round := 0; round < maxToolRounds; round++ {
		resp, err := d.chat(ctx, messages, useTools)
		if err != nil {
			return nil, err
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("no choices in response")
		}

		choice := resp.Choices[0]
		msg := choice.Message

		// 检查是否有 tool_calls
		if len(msg.ToolCalls) > 0 && useTools {
			// 将 assistant 消息加入历史
			messages = append(messages, msg)

			// 执行每个工具调用
			for _, tc := range msg.ToolCalls {
				log.Printf("[%s] tool call: %s(%s)", d.name, tc.Function.Name, truncateStr(tc.Function.Arguments, 200))

				var args map[string]interface{}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)

				var result *tools.ToolResult
				if d.containerExec != nil {
					// 通过容器内 tool-agent 执行
					result = d.containerExec(ctx, tc.Function.Name, args)
				} else {
					// 本地执行（fallback）
					result = d.toolExecutor.Execute(ctx, tc.Function.Name, args)
				}

				var output string
				if result.Error != "" {
					output = "ERROR: " + result.Error
				} else {
					output = result.Output
					// 截断过长的输出
					if len(output) > 16000 {
						output = output[:16000] + "\n... [truncated]"
					}
				}

				log.Printf("[%s] tool result: %s", d.name, truncateStr(output, 500))

				// 添加工具结果消息
				messages = append(messages, chatMessage{
					Role:       "tool",
					Content:    output,
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				})
			}
			continue // 继续下一轮
		}

		// 没有 tool_calls，这是最终回答
		content := msg.Content
		content = stripThinkBlock(content)
		content = extractJSONFromMarkdown(content)

		var result worker.TaskResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("parse task result: %w (content: %s)", err, truncateStr(content, 500))
		}
		return &result, nil
	}

	// 达到最大轮次后，强制让 AI 返回当前发现
	log.Printf("[%s] forcing conclusion after %d rounds", d.name, maxToolRounds)
	messages = append(messages, chatMessage{
		Role:    "user",
		Content: "You have reached the maximum number of tool call rounds. You MUST now return a JSON result with your current findings, even if incomplete. Return format: {\"accepted\": true, \"data\": {\"description\": \"what you found so far\"}}",
	})

	resp, err := d.chat(ctx, messages, false) // 强制不使用工具，直接返回结论
	if err != nil {
		return nil, fmt.Errorf("conclusion failed: %w", err)
	}

	if len(resp.Choices) > 0 {
		content := resp.Choices[0].Message.Content
		content = stripThinkBlock(content)
		content = extractJSONFromMarkdown(content)

		var result worker.TaskResult
		if err := json.Unmarshal([]byte(content), &result); err == nil {
			return &result, nil
		}
	}

	return nil, fmt.Errorf("exceeded max tool call rounds (%d)", maxToolRounds)
}

// chat 发起一次 API 调用
func (d *OpenAIDriver) chat(ctx context.Context, messages []chatMessage, useTools bool) (*chatResponse, error) {
	reqBody := map[string]interface{}{
		"model":       d.model,
		"messages":    messages,
		"temperature": 0.1,
		"max_tokens":  16384,
	}

	if useTools {
		reqBody["tools"] = tools.AvailableTools()
		reqBody["tool_choice"] = "auto"
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", d.chatURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	resp, err := d.llmClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, truncateStr(string(respBody), 500))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &chatResp, nil
}

func (d *OpenAIDriver) Conclude(ctx context.Context, task *worker.Task, sessionID string) (*worker.TaskResult, error) {
	return d.Execute(ctx, task)
}

func (d *OpenAIDriver) SupportsConclude() bool {
	return true
}

// --- helpers ---

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// stripThinkBlock 移除 <think>...</think> 块
func stripThinkBlock(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "<think>") {
		end := strings.Index(s, "</think>")
		if end == -1 {
			if idx := strings.Index(s, "{"); idx >= 0 {
				s = s[idx:]
			}
			break
		}
		s = strings.TrimSpace(s[end+len("</think>"):])
	}
	return s
}

// extractJSONFromMarkdown 提取 JSON
func extractJSONFromMarkdown(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		return strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		start := strings.Index(s, "{")
		if start < 0 {
			return s
		}
		s = s[start:]
	}
	if strings.HasPrefix(s, "{") {
		if end := findMatchingBrace(s); end >= 0 {
			return s[:end+1]
		}
	}
	return s
}

func findMatchingBrace(s string) int {
	depth := 0
	inString := false
	escaped := false
	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
