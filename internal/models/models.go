package models

import "time"

// ProjectStatus 项目状态
type ProjectStatus string

const (
	ProjectStatusActive    ProjectStatus = "active"
	ProjectStatusStopped   ProjectStatus = "stopped"
	ProjectStatusCompleted ProjectStatus = "completed"
)

// Project 项目 — 一个有明确起点和终点的问题实例
type Project struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Status    ProjectStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	Reason    *ReasonLease  `json:"reason"`
}

// ReasonLease 项目级 reason 租约
type ReasonLease struct {
	Worker          string    `json:"worker"`
	Trigger         string    `json:"trigger"`
	StartedAt       time.Time `json:"started_at"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
}

// ProjectSummary 项目列表概览
type ProjectSummary struct {
	Project
	FactCount            int `json:"fact_count"`
	IntentCount          int `json:"intent_count"`
	WorkingIntentCount   int `json:"working_intent_count"`
	UnclaimedIntentCount int `json:"unclaimed_intent_count"`
	HintCount            int `json:"hint_count"`
}

// Fact 图节点 — 已确认的客观事实，只增不改
type Fact struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Intent 图边 — 从一个或多个 Fact 出发的探索过程
type Intent struct {
	ID              string     `json:"id"`
	From            []string   `json:"from"`
	To              *string    `json:"to"`
	Description     string     `json:"description"`
	Creator         string     `json:"creator"`
	Worker          *string    `json:"worker"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	CreatedAt       time.Time  `json:"created_at"`
	ConcludedAt     *time.Time `json:"concluded_at"`
}

// IsOpen 是否尚无结论
func (i *Intent) IsOpen() bool {
	return i.To == nil
}

// IsClaimed 是否已被认领
func (i *Intent) IsClaimed() bool {
	return i.Worker != nil && *i.Worker != ""
}

// Hint 图外输入 — 策略建议或补充说明
type Hint struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Creator   string    `json:"creator"`
	CreatedAt time.Time `json:"created_at"`
}

// Settings 全局设置
type Settings struct {
	IntentTimeout int `json:"intent_timeout" yaml:"intent_timeout"` // 秒
	ReasonTimeout int `json:"reason_timeout" yaml:"reason_timeout"` // 秒
}

// --- Request / Response DTOs ---

// CreateProjectRequest 创建项目请求
type CreateProjectRequest struct {
	Title  string             `json:"title" binding:"required"`
	Origin string             `json:"origin" binding:"required"`
	Goal   string             `json:"goal" binding:"required"`
	Hints  []CreateHintParams `json:"hints"`
}

type CreateHintParams struct {
	Content string `json:"content" binding:"required"`
	Creator string `json:"creator" binding:"required"`
}

// CreateIntentRequest 声明探索意图
type CreateIntentRequest struct {
	From        []string `json:"from" binding:"required,min=1"`
	Description string   `json:"description" binding:"required"`
	Creator     string   `json:"creator" binding:"required"`
	Worker      *string  `json:"worker"`
}

// HeartbeatRequest 心跳/认领请求
type HeartbeatRequest struct {
	Worker string `json:"worker" binding:"required"`
}

// ConcludeRequest 结论落定请求
type ConcludeRequest struct {
	Worker      string `json:"worker" binding:"required"`
	Description string `json:"description" binding:"required"`
}

// CompleteRequest 项目完成请求
type CompleteRequest struct {
	From        []string `json:"from" binding:"required,min=1"`
	Description string   `json:"description" binding:"required"`
	Worker      string   `json:"worker" binding:"required"`
}

// ReopenRequest 撤销完成态
type ReopenRequest struct {
	Description string `json:"description" binding:"required"`
	Creator     string `json:"creator" binding:"required"`
}

// StatusUpdateRequest 状态变更
type StatusUpdateRequest struct {
	Status ProjectStatus `json:"status" binding:"required"`
}

// TitleUpdateRequest 标题变更
type TitleUpdateRequest struct {
	Title string `json:"title" binding:"required"`
}

// ReasonClaimRequest reason lease 认领
type ReasonClaimRequest struct {
	Worker  string `json:"worker" binding:"required"`
	Trigger string `json:"trigger" binding:"required"`
}

// ProjectDetailResponse 项目详情响应
type ProjectDetailResponse struct {
	Project Project  `json:"project"`
	Facts   []Fact   `json:"facts"`
	Intents []Intent `json:"intents"`
	Hints   []Hint   `json:"hints"`
}

// ConcludeResponse 结论落定响应
type ConcludeResponse struct {
	Fact   Fact   `json:"fact"`
	Intent Intent `json:"intent"`
}

// FactTagCounts 单个项目的 fact 标签统计
type FactTagCounts struct {
	SuccessCount int `json:"success_count"`
	FailureCount int `json:"failure_count"`
	BlockerCount int `json:"blocker_count"`
}

// ReopenResponse 重开响应
type ReopenResponse struct {
	Project Project `json:"project"`
	Fact    Fact    `json:"fact"`
	Intent  Intent  `json:"intent"`
}

// --- Task Event (timeline replay) ---

// TaskEvent 任务事件 — 记录 dispatcher 每次任务的完整生命周期
type TaskEvent struct {
	ID         int64     `json:"id"`
	ProjectID  string    `json:"project_id"`
	TaskType   string    `json:"task_type"`   // bootstrap / reason / explore
	IntentID   string    `json:"intent_id"`   // explore 时关联的 intent
	Worker     string    `json:"worker"`      // 执行的 worker 名
	Phase      string    `json:"phase"`       // dispatched / healthcheck_failed / executing / concluded / succeed / failed / rejected / cancelled
	Prompt     string    `json:"prompt"`      // 发送给 LLM 的完整 prompt
	Output     string    `json:"output"`      // LLM 返回的原始结果 JSON
	Error      string    `json:"error"`       // 错误信息
	DurationMs int64     `json:"duration_ms"` // 耗时
	CreatedAt  time.Time `json:"created_at"`
}

// TaskEventFilter 查询过滤
type TaskEventFilter struct {
	TaskType string `form:"task_type"`
	Worker   string `form:"worker"`
	Phase    string `form:"phase"`
	Limit    int    `form:"limit"`
	Offset   int    `form:"offset"`
}

// ToolEvent 工具调用事件 — 记录 Worker 中每个工具的调用
type ToolEvent struct {
	ID         int64     `json:"id"`
	ProjectID  string    `json:"project_id"`
	TaskType   string    `json:"task_type"` // bootstrap / reason / explore
	IntentID   string    `json:"intent_id,omitempty"`
	Worker     string    `json:"worker"`      // 执行的 worker 名
	Tool       string    `json:"tool"`        // 工具名: http_request, shell_exec, etc.
	Args       string    `json:"args"`        // 调用参数 JSON
	Output     string    `json:"output"`      // 执行结果
	Error      string    `json:"error"`       // 错误信息
	DurationMs int64     `json:"duration_ms"` // 耗时
	CreatedAt  time.Time `json:"created_at"`
}

// ToolEventFilter 查询过滤
type ToolEventFilter struct {
	Tool    string `form:"tool"`
	Project string `form:"project"`
	Limit   int    `form:"limit"`
	Offset  int    `form:"offset"`
}

// --- CTFd Integration ---

// CTFdInstance 外部 CTFd 平台配置
type CTFdInstance struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Token     string    `json:"token,omitempty"` // API Token（列表时不返回）
	CreatedAt time.Time `json:"created_at"`
}

// CTFdChallenge CTFd 题目
type CTFdChallenge struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	Category       string     `json:"category"`
	Description    string     `json:"description"`
	Value          int        `json:"value"`
	Solves         int        `json:"solves"`
	Type           string     `json:"type"`
	Tags           []string   `json:"tags"`
	Files          []CTFdFile `json:"files"`
	Solved         bool       `json:"solved"`
	ConnectionInfo string     `json:"connection_info"`
	MaxAttempts    int        `json:"max_attempts"`
	Attempts       int        `json:"attempts"`
}

// CTFdFile CTFd 附件
type CTFdFile struct {
	ID       int    `json:"id"`
	Location string `json:"location"` // 相对路径
}

// CTFdSubmitRequest 提交 flag
type CTFdSubmitRequest struct {
	ChallengeID int    `json:"challenge_id" binding:"required"`
	Flag        string `json:"flag" binding:"required"`
}

// CTFdSubmitResponse 提交结果
type CTFdSubmitResponse struct {
	Status  string `json:"status"` // correct / incorrect / already_solved
	Message string `json:"message"`
}

// CTFdImportRequest 从 CTFd 题目导入为项目
type CTFdImportRequest struct {
	InstanceID  string `json:"instance_id" binding:"required"`
	ChallengeID int    `json:"challenge_id" binding:"required"`
}

// AddCTFdInstanceRequest 添加 CTFd 实例
type AddCTFdInstanceRequest struct {
	Name  string `json:"name" binding:"required"`
	URL   string `json:"url" binding:"required"`
	Token string `json:"token" binding:"required"`
}

// CTFdProjectLink 项目与 CTFd 题目的关联
type CTFdProjectLink struct {
	ProjectID       string `json:"project_id"`
	CTFdInstanceID  string `json:"ctfd_instance_id"`
	CTFdChallengeID int    `json:"ctfd_challenge_id"`
	AutoSubmit      bool   `json:"auto_submit"`
}

// AgentChatRequest 平台 Agent 聊天请求
type AgentChatRequest struct {
	Message string `json:"message" binding:"required"`
}

// AgentChatResponse 平台 Agent 聊天响应
type AgentChatResponse struct {
	Reply   string        `json:"reply"`
	Actions []AgentAction `json:"actions,omitempty"`
}

// AgentAction Agent 执行的操作记录
type AgentAction struct {
	Type   string `json:"type"`   // submit_flag, stop_project, reopen_project, etc.
	Detail string `json:"detail"` // 操作描述
	Result string `json:"result"` // 操作结果
}

// AgentConfig 平台 Agent LLM 配置
type AgentConfig struct {
	LLMBaseURL string `json:"llm_base_url"`
	LLMAPIKey  string `json:"llm_api_key,omitempty"`
	LLMModel   string `json:"llm_model"`
}
