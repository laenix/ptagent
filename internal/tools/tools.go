package tools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
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
	case "shell_exec":
		return e.shellExec(ctx, args)
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

// pythonExec 执行 Python 代码
func (e *Executor) pythonExec(ctx context.Context, args map[string]interface{}) *ToolResult {
	code, _ := args["code"].(string)
	if code == "" {
		return &ToolResult{Error: "code is required"}
	}

	timeout := 120 * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "python3", "-c", code)
	output, err := cmd.CombinedOutput()
	result := string(output)
	const maxOutput = 64 * 1024
	if len(result) > maxOutput {
		result = result[:maxOutput] + "\n... [truncated]"
	}
	if err != nil {
		return &ToolResult{Output: result, Error: err.Error()}
	}
	return &ToolResult{Output: result}
}

// shellExec 执行 Shell 命令
func (e *Executor) shellExec(ctx context.Context, args map[string]interface{}) *ToolResult {
	command, _ := args["command"].(string)
	if command == "" {
		return &ToolResult{Error: "command is required"}
	}

	timeout := 120
	if t, ok := args["timeout"].(float64); ok && t > 0 && t <= 600 {
		timeout = int(t)
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	output, err := cmd.CombinedOutput()
	result := string(output)
	const maxOutput = 64 * 1024
	if len(result) > maxOutput {
		result = result[:maxOutput] + "\n... [truncated]"
	}
	if err != nil {
		return &ToolResult{Output: result, Error: err.Error()}
	}
	return &ToolResult{Output: result}
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
				"description": "Execute a shell command. Use for running tools like nmap, sqlmap, gobuster, curl, hashcat, john, openssl, etc. Also useful for piping data, file operations, and chaining commands.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Shell command to execute, e.g. 'nmap -sV target.com'",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Timeout in seconds (default 120, max 600)",
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
				"description": "Execute Python code. Use for writing exploit scripts, data processing, complex logic, and cryptography tasks. Available libraries include: pycryptodome (AES/RSA/DES/ChaCha20), hashlib, base64, binascii, struct, gmpy2 (for RSA math), sympy (symbolic math), z3-solver (constraint solving/SAT). Use for: block cipher attacks (ECB/CBC padding oracle/bit flipping), RSA (factoring/Wiener/Boneh-Durfee/Hastad/CRT), hash cracking/length extension, classical ciphers (Caesar/Vigenere/substitution), XOR analysis, encoding/decoding, and custom crypto exploitation.",
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
