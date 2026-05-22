package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ptagent/ptagent/internal/config"
)

// InstanceStatus dispatcher 实例状态
type InstanceStatus string

const (
	StatusRunning InstanceStatus = "running"
	StatusStopped InstanceStatus = "stopped"
	StatusError   InstanceStatus = "error"
)

// Instance 表示一个 dispatcher 实例
type Instance struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Status    InstanceStatus         `json:"status"`
	Config    *config.DispatchConfig `json:"config"`
	StartedAt *time.Time             `json:"started_at"`
	Error     string                 `json:"error,omitempty"`

	dispatcher *Dispatcher
	cancel     context.CancelFunc
}

// InstanceInfo 返回给前端的实例信息（不含敏感字段）
type InstanceInfo struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Status        InstanceStatus `json:"status"`
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	Error         string         `json:"error,omitempty"`
	Workers       []WorkerInfo   `json:"workers"`
	Runtime       RuntimeInfo    `json:"runtime"`
	RunningTasks  int            `json:"running_tasks"`
	AdmittedCount int            `json:"admitted_count"`
}

// WorkerInfo worker 信息
type WorkerInfo struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	TaskTypes  []string `json:"task_types"`
	MaxRunning int      `json:"max_running"`
	Running    int      `json:"running"`
}

// RuntimeInfo 运行时配置信息
type RuntimeInfo struct {
	Interval           int    `json:"interval"`
	MaxWorkers         int    `json:"max_workers"`
	MaxRunningProjects int    `json:"max_running_projects"`
	MaxProjectWorkers  int    `json:"max_project_workers"`
	PromptGroup        string `json:"prompt_group"`
}

// Manager 管理多个 dispatcher 实例
type Manager struct {
	mu        sync.RWMutex
	instances map[string]*Instance
	nextID    int
}

// NewManager 创建 Manager
func NewManager() *Manager {
	return &Manager{
		instances: make(map[string]*Instance),
		nextID:    1,
	}
}

// List 列出所有实例
func (m *Manager) List() []InstanceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]InstanceInfo, 0, len(m.instances))
	for _, inst := range m.instances {
		result = append(result, m.buildInfo(inst))
	}
	return result
}

// Get 获取单个实例信息
func (m *Manager) Get(id string) (*InstanceInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.instances[id]
	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}
	info := m.buildInfo(inst)
	return &info, nil
}

// Create 创建并启动新的 dispatcher 实例
func (m *Manager) Create(cfg *config.DispatchConfig, name string) (*InstanceInfo, error) {
	d, err := New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create dispatcher: %w", err)
	}

	m.mu.Lock()
	id := fmt.Sprintf("disp_%03d", m.nextID)
	for {
		if _, exists := m.instances[id]; !exists {
			break
		}
		m.nextID++
		id = fmt.Sprintf("disp_%03d", m.nextID)
	}
	m.nextID++

	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())

	inst := &Instance{
		ID:         id,
		Name:       name,
		Status:     StatusRunning,
		Config:     cfg,
		StartedAt:  &now,
		dispatcher: d,
		cancel:     cancel,
	}
	m.instances[id] = inst
	m.mu.Unlock()

	go func() {
		if err := d.Run(ctx); err != nil {
			m.mu.Lock()
			inst.Status = StatusError
			inst.Error = err.Error()
			m.mu.Unlock()
		}
	}()

	info := m.buildInfo(inst)
	return &info, nil
}

// Register 注册一个已存在的 dispatcher（用于 cmd/ptagent 中已经启动的）
func (m *Manager) Register(id, name string, cfg *config.DispatchConfig, d *Dispatcher, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.instances[id] = &Instance{
		ID:         id,
		Name:       name,
		Status:     StatusRunning,
		Config:     cfg,
		StartedAt:  &now,
		dispatcher: d,
		cancel:     cancel,
	}
	// Ensure nextID doesn't collide with registered disp_NNN IDs.
	var n int
	if _, err := fmt.Sscanf(id, "disp_%d", &n); err == nil {
		if n >= m.nextID {
			m.nextID = n + 1
		}
	} else if m.nextID <= 1 {
		m.nextID = 2
	}
}

// Stop 停止实例
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	if inst.Status != StatusRunning {
		return fmt.Errorf("instance %s is not running", id)
	}

	inst.cancel()
	inst.Status = StatusStopped
	return nil
}

// Start 重启一个已停止的实例
func (m *Manager) Start(id string) error {
	m.mu.Lock()
	inst, ok := m.instances[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance %s not found", id)
	}
	if inst.Status == StatusRunning {
		m.mu.Unlock()
		return fmt.Errorf("instance %s is already running", id)
	}

	// 重新创建 dispatcher
	d, err := New(inst.Config)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("recreate dispatcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	inst.dispatcher = d
	inst.cancel = cancel
	inst.Status = StatusRunning
	inst.StartedAt = &now
	inst.Error = ""
	m.mu.Unlock()

	go func() {
		if err := d.Run(ctx); err != nil {
			m.mu.Lock()
			inst.Status = StatusError
			inst.Error = err.Error()
			m.mu.Unlock()
		}
	}()

	return nil
}

// Delete 删除实例（先停止如果在运行）
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}

	if inst.Status == StatusRunning {
		inst.cancel()
	}
	delete(m.instances, id)
	return nil
}

func (m *Manager) buildInfo(inst *Instance) InstanceInfo {
	info := InstanceInfo{
		ID:        inst.ID,
		Name:      inst.Name,
		Status:    inst.Status,
		StartedAt: inst.StartedAt,
		Error:     inst.Error,
	}

	if inst.Config != nil {
		info.Runtime = RuntimeInfo{
			Interval:           inst.Config.Runtime.Interval,
			MaxWorkers:         inst.Config.Runtime.MaxWorkers,
			MaxRunningProjects: inst.Config.Runtime.MaxRunningProjects,
			MaxProjectWorkers:  inst.Config.Runtime.MaxProjectWorkers,
			PromptGroup:        inst.Config.Runtime.PromptGroup,
		}

		for _, w := range inst.Config.Workers {
			wi := WorkerInfo{
				Name:       w.Name,
				Type:       w.Type,
				TaskTypes:  w.TaskTypes,
				MaxRunning: w.MaxRunning,
			}
			if inst.dispatcher != nil {
				inst.dispatcher.mu.Lock()
				wi.Running = inst.dispatcher.workerRunning[w.Name]
				inst.dispatcher.mu.Unlock()
			}
			info.Workers = append(info.Workers, wi)
		}
	}

	if inst.dispatcher != nil {
		inst.dispatcher.mu.Lock()
		info.RunningTasks = inst.dispatcher.runningTasks
		info.AdmittedCount = len(inst.dispatcher.admitted)
		inst.dispatcher.mu.Unlock()
	}

	return info
}
