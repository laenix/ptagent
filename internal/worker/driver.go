package worker

import (
	"context"
)

// TaskType 任务类型
type TaskType string

const (
	TaskBootstrap TaskType = "bootstrap"
	TaskReason    TaskType = "reason"
	TaskExplore   TaskType = "explore"
)

// TaskResult Worker 执行结果
type TaskResult struct {
	Accepted bool        `json:"accepted"`
	Data     interface{} `json:"data,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

// BootstrapData Bootstrap 任务输出
type BootstrapData struct {
	Fact     *FactData     `json:"fact,omitempty"`
	Complete *CompleteData `json:"complete,omitempty"`
}

// ReasonData Reason 任务输出
type ReasonData struct {
	Complete *CompleteData `json:"complete,omitempty"`
	Intent   *IntentData   `json:"intent,omitempty"`
}

// ExploreData Explore 任务输出
type ExploreData struct {
	Description string `json:"description"`
}

type FactData struct {
	Description string `json:"description"`
}

type CompleteData struct {
	From        []string `json:"from,omitempty"`
	Description string   `json:"description"`
}

type IntentData struct {
	From        []string `json:"from"`
	Description string   `json:"description"`
}

// Driver Worker 驱动接口
type Driver interface {
	// Name 驱动名称
	Name() string

	// Healthcheck 健康检查
	Healthcheck(ctx context.Context) error

	// Execute 执行任务
	Execute(ctx context.Context, task *Task) (*TaskResult, error)

	// Conclude 收尾阶段（双阶段模式）
	Conclude(ctx context.Context, task *Task, sessionID string) (*TaskResult, error)

	// SupportsConclude 是否支持双阶段
	SupportsConclude() bool
}

// Task 任务描述
type Task struct {
	Type        TaskType          `json:"type"`
	ProjectID   string            `json:"project_id"`
	IntentID    string            `json:"intent_id,omitempty"`
	Prompt      string            `json:"prompt"`
	Env         map[string]string `json:"env"`
	Timeout     int               `json:"timeout"`
	ContainerID string            `json:"container_id,omitempty"`
}
