package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/ptagent/ptagent/internal/worker"
)

// MockDriver 本地模拟测试驱动
type MockDriver struct {
	name string
	env  map[string]string
}

// NewMockDriver 创建 Mock 驱动
func NewMockDriver(name string, env map[string]string) *MockDriver {
	return &MockDriver{name: name, env: env}
}

func (d *MockDriver) Name() string { return d.name }

func (d *MockDriver) Healthcheck(ctx context.Context) error {
	cfg := d.getPhaseConfig("MOCK_HEALTHCHECK")
	if cfg == nil {
		return nil
	}

	d.simulateDelay(cfg)

	outcome := d.pickOutcome(cfg)
	if outcome == "fail" {
		return fmt.Errorf("mock healthcheck failed")
	}
	return nil
}

func (d *MockDriver) Execute(ctx context.Context, task *worker.Task) (*worker.TaskResult, error) {
	var phase string
	switch task.Type {
	case worker.TaskBootstrap:
		phase = "MOCK_BOOTSTRAP"
	case worker.TaskReason:
		phase = "MOCK_REASON"
	case worker.TaskExplore:
		phase = "MOCK_EXPLORE_EXECUTE"
	}

	cfg := d.getPhaseConfig(phase)
	if cfg == nil {
		return &worker.TaskResult{Accepted: true, Data: map[string]string{"description": "mock result"}}, nil
	}

	d.simulateDelay(cfg)

	outcome := d.pickOutcome(cfg)
	return d.buildResult(task.Type, outcome), nil
}

func (d *MockDriver) Conclude(ctx context.Context, task *worker.Task, sessionID string) (*worker.TaskResult, error) {
	var phase string
	switch task.Type {
	case worker.TaskBootstrap:
		phase = "MOCK_BOOTSTRAP_CONCLUDE"
	case worker.TaskExplore:
		phase = "MOCK_EXPLORE_CONCLUDE"
	default:
		return &worker.TaskResult{Accepted: true, Data: map[string]string{"description": "mock conclude"}}, nil
	}

	cfg := d.getPhaseConfig(phase)
	if cfg == nil {
		return &worker.TaskResult{Accepted: true, Data: map[string]string{"description": "mock conclude"}}, nil
	}

	d.simulateDelay(cfg)
	outcome := d.pickOutcome(cfg)
	return d.buildResult(task.Type, outcome), nil
}

func (d *MockDriver) SupportsConclude() bool { return true }

type mockPhaseConfig struct {
	Delay    [2]float64         `json:"delay"`
	Outcomes map[string]float64 `json:"outcomes"`
}

func (d *MockDriver) getPhaseConfig(envKey string) *mockPhaseConfig {
	raw, ok := d.env[envKey]
	if !ok {
		return nil
	}
	var cfg mockPhaseConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil
	}
	return &cfg
}

func (d *MockDriver) simulateDelay(cfg *mockPhaseConfig) {
	if cfg.Delay[1] > 0 {
		delay := cfg.Delay[0] + rand.Float64()*(cfg.Delay[1]-cfg.Delay[0])
		time.Sleep(time.Duration(delay * float64(time.Second)))
	}
}

func (d *MockDriver) pickOutcome(cfg *mockPhaseConfig) string {
	r := rand.Float64()
	cumulative := 0.0
	for outcome, prob := range cfg.Outcomes {
		cumulative += prob
		if r < cumulative {
			return outcome
		}
	}
	// fallback to first
	for outcome := range cfg.Outcomes {
		return outcome
	}
	return "fact"
}

func (d *MockDriver) buildResult(taskType worker.TaskType, outcome string) *worker.TaskResult {
	switch outcome {
	case "rejected":
		return &worker.TaskResult{Accepted: false, Reason: "mock_rejected"}
	case "invalid_json":
		return nil // caller handles nil as parse failure
	case "invalid_payload":
		return &worker.TaskResult{Accepted: true} // missing required fields
	case "command_fail":
		return nil
	case "fact":
		return &worker.TaskResult{
			Accepted: true,
			Data:     map[string]string{"description": fmt.Sprintf("Mock fact discovered at %s", time.Now().Format(time.RFC3339))},
		}
	case "complete":
		return &worker.TaskResult{
			Accepted: true,
			Data: map[string]interface{}{
				"complete": map[string]interface{}{
					"from":        []string{"f001"},
					"description": "Mock: goal achieved",
				},
			},
		}
	case "intent":
		return &worker.TaskResult{
			Accepted: true,
			Data: map[string]interface{}{
				"intent": map[string]interface{}{
					"from":        []string{"f001"},
					"description": fmt.Sprintf("Mock intent: explore direction %d", rand.Intn(100)),
				},
			},
		}
	case "noop":
		return &worker.TaskResult{Accepted: true, Data: map[string]interface{}{}}
	default:
		return &worker.TaskResult{
			Accepted: true,
			Data:     map[string]string{"description": "mock default result"},
		}
	}
}
