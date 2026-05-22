package config

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DispatchConfig Dispatcher 配置
type DispatchConfig struct {
	Server    string            `yaml:"server"`
	Proxy     ProxyConfig       `yaml:"proxy"`
	CommonEnv map[string]string `yaml:"common_env"`
	Runtime   RuntimeConfig     `yaml:"runtime"`
	Tasks     TasksConfig       `yaml:"tasks"`
	Container ContainerConfig   `yaml:"container"`
	Workers   []WorkerConfig    `yaml:"workers"`
}

// ProxyConfig 代理配置
type ProxyConfig struct {
	HTTPProxy   string `yaml:"http_proxy"`
	HTTPSProxy  string `yaml:"https_proxy"`
	Socks5Proxy string `yaml:"socks5_proxy"`
	NoProxy     string `yaml:"no_proxy"`
	ProxyLLMAPI bool   `yaml:"proxy_llm_api"`
}

// RuntimeConfig 运行时配置
type RuntimeConfig struct {
	Interval           int    `yaml:"interval"`             // 主循环间隔和 heartbeat 周期（秒）
	MaxWorkers         int    `yaml:"max_workers"`          // 同时运行的任务总数上限
	MaxRunningProjects int    `yaml:"max_running_projects"` // 活跃项目上限
	MaxProjectWorkers  int    `yaml:"max_project_workers"`  // 单项目内并发任务上限
	HealthcheckTimeout int    `yaml:"healthcheck_timeout"`  // Worker 健康检查超时（秒）
	PromptGroup        string `yaml:"prompt_group"`         // Prompt 组目录名
}

// TasksConfig 任务超时配置
type TasksConfig struct {
	Bootstrap BootstrapTaskConfig `yaml:"bootstrap"`
	Reason    ReasonTaskConfig    `yaml:"reason"`
	Explore   ExploreTaskConfig   `yaml:"explore"`
}

type BootstrapTaskConfig struct {
	Timeout         int `yaml:"timeout"`          // 第一阶段超时（秒）
	ConcludeTimeout int `yaml:"conclude_timeout"` // 收尾阶段超时（秒）
}

type ReasonTaskConfig struct {
	Timeout    int `yaml:"timeout"`     // 超时（秒）
	MaxIntents int `yaml:"max_intents"` // 单次 reason 最大 intent 数
}

type ExploreTaskConfig struct {
	Timeout         int `yaml:"timeout"`          // 第一阶段超时（秒）
	ConcludeTimeout int `yaml:"conclude_timeout"` // 收尾阶段超时（秒）
}

// ContainerConfig 容器配置
type ContainerConfig struct {
	Image           string            `yaml:"image"`
	NetworkMode     string            `yaml:"network_mode"`
	CompletedAction string            `yaml:"completed_action"` // "remove" | "stop"
	CapAdd          []string          `yaml:"cap_add"`
	Enabled         bool              `yaml:"enabled"`   // 是否启用容器化执行
	ProxyEnv        map[string]string `yaml:"proxy_env"` // 注入到容器的代理环境变量
	ServerURL       string            `yaml:"server_url"` // Server API URL（注入到容器环境变量）
}

// WorkerConfig Worker 配置
type WorkerConfig struct {
	Name       string            `yaml:"name"`
	Type       string            `yaml:"type"`       // openai, anthropic, shell, mock
	TaskTypes  []string          `yaml:"task_types"` // bootstrap, reason, explore
	MaxRunning int               `yaml:"max_running"`
	Priority   int               `yaml:"priority"`
	Env        map[string]string `yaml:"env"`
}

// LoadDispatchConfig 加载配置文件
func LoadDispatchConfig(path string) (*DispatchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg DispatchConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	// 环境变量覆盖 (方便 Docker 部署)
	if s := os.Getenv("PTAGENT_SERVER"); s != "" {
		cfg.Server = s
	}

	// 合并 common_env 到每个 worker
	for i := range cfg.Workers {
		if cfg.Workers[i].Env == nil {
			cfg.Workers[i].Env = make(map[string]string)
		}
		for k, v := range cfg.CommonEnv {
			if _, exists := cfg.Workers[i].Env[k]; !exists {
				cfg.Workers[i].Env[k] = v
			}
		}
	}

	return &cfg, nil
}

// Validate 校验配置
func (c *DispatchConfig) Validate() error {
	if c.Server == "" {
		return fmt.Errorf("server is required")
	}
	if c.Runtime.MaxWorkers <= 0 {
		return fmt.Errorf("runtime.max_workers must be positive")
	}
	if c.Runtime.MaxRunningProjects <= 0 {
		return fmt.Errorf("runtime.max_running_projects must be positive")
	}
	if c.Runtime.MaxProjectWorkers <= 0 {
		return fmt.Errorf("runtime.max_project_workers must be positive")
	}
	if c.Runtime.Interval <= 0 {
		return fmt.Errorf("runtime.interval must be positive")
	}
	if c.Runtime.HealthcheckTimeout <= 0 {
		return fmt.Errorf("runtime.healthcheck_timeout must be positive")
	}
	if c.Runtime.PromptGroup == "" {
		return fmt.Errorf("runtime.prompt_group is required")
	}
	if c.Tasks.Bootstrap.Timeout <= 0 {
		return fmt.Errorf("tasks.bootstrap.timeout must be positive")
	}
	if c.Tasks.Reason.Timeout <= 0 {
		return fmt.Errorf("tasks.reason.timeout must be positive")
	}
	if c.Tasks.Explore.Timeout <= 0 {
		return fmt.Errorf("tasks.explore.timeout must be positive")
	}

	validTypes := map[string]bool{"openai": true, "anthropic": true, "shell": true, "mock": true}
	validTasks := map[string]bool{"bootstrap": true, "reason": true, "explore": true}

	for _, w := range c.Workers {
		if w.Name == "" {
			return fmt.Errorf("worker name is required")
		}
		if !validTypes[w.Type] {
			return fmt.Errorf("worker %s: invalid type %q", w.Name, w.Type)
		}
		if w.MaxRunning <= 0 {
			return fmt.Errorf("worker %s: max_running must be positive", w.Name)
		}
		for _, t := range w.TaskTypes {
			if !validTasks[t] {
				return fmt.Errorf("worker %s: invalid task_type %q", w.Name, t)
			}
		}
	}

	return nil
}

// BuildTransport 根据代理配置构建 http.Transport
// forLLM=true 时只在 proxy_llm_api=true 时使用代理
func (p *ProxyConfig) BuildTransport(forLLM bool) *http.Transport {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// 如果是 LLM 请求且未开启 proxy_llm_api，不设代理
	if forLLM && !p.ProxyLLMAPI {
		return transport
	}

	proxyURL := p.HTTPSProxy
	if proxyURL == "" {
		proxyURL = p.HTTPProxy
	}
	if proxyURL == "" {
		return transport
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return transport
	}

	noProxyList := strings.Split(p.NoProxy, ",")
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		host := req.URL.Hostname()
		for _, np := range noProxyList {
			np = strings.TrimSpace(np)
			if np == "" {
				continue
			}
			if strings.Contains(np, "/") {
				_, cidr, err := net.ParseCIDR(np)
				if err == nil {
					if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
						return nil, nil
					}
				}
			} else if host == np || strings.HasSuffix(host, "."+np) {
				return nil, nil
			}
		}
		return parsed, nil
	}

	return transport
}

// BuildHTTPClient 构建带代理的 HTTP Client
func (p *ProxyConfig) BuildHTTPClient(forLLM bool, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: p.BuildTransport(forLLM),
		Timeout:   timeout,
	}
}
