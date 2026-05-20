package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// tool-agent: 轻量级 HTTP 服务，运行在 Docker 容器内
// 提供工具执行接口供 dispatcher 调用

func main() {
	addr := ":9999"
	if v := os.Getenv("TOOLAGENT_ADDR"); v != "" {
		addr = v
	}

	proxyURL := os.Getenv("HTTP_PROXY")
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTPS_PROXY")
	}

	agent := &ToolAgent{
		httpClient: buildHTTPClient(proxyURL),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", agent.healthHandler)
	mux.HandleFunc("/execute", agent.executeHandler)

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("[tool-agent] listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[tool-agent] listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func buildHTTPClient(proxyURL string) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

type ToolAgent struct {
	httpClient *http.Client
}

type ExecuteRequest struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args"`
}

type ExecuteResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func (a *ToolAgent) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *ToolAgent) executeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body: "+err.Error())
		return
	}

	var resp ExecuteResponse
	switch req.Tool {
	case "http_request":
		resp = a.httpRequest(r.Context(), req.Args)
	case "python_exec":
		resp = a.pythonExec(r.Context(), req.Args)
	case "shell_exec":
		resp = a.shellExec(r.Context(), req.Args)
	default:
		resp = ExecuteResponse{Error: fmt.Sprintf("unknown tool: %s", req.Tool)}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ExecuteResponse{Error: msg})
}

// --- Tool Implementations ---

func (a *ToolAgent) httpRequest(ctx context.Context, args map[string]interface{}) ExecuteResponse {
	method, _ := args["method"].(string)
	if method == "" {
		method = "GET"
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return ExecuteResponse{Error: "url is required"}
	}

	var bodyReader io.Reader
	if body, ok := args["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), rawURL, bodyReader)
	if err != nil {
		return ExecuteResponse{Error: fmt.Sprintf("build request: %v", err)}
	}

	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if sv, ok := v.(string); ok {
				req.Header.Set(k, sv)
			}
		}
	}
	if req.Header.Get("Content-Type") == "" && bodyReader != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return ExecuteResponse{Error: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	const maxBody = 64 * 1024
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return ExecuteResponse{Error: fmt.Sprintf("read response: %v", err)}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP %d %s\n", resp.StatusCode, resp.Status))
	for k, vs := range resp.Header {
		for _, v := range vs {
			sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
		}
	}
	sb.WriteString("\n")
	sb.Write(respBody)

	return ExecuteResponse{Output: sb.String()}
}

func (a *ToolAgent) pythonExec(ctx context.Context, args map[string]interface{}) ExecuteResponse {
	code, _ := args["code"].(string)
	if code == "" {
		return ExecuteResponse{Error: "code is required"}
	}

	cmd := exec.CommandContext(ctx, "python3", "-c", code)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return ExecuteResponse{
			Output: string(output),
			Error:  fmt.Sprintf("python exit: %v", err),
		}
	}
	return ExecuteResponse{Output: string(output)}
}

func (a *ToolAgent) shellExec(ctx context.Context, args map[string]interface{}) ExecuteResponse {
	command, _ := args["command"].(string)
	if command == "" {
		return ExecuteResponse{Error: "command is required"}
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return ExecuteResponse{
			Output: string(output),
			Error:  fmt.Sprintf("shell exit: %v", err),
		}
	}
	return ExecuteResponse{Output: string(output)}
}
