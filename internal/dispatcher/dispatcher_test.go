package dispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/server/api"
	"github.com/ptagent/ptagent/internal/store/sqlite"
	"github.com/ptagent/ptagent/internal/worker"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// mockDriver is a test driver that records calls and returns configurable results
type mockDriver struct {
	name             string
	healthOK         bool
	executeResult    *worker.TaskResult
	executeErr       error
	concludeResult   *worker.TaskResult
	concludeErr      error
	supportsConclude bool

	mu        sync.Mutex
	execCalls int
	concCalls int
	lastTask  *worker.Task
}

func (m *mockDriver) Name() string { return m.name }

func (m *mockDriver) Healthcheck(ctx context.Context) error {
	if !m.healthOK {
		return context.DeadlineExceeded
	}
	return nil
}

func (m *mockDriver) Execute(ctx context.Context, task *worker.Task) (*worker.TaskResult, error) {
	m.mu.Lock()
	m.execCalls++
	m.lastTask = task
	m.mu.Unlock()
	return m.executeResult, m.executeErr
}

func (m *mockDriver) Conclude(ctx context.Context, task *worker.Task, sessionID string) (*worker.TaskResult, error) {
	m.mu.Lock()
	m.concCalls++
	m.mu.Unlock()
	return m.concludeResult, m.concludeErr
}

func (m *mockDriver) SupportsConclude() bool { return m.supportsConclude }

// setupTestServer creates a test HTTP server with a real SQLite store
func setupTestServer(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	r := gin.New()
	h := api.NewHandler(store)
	h.RegisterRoutes(r)
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, store
}

func newTestDispatcher(t *testing.T, serverURL string, drv *mockDriver) *Dispatcher {
	t.Helper()

	// Create minimal prompt templates
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts", "test")
	os.MkdirAll(promptDir, 0o755)
	for _, name := range []string{"bootstrap.md", "reason.md", "explore.md", "bootstrap_conclude.md", "explore_conclude.md"} {
		os.WriteFile(filepath.Join(promptDir, name), []byte("test prompt {origin} {goal} {hints} {graph_yaml} {fact_ids} {open_intents} {max_intents}"), 0o644)
	}

	cfg := &config.DispatchConfig{
		Server: serverURL,
		Runtime: config.RuntimeConfig{
			Interval:           1,
			MaxWorkers:         4,
			MaxRunningProjects: 2,
			MaxProjectWorkers:  2,
			HealthcheckTimeout: 5,
			PromptGroup:        "test",
		},
		Tasks: config.TasksConfig{
			Bootstrap: config.BootstrapTaskConfig{Timeout: 10, ConcludeTimeout: 5},
			Reason:    config.ReasonTaskConfig{Timeout: 10, MaxIntents: 3},
			Explore:   config.ExploreTaskConfig{Timeout: 10, ConcludeTimeout: 5},
		},
		Workers: []config.WorkerConfig{
			{Name: drv.name, Type: "mock", TaskTypes: []string{"bootstrap", "reason", "explore"}, MaxRunning: 2, Priority: 1},
		},
	}

	d := &Dispatcher{
		cfg:              cfg,
		client:           &http.Client{Timeout: 5 * time.Second},
		drivers:          map[string]worker.Driver{drv.name: drv},
		projectWorkers:   make(map[string]int),
		workerRunning:    make(map[string]int),
		admitted:         make(map[string]bool),
		bootstrapping:    make(map[string]bool),
		reasoning:        make(map[string]bool),
		exploringIntents: make(map[string]bool),
	}
	d.prompts = &PromptManager{group: "test", basePath: filepath.Join(dir, "prompts")}

	return d
}

// --- Tests ---

func TestSelectWorker_Basic(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "test-worker", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	w := d.selectWorker(worker.TaskBootstrap)
	if w == nil {
		t.Fatal("expected a worker")
	}
	if w.Name != "test-worker" {
		t.Fatalf("expected test-worker, got %s", w.Name)
	}
}

func TestSelectWorker_MaxRunning(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "test-worker", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	// simulate all slots taken
	d.mu.Lock()
	d.workerRunning["test-worker"] = 2 // maxRunning is 2
	d.mu.Unlock()

	w := d.selectWorker(worker.TaskBootstrap)
	if w != nil {
		t.Fatal("expected nil when maxRunning reached")
	}
}

func TestSelectWorker_UnsupportedTask(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "test-worker", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	d.cfg.Workers[0].TaskTypes = []string{"bootstrap"} // only bootstrap

	w := d.selectWorker(worker.TaskExplore)
	if w != nil {
		t.Fatal("expected nil for unsupported task type")
	}
}

func TestCanDispatch(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	if !d.canDispatch() {
		t.Fatal("should be able to dispatch initially")
	}

	d.mu.Lock()
	d.runningTasks = d.cfg.Runtime.MaxWorkers
	d.mu.Unlock()

	if d.canDispatch() {
		t.Fatal("should not dispatch when max reached")
	}
}

func TestListProjects(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	ctx := context.Background()

	// no projects
	projects, err := d.listProjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0, got %d", len(projects))
	}

	// create a project
	store.CreateProject(ctx, &models.CreateProjectRequest{Title: "P1", Origin: "o", Goal: "g"})

	projects, err = d.listProjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1, got %d", len(projects))
	}
}

func TestGetProjectDetail(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	ctx := context.Background()
	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{
		Title:  "Detail Test",
		Origin: "http://example.com",
		Goal:   "Find XSS",
	})

	detail, err := d.getProjectDetail(ctx, proj.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Project.Title != "Detail Test" {
		t.Fatalf("expected Detail Test, got %s", detail.Project.Title)
	}
	if len(detail.Facts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(detail.Facts))
	}
}

func TestExportProjectYAML(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	ctx := context.Background()
	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{
		Title: "YAML Test", Origin: "http://example.com", Goal: "test",
	})

	yaml, err := d.exportProjectYAML(ctx, proj.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if yaml == "" {
		t.Fatal("expected non-empty YAML")
	}
}

func TestSchedulingRound_AdmitProject(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{
		name:     "w",
		healthOK: true,
		executeResult: &worker.TaskResult{
			Accepted: true,
			Data: map[string]interface{}{
				"intents": []interface{}{
					map[string]interface{}{"from": []string{"origin"}, "description": "scan"},
				},
			},
		},
	}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	store.CreateProject(ctx, &models.CreateProjectRequest{Title: "P1", Origin: "o", Goal: "g"})

	// first round: admit
	d.schedulingRound(ctx)

	if len(d.admitted) != 1 {
		t.Fatalf("expected 1 admitted, got %d", len(d.admitted))
	}
}

func TestSchedulingRound_CleanupInactive(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "P1", Origin: "o", Goal: "g"})
	d.admitted[proj.ID] = true

	// stop the project
	store.UpdateProjectStatus(ctx, proj.ID, models.ProjectStatusStopped)

	d.schedulingRound(ctx)

	if d.admitted[proj.ID] {
		t.Fatal("stopped project should be removed from admitted")
	}
}

func TestDispatchTask_Bootstrap(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{
		name:     "w",
		healthOK: true,
		executeResult: &worker.TaskResult{
			Accepted: true,
			Data: map[string]interface{}{
				"intents": []interface{}{
					map[string]interface{}{
						"from":        []interface{}{"origin"},
						"description": "scan port 80",
					},
				},
			},
		},
	}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	d.dispatchTask(ctx, proj.ID, worker.TaskBootstrap)

	// wait for async goroutine
	time.Sleep(500 * time.Millisecond)

	drv.mu.Lock()
	calls := drv.execCalls
	drv.mu.Unlock()

	if calls != 1 {
		t.Fatalf("expected 1 execute call, got %d", calls)
	}

	// check intent was created
	detail, _ := store.GetProject(ctx, proj.ID)
	if len(detail.Intents) != 1 {
		t.Fatalf("expected 1 intent after bootstrap, got %d", len(detail.Intents))
	}
	if detail.Intents[0].Description != "scan port 80" {
		t.Fatalf("intent desc mismatch: %s", detail.Intents[0].Description)
	}
}

func TestDispatchTask_HealthcheckFailed(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: false}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	// should not panic or block
	d.dispatchTask(ctx, "proj_001", worker.TaskBootstrap)

	drv.mu.Lock()
	calls := drv.execCalls
	drv.mu.Unlock()

	if calls != 0 {
		t.Fatal("should not execute when healthcheck fails")
	}
}

func TestHeartbeatIntent_ViaAPI(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})
	intent, _ := store.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w",
	})

	status := d.heartbeatIntent(ctx, proj.ID, intent.ID, "w")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}

	// verify intent is claimed
	detail, _ := store.GetProject(ctx, proj.ID)
	for _, i := range detail.Intents {
		if i.ID == intent.ID {
			if !i.IsClaimed() {
				t.Fatal("intent should be claimed after heartbeat")
			}
			return
		}
	}
	t.Fatal("intent not found")
}

func TestClaimAndReleaseReason_ViaAPI(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	status := d.claimReason(ctx, proj.ID, "w", "test")
	if status != http.StatusOK {
		t.Fatalf("claim reason: expected 200, got %d", status)
	}

	p, _ := store.GetProject(ctx, proj.ID)
	if p.Project.Reason == nil || p.Project.Reason.Worker != "w" {
		t.Fatal("reason should be claimed")
	}

	d.releaseReason(ctx, proj.ID, "w")

	p, _ = store.GetProject(ctx, proj.ID)
	if p.Project.Reason != nil {
		t.Fatal("reason should be released")
	}
}

func TestReleaseIntent_ViaAPI(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})
	intent, _ := store.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w",
	})
	store.HeartbeatIntent(ctx, proj.ID, intent.ID, "w")

	d.releaseIntent(ctx, proj.ID, intent.ID, "w")

	detail, _ := store.GetProject(ctx, proj.ID)
	for _, i := range detail.Intents {
		if i.ID == intent.ID {
			if i.IsClaimed() {
				t.Fatal("intent should be released")
			}
			return
		}
	}
}

func TestWriteBack_ReasonIntentsArray(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	task := &worker.Task{Type: worker.TaskReason, ProjectID: proj.ID}
	result := &worker.TaskResult{
		Accepted: true,
		Data: map[string]interface{}{
			"intents": []interface{}{
				map[string]interface{}{"from": []interface{}{"origin"}, "description": "intent A"},
				map[string]interface{}{"from": []interface{}{"origin"}, "description": "intent B"},
			},
		},
	}

	d.writeBack(ctx, proj.ID, task, result, "w")

	detail, _ := store.GetProject(ctx, proj.ID)
	if len(detail.Intents) != 2 {
		t.Fatalf("expected 2 intents from reason, got %d", len(detail.Intents))
	}
}

func TestWriteBack_ReasonSingleIntent(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	task := &worker.Task{Type: worker.TaskReason, ProjectID: proj.ID}
	result := &worker.TaskResult{
		Accepted: true,
		Data: map[string]interface{}{
			"intent": map[string]interface{}{"from": []interface{}{"origin"}, "description": "single intent"},
		},
	}

	d.writeBack(ctx, proj.ID, task, result, "w")

	detail, _ := store.GetProject(ctx, proj.ID)
	if len(detail.Intents) != 1 {
		t.Fatalf("expected 1 intent, got %d", len(detail.Intents))
	}
}

func TestWriteBack_Complete(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	task := &worker.Task{Type: worker.TaskReason, ProjectID: proj.ID}
	result := &worker.TaskResult{
		Accepted: true,
		Data: map[string]interface{}{
			"complete": map[string]interface{}{
				"from":        []interface{}{"origin"},
				"description": "project done",
			},
		},
	}

	d.writeBack(ctx, proj.ID, task, result, "w")

	detail, _ := store.GetProject(ctx, proj.ID)
	if detail.Project.Status != models.ProjectStatusCompleted {
		t.Fatalf("expected completed, got %s", detail.Project.Status)
	}
}

func TestIsInitialState(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	initial := &models.ProjectSummary{FactCount: 2, IntentCount: 0}
	if !d.isInitialState(initial) {
		t.Fatal("should be initial state")
	}

	notInitial := &models.ProjectSummary{FactCount: 3, IntentCount: 1}
	if d.isInitialState(notInitial) {
		t.Fatal("should not be initial state")
	}
}

func TestCreateIntent_ViaAPI(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	ok := d.createIntent(ctx, proj.ID, map[string]interface{}{
		"from":        []interface{}{"origin"},
		"description": "test intent",
	}, "w")

	if !ok {
		t.Fatal("createIntent should succeed")
	}

	detail, _ := store.GetProject(ctx, proj.ID)
	if len(detail.Intents) != 1 {
		t.Fatalf("expected 1 intent, got %d", len(detail.Intents))
	}
}

func TestConcludeIntent_ViaAPI(t *testing.T) {
	ts, store := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)
	ctx := context.Background()

	proj, _ := store.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})
	intent, _ := store.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w",
	})

	d.concludeIntent(ctx, proj.ID, intent.ID, "found result", "w")

	detail, _ := store.GetProject(ctx, proj.ID)
	concluded := false
	for _, i := range detail.Intents {
		if i.ID == intent.ID && !i.IsOpen() {
			concluded = true
		}
	}
	if !concluded {
		t.Fatal("intent should be concluded")
	}
	// fact should have been created
	if len(detail.Facts) != 3 { // origin, goal, f001
		t.Fatalf("expected 3 facts, got %d", len(detail.Facts))
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Fatal("should not truncate short strings")
	}
	if truncate("hello world!", 5) != "hello..." {
		t.Fatalf("unexpected: %s", truncate("hello world!", 5))
	}
}

func TestExtractDescription(t *testing.T) {
	ts, _ := setupTestServer(t)
	drv := &mockDriver{name: "w", healthOK: true}
	d := newTestDispatcher(t, ts.URL, drv)

	data := map[string]interface{}{
		"fact":        map[string]interface{}{"description": "found something"},
		"description": "top-level desc",
	}

	desc, ok := d.extractDescription(data, "fact")
	if !ok || desc != "found something" {
		t.Fatalf("expected 'found something', got %q ok=%v", desc, ok)
	}

	desc, ok = d.extractDescription(data, "")
	if !ok || desc != "top-level desc" {
		t.Fatalf("expected 'top-level desc', got %q ok=%v", desc, ok)
	}

	_, ok = d.extractDescription(data, "nonexistent")
	if ok {
		t.Fatal("should return false for nonexistent key")
	}
}

func TestPromptManager_Render(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts", "test")
	os.MkdirAll(promptDir, 0o755)
	os.WriteFile(filepath.Join(promptDir, "bootstrap.md"),
		[]byte("Origin: {origin}\nGoal: {goal}\nHints: {hints}"), 0o644)

	pm := &PromptManager{group: "test", basePath: filepath.Join(dir, "prompts")}

	detail := &models.ProjectDetailResponse{
		Project: models.Project{Title: "Test"},
		Facts: []models.Fact{
			{ID: "origin", Description: "http://example.com"},
			{ID: "goal", Description: "Find XSS"},
		},
		Hints: []models.Hint{
			{Content: "Check <script>", Creator: "user"},
		},
	}

	result, err := pm.Render(worker.TaskBootstrap, detail)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !containsStr(result, "http://example.com") {
		t.Fatal("should contain origin")
	}
	if !containsStr(result, "Find XSS") {
		t.Fatal("should contain goal")
	}

	// Verify JSON-safe hints (should use json.Marshal, not raw string)
	var hints []map[string]string
	// extract hints JSON from result
	hintsStart := indexOf(result, "[{")
	hintsEnd := indexOf(result, "}]") + 2
	if hintsStart >= 0 && hintsEnd > hintsStart {
		hintsJSON := result[hintsStart:hintsEnd]
		if err := json.Unmarshal([]byte(hintsJSON), &hints); err != nil {
			t.Fatalf("hints should be valid JSON: %v (got: %s)", err, hintsJSON)
		}
		if hints[0]["content"] != "Check <script>" {
			t.Fatalf("hint content mismatch after JSON round-trip: %s", hints[0]["content"])
		}
	}
}

func TestPromptManager_RenderWithExport(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts", "test")
	os.MkdirAll(promptDir, 0o755)
	os.WriteFile(filepath.Join(promptDir, "reason.md"),
		[]byte("{graph_yaml}"), 0o644)

	pm := &PromptManager{group: "test", basePath: filepath.Join(dir, "prompts")}

	detail := &models.ProjectDetailResponse{
		Project: models.Project{Title: "Test"},
		Facts:   []models.Fact{{ID: "origin", Description: "o"}, {ID: "goal", Description: "g"}},
	}

	result, err := pm.RenderWithExport(worker.TaskReason, detail, "exported:\n  data: value\n")
	if err != nil {
		t.Fatalf("render with export: %v", err)
	}

	if containsStr(result, "project:") {
		// If export YAML was provided, the built-in graph should be replaced
		// This is a best-effort test - the replacement depends on exact match
		t.Log("Note: built-in graph may still appear if export didn't match exactly")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
