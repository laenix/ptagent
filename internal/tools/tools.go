package tools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tool 工具定义
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}

// ToolResult 工具执行结果
type ToolResult struct {
	Output string
	Error  string
}

// Executor 工具执行器
type Executor struct {
	httpClient *http.Client
}

// NewExecutor 创建工具执行器，proxyURL 为空则不走代理
func NewExecutor(proxyURL string) *Executor {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &Executor{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// Execute 执行工具调用
func (e *Executor) Execute(ctx context.Context, name string, args map[string]interface{}) *ToolResult {
	switch name {
	case "http_request":
		return e.httpRequest(ctx, args)
	case "python_exec":
		return e.pythonExec(ctx, args)
	default:
		return &ToolResult{Error: fmt.Sprintf("unknown tool: %s", name)}
	}
}

// httpRequest 执行 HTTP 请求
func (e *Executor) httpRequest(ctx context.Context, args map[string]interface{}) *ToolResult {
	method, _ := args["method"].(string)
	if method == "" {
		method = "GET"
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return &ToolResult{Error: "url is required"}
	}

	var bodyReader io.Reader
	if body, ok := args["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), rawURL, bodyReader)
	if err != nil {
		return &ToolResult{Error: fmt.Sprintf("build request: %v", err)}
	}

	// Headers
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if sv, ok := v.(string); ok {
				req.Header.Set(k, sv)
			}
		}
	}

	// 默认 Content-Type
	if req.Header.Get("Content-Type") == "" && bodyReader != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return &ToolResult{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	// 限制读取 body 大小
	const maxBody = 64 * 1024
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return &ToolResult{Error: fmt.Sprintf("read response: %v", err)}
	}

	// 构造输出
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP %d %s\n", resp.StatusCode, resp.Status))
	for k, vs := range resp.Header {
		for _, v := range vs {
			sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
		}
	}
	sb.WriteString("\n")
	sb.Write(respBody)

	return &ToolResult{Output: sb.String()}
}

// pythonExec 暂未实现
func (e *Executor) pythonExec(ctx context.Context, args map[string]interface{}) *ToolResult {
	return &ToolResult{Error: "python_exec not yet implemented"}
}

// AvailableTools 返回可用工具的 OpenAI function 格式定义
func AvailableTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "http_request",
				"description": "Send an HTTP request to a target. Use this to interact with the target web application.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"method": map[string]interface{}{
							"type":        "string",
							"description": "HTTP method: GET, POST, PUT, DELETE, etc.",
							"enum":        []string{"GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH"},
						},
						"url": map[string]interface{}{
							"type":        "string",
							"description": "Full URL to request, e.g. http://example.com/login",
						},
						"headers": map[string]interface{}{
							"type":        "object",
							"description": "HTTP headers as key-value pairs",
							"additionalProperties": map[string]interface{}{
								"type": "string",
							},
						},
						"body": map[string]interface{}{
							"type":        "string",
							"description": "Request body (for POST/PUT). Can be form data or JSON string.",
						},
					},
					"required": []string{"url"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "shell_exec",
				"description": "Execute a shell command. Use for running tools like nmap, sqlmap, gobuster, curl, etc.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Shell command to execute, e.g. 'nmap -sV target.com'",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Timeout in seconds (default 120)",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "python_exec",
				"description": "Execute Python code. Use for writing exploit scripts, data processing, or complex logic.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"code": map[string]interface{}{
							"type":        "string",
							"description": "Python code to execute",
						},
					},
					"required": []string{"code"},
				},
			},
		},
	}
}
