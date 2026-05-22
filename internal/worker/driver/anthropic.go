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
	"sync"
	"time"

	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/tools"
	"github.com/ptagent/ptagent/internal/worker"
)

// AnthropicDriver Anthropic Messages API 兼容的 Worker 驱动
type AnthropicDriver struct {
	name         string
	baseURL      string
	apiKey       string
	model        string
	llmClient    *http.Client
	toolExecutor *tools.Executor // 本地工具执行器（fallback）
}

// NewAnthropicDriver 创建 Anthropic 驱动
func NewAnthropicDriver(name string, env map[string]string, proxyCfg *config.ProxyConfig) *AnthropicDriver {
	var llmClient *http.Client
	var proxyURL string
	if proxyCfg != nil {
		llmClient = proxyCfg.BuildHTTPClient(true, 10*time.Minute)
		if proxyCfg.HTTPSProxy != "" {
			proxyURL = proxyCfg.HTTPSProxy
		} else if proxyCfg.HTTPProxy != "" {
			proxyURL = proxyCfg.HTTPProxy
		}
	} else {
		llmClient = &http.Client{Timeout: 10 * time.Minute}
	}

	baseURL := env["ANTHROPIC_BASE_URL"]
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	return &AnthropicDriver{
		name:         name,
		baseURL:      baseURL,
		apiKey:       env["ANTHROPIC_API_KEY"],
		model:        env["ANTHROPIC_MODEL"],
		llmClient:    llmClient,
		toolExecutor: tools.NewExecutor(proxyURL),
	}
}

func (d *AnthropicDriver) Name() string { return d.name }

// messagesURL 构建 Messages API 端点 URL
func (d *AnthropicDriver) messagesURL() string {
	base := strings.TrimRight(d.baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// --- Anthropic Messages API 数据结构 ---

// anthContentBlock 消息内容块（text / tool_use / tool_result）
type anthContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`    // tool_use 的 ID
	Name  string          `json:"name,omitempty"`  // tool_use 的工具名
	Input json.RawMessage `json:"input,omitempty"` // tool_use 的参数

	// tool_result 字段
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"` // tool_result 的文本内容
	IsError   bool   `json:"is_error,omitempty"`
}

// anthMessage 消息
type anthMessage struct {
	Role    string          `json:"role"` // user / assistant
	Content json.RawMessage `json:"content"`
}

// anthResponse Messages API 响应
type anthResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthContentBlock `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// anthTool Anthropic 工具定义格式
type anthTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// --- 核心方法 ---

func (d *AnthropicDriver) Healthcheck(ctx context.Context) error {
	userContent, _ := json.Marshal([]anthContentBlock{{Type: "text", Text: "ping"}})
	messages := []anthMessage{{Role: "user", Content: userContent}}

	reqBody := map[string]interface{}{
		"model":      d.model,
		"max_tokens": 10,
		"messages":   messages,
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", d.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	d.setHeaders(req)

	resp, err := d.llmClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("healthcheck returned status %d: %s", resp.StatusCode, truncateStr(string(respBody), 300))
	}
	return nil
}

func (d *AnthropicDriver) Execute(ctx context.Context, task *worker.Task) (*worker.TaskResult, error) {
	systemPrompt := "You are an autonomous penetration testing agent. You have access to tools to interact with target systems. Use tools to gather information and exploit vulnerabilities. IMPORTANT: After using tools for exploration, you MUST return a JSON result with your findings. If you found a flag, include it in the result. If you cannot solve it, return what you found with description=\"partial findings\" field. NEVER call tools indefinitely - you have limited rounds. Return JSON: {\"description\": \"your findings\"}"

	// 构建初始消息
	userContent, _ := json.Marshal([]anthContentBlock{{Type: "text", Text: task.Prompt}})
	messages := []anthMessage{
		{Role: "user", Content: userContent},
	}

	useTools := (task.Type == worker.TaskExplore)

	// 去重检测
	var lastToolSig string
	dupCount := 0
	const maxDupRounds = 2

	for round := 0; round < maxToolRounds; round++ {
		resp, err := d.chat(ctx, systemPrompt, messages, useTools)
		if err != nil {
			return nil, err
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("Anthropic API error: [%s] %s", resp.Error.Type, resp.Error.Message)
		}

		// 分离文本和工具调用
		var textParts []string
		var toolUses []anthContentBlock
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, block.Text)
			case "tool_use":
				toolUses = append(toolUses, block)
			}
		}

		if len(toolUses) > 0 && useTools {
			// 将 assistant 消息加入历史
			assistantContent, _ := json.Marshal(resp.Content)
			messages = append(messages, anthMessage{Role: "assistant", Content: assistantContent})

			// 去重检测
			sig := d.toolUseSig(toolUses)
			if sig == lastToolSig {
				dupCount++
				if dupCount >= maxDupRounds {
					log.Printf("[%s] detected %d duplicate tool call rounds, forcing conclusion", d.name, dupCount)
					forceContent, _ := json.Marshal([]anthContentBlock{{Type: "text", Text: "You are repeating the same tool calls. Stop calling tools and return your findings now as JSON: {\"accepted\": true, \"data\": {\"description\": \"your findings\"}}"}})
					messages = append(messages, anthMessage{Role: "user", Content: forceContent})

					resp2, err := d.chat(ctx, systemPrompt, messages, false)
					if err == nil {
						content := d.extractText(resp2.Content)
						content = stripThinkBlock(content)
						content = extractJSONFromMarkdown(content)
						var result worker.TaskResult
						if err := json.Unmarshal([]byte(content), &result); err == nil {
							return &result, nil
						}
					}
					return nil, fmt.Errorf("stuck in duplicate tool calls after %d rounds", round+1)
				}
			} else {
				dupCount = 0
			}
			lastToolSig = sig

			// 执行工具调用（并行）
			toolResults := d.executeToolUses(ctx, toolUses)

			// 将工具结果作为 user 消息添加（Anthropic 格式）
			messages = append(messages, anthMessage{Role: "user", Content: toolResults})
			continue
		}

		// 最终回答
		content := strings.Join(textParts, "\n")
		content = stripThinkBlock(content)
		content = extractJSONFromMarkdown(content)

		var result worker.TaskResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("parse task result: %w (content: %s)", err, truncateStr(content, 500))
		}
		return &result, nil
	}

	// 达到最大轮次，强制结论
	log.Printf("[%s] forcing conclusion after %d rounds", d.name, maxToolRounds)
	forceContent, _ := json.Marshal([]anthContentBlock{{Type: "text", Text: "You have reached the maximum number of tool call rounds. You MUST now return a JSON result with your current findings, even if incomplete. Return format: {\"accepted\": true, \"data\": {\"description\": \"what you found so far\"}}"}})
	messages = append(messages, anthMessage{Role: "user", Content: forceContent})

	resp, err := d.chat(ctx, systemPrompt, messages, false)
	if err != nil {
		return nil, fmt.Errorf("conclusion failed: %w", err)
	}

	content := d.extractText(resp.Content)
	content = stripThinkBlock(content)
	content = extractJSONFromMarkdown(content)

	var result worker.TaskResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		return &result, nil
	}

	return nil, fmt.Errorf("exceeded max tool call rounds (%d)", maxToolRounds)
}

func (d *AnthropicDriver) Conclude(ctx context.Context, task *worker.Task, sessionID string) (*worker.TaskResult, error) {
	return d.Execute(ctx, task)
}

func (d *AnthropicDriver) SupportsConclude() bool {
	return true
}

// --- 内部方法 ---

func (d *AnthropicDriver) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", d.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}

// chat 发起一次 Messages API 调用
func (d *AnthropicDriver) chat(ctx context.Context, system string, messages []anthMessage, useTools bool) (*anthResponse, error) {
	reqBody := map[string]interface{}{
		"model":       d.model,
		"messages":    messages,
		"max_tokens":  16384,
		"temperature": 0.1,
		"system":      system,
	}

	if useTools {
		reqBody["tools"] = d.convertTools()
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", d.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	d.setHeaders(req)

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

	var anthResp anthResponse
	if err := json.Unmarshal(respBody, &anthResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &anthResp, nil
}

// convertTools 将 OpenAI 格式工具定义转为 Anthropic 格式
func (d *AnthropicDriver) convertTools() []anthTool {
	openaiTools := tools.AvailableTools()
	var result []anthTool
	for _, t := range openaiTools {
		fn, ok := t["function"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]interface{})
		if params == nil {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		result = append(result, anthTool{
			Name:        name,
			Description: desc,
			InputSchema: params,
		})
	}
	return result
}

// executeToolUses 执行工具调用并返回 tool_result content blocks
func (d *AnthropicDriver) executeToolUses(ctx context.Context, toolUses []anthContentBlock) json.RawMessage {
	type toolResultEntry struct {
		idx   int
		block anthContentBlock
	}

	results := make([]toolResultEntry, len(toolUses))

	if len(toolUses) == 1 {
		tu := toolUses[0]
		log.Printf("[%s] tool call: %s(%s)", d.name, tu.Name, truncateStr(string(tu.Input), 200))
		output := d.execSingleTool(ctx, tu)
		log.Printf("[%s] tool result: %s", d.name, truncateStr(output, 500))
		results[0] = toolResultEntry{idx: 0, block: anthContentBlock{
			Type:      "tool_result",
			ToolUseID: tu.ID,
			Content:   output,
		}}
	} else {
		log.Printf("[%s] executing %d tool calls in parallel", d.name, len(toolUses))
		var wg sync.WaitGroup
		for i, tu := range toolUses {
			wg.Add(1)
			go func(idx int, tu anthContentBlock) {
				defer wg.Done()
				log.Printf("[%s] tool call [%d]: %s(%s)", d.name, idx, tu.Name, truncateStr(string(tu.Input), 200))
				output := d.execSingleTool(ctx, tu)
				log.Printf("[%s] tool result [%d]: %s", d.name, idx, truncateStr(output, 500))
				results[idx] = toolResultEntry{idx: idx, block: anthContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   output,
				}}
			}(i, tu)
		}
		wg.Wait()
	}

	// 按顺序提取 blocks
	var blocks []anthContentBlock
	for _, r := range results {
		blocks = append(blocks, r.block)
	}
	data, _ := json.Marshal(blocks)
	return data
}

// execSingleTool 执行单个 Anthropic 工具调用
func (d *AnthropicDriver) execSingleTool(ctx context.Context, tu anthContentBlock) string {
	var args map[string]interface{}
	if err := json.Unmarshal(tu.Input, &args); err != nil {
		return fmt.Sprintf("ERROR: invalid tool arguments JSON: %v", err)
	}

	var result *tools.ToolResult
	// 优先使用 context 中绑定的执行器（并发安全）
	if ctxExec := toolExecutorFromContext(ctx); ctxExec != nil {
		result = ctxExec(ctx, tu.Name, args)
	} else {
		result = d.toolExecutor.Execute(ctx, tu.Name, args)
	}

	var output string
	if result.Error != "" {
		output = "ERROR: " + result.Error
		if result.Output != "" {
			output += "\n" + result.Output
		}
	} else {
		output = result.Output
	}
	if len(output) > 16000 {
		output = output[:16000] + "\n... [truncated]"
	}
	return output
}

// extractText 从响应 content blocks 中提取文本
func (d *AnthropicDriver) extractText(blocks []anthContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolUseSig 生成工具调用签名用于去重
func (d *AnthropicDriver) toolUseSig(uses []anthContentBlock) string {
	var parts []string
	for _, tu := range uses {
		parts = append(parts, tu.Name+":"+string(tu.Input))
	}
	return strings.Join(parts, "|")
}
