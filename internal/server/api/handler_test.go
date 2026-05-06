package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/store/sqlite"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupRouter(t *testing.T) *gin.Engine {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	r := gin.New()
	h := NewHandler(store)
	h.RegisterRoutes(r)
	return r
}

func doRequest(r *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func createTestProject(t *testing.T, r *gin.Engine) string {
	t.Helper()
	w := doRequest(r, "POST", "/api/projects", models.CreateProjectRequest{
		Title:  "Test",
		Origin: "http://example.com",
		Goal:   "Find vulns",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: status %d, body: %s", w.Code, w.Body.String())
	}
	var proj models.Project
	json.Unmarshal(w.Body.Bytes(), &proj)
	return proj.ID
}

// --- Settings ---

func TestAPI_GetSettings(t *testing.T) {
	r := setupRouter(t)
	w := doRequest(r, "GET", "/api/settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var s models.Settings
	json.Unmarshal(w.Body.Bytes(), &s)
	if s.IntentTimeout != 30 {
		t.Fatalf("expected 30, got %d", s.IntentTimeout)
	}
}

func TestAPI_UpdateSettings(t *testing.T) {
	r := setupRouter(t)
	w := doRequest(r, "PUT", "/api/settings", models.Settings{IntentTimeout: 60, ReasonTimeout: 90})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var s models.Settings
	json.Unmarshal(w.Body.Bytes(), &s)
	if s.IntentTimeout != 60 || s.ReasonTimeout != 90 {
		t.Fatalf("expected 60/90, got %d/%d", s.IntentTimeout, s.ReasonTimeout)
	}
}

// --- Projects ---

func TestAPI_CreateAndListProjects(t *testing.T) {
	r := setupRouter(t)

	// create
	w := doRequest(r, "POST", "/api/projects", models.CreateProjectRequest{
		Title:  "Test Project",
		Origin: "http://target.com",
		Goal:   "SQLi",
		Hints:  []models.CreateHintParams{{Content: "hint1", Creator: "user"}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var proj models.Project
	json.Unmarshal(w.Body.Bytes(), &proj)
	if proj.ID == "" {
		t.Fatal("project ID should not be empty")
	}

	// list
	w = doRequest(r, "GET", "/api/projects", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var projects []models.ProjectSummary
	json.Unmarshal(w.Body.Bytes(), &projects)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].FactCount != 2 {
		t.Fatalf("expected 2 facts, got %d", projects[0].FactCount)
	}
	if projects[0].HintCount != 1 {
		t.Fatalf("expected 1 hint, got %d", projects[0].HintCount)
	}
}

func TestAPI_GetProject(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "GET", "/api/projects/"+id, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var detail models.ProjectDetailResponse
	json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.Project.ID != id {
		t.Fatalf("expected %s, got %s", id, detail.Project.ID)
	}
	if len(detail.Facts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(detail.Facts))
	}
}

func TestAPI_GetProject_NotFound(t *testing.T) {
	r := setupRouter(t)
	w := doRequest(r, "GET", "/api/projects/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAPI_DeleteProject(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "DELETE", "/api/projects/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	w = doRequest(r, "GET", "/api/projects/"+id, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestAPI_UpdateTitle(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "PUT", "/api/projects/"+id+"/title", models.TitleUpdateRequest{Title: "New Name"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var p models.Project
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.Title != "New Name" {
		t.Fatalf("expected 'New Name', got %s", p.Title)
	}
}

func TestAPI_UpdateStatus(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "PUT", "/api/projects/"+id+"/status", models.StatusUpdateRequest{Status: "stopped"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var p models.Project
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.Status != models.ProjectStatusStopped {
		t.Fatalf("expected stopped, got %s", p.Status)
	}
}

func TestAPI_UpdateStatus_InvalidStatus(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "PUT", "/api/projects/"+id+"/status", models.StatusUpdateRequest{Status: "completed"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for completed status, got %d", w.Code)
	}
}

// --- Intents ---

func TestAPI_IntentLifecycle(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	// create intent
	w := doRequest(r, "POST", "/api/projects/"+id+"/intents", models.CreateIntentRequest{
		From:        []string{"origin"},
		Description: "Scan ports",
		Creator:     "worker1",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var intent models.Intent
	json.Unmarshal(w.Body.Bytes(), &intent)
	if intent.ID == "" {
		t.Fatal("intent ID should not be empty")
	}

	// heartbeat (claim)
	w = doRequest(r, "POST", "/api/projects/"+id+"/intents/"+intent.ID+"/heartbeat",
		models.HeartbeatRequest{Worker: "worker1"})
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat: expected 200, got %d", w.Code)
	}

	// release
	w = doRequest(r, "POST", "/api/projects/"+id+"/intents/"+intent.ID+"/release",
		models.HeartbeatRequest{Worker: "worker1"})
	if w.Code != http.StatusOK {
		t.Fatalf("release: expected 200, got %d", w.Code)
	}

	// conclude
	w = doRequest(r, "POST", "/api/projects/"+id+"/intents/"+intent.ID+"/conclude",
		models.ConcludeRequest{Worker: "worker1", Description: "Ports 80,443 open"})
	if w.Code != http.StatusOK {
		t.Fatalf("conclude: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.ConcludeResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Fact.ID == "" {
		t.Fatal("conclude should produce a fact")
	}
}

func TestAPI_CreateIntent_BadRequest(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	// missing required field
	w := doRequest(r, "POST", "/api/projects/"+id+"/intents", map[string]string{
		"description": "no from field",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Reason lease ---

func TestAPI_ReasonLease(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	// claim
	w := doRequest(r, "POST", "/api/projects/"+id+"/reason/claim",
		models.ReasonClaimRequest{Worker: "w1", Trigger: "explore_done"})
	if w.Code != http.StatusOK {
		t.Fatalf("claim: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// heartbeat
	w = doRequest(r, "POST", "/api/projects/"+id+"/reason/heartbeat",
		models.HeartbeatRequest{Worker: "w1"})
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat: expected 200, got %d", w.Code)
	}

	// release
	w = doRequest(r, "POST", "/api/projects/"+id+"/reason/release",
		models.HeartbeatRequest{Worker: "w1"})
	if w.Code != http.StatusOK {
		t.Fatalf("release: expected 200, got %d", w.Code)
	}
}

// --- Complete / Reopen ---

func TestAPI_CompleteAndReopen(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	// create and conclude an intent first
	w := doRequest(r, "POST", "/api/projects/"+id+"/intents", models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w1",
	})
	var intent models.Intent
	json.Unmarshal(w.Body.Bytes(), &intent)

	doRequest(r, "POST", "/api/projects/"+id+"/intents/"+intent.ID+"/conclude",
		models.ConcludeRequest{Worker: "w1", Description: "found sqli"})

	// complete
	w = doRequest(r, "POST", "/api/projects/"+id+"/complete",
		models.CompleteRequest{From: []string{"f001"}, Description: "done", Worker: "w1"})
	if w.Code != http.StatusOK {
		t.Fatalf("complete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var p models.Project
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.Status != models.ProjectStatusCompleted {
		t.Fatalf("expected completed, got %s", p.Status)
	}

	// reopen
	w = doRequest(r, "POST", "/api/projects/"+id+"/reopen",
		models.ReopenRequest{Description: "missed something", Creator: "user"})
	if w.Code != http.StatusOK {
		t.Fatalf("reopen: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.ReopenResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Project.Status != models.ProjectStatusActive {
		t.Fatalf("expected active, got %s", resp.Project.Status)
	}
}

// --- Hints ---

func TestAPI_CreateHint(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "POST", "/api/projects/"+id+"/hints",
		models.CreateHintParams{Content: "Check admin panel", Creator: "user"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var h models.Hint
	json.Unmarshal(w.Body.Bytes(), &h)
	if h.Content != "Check admin panel" {
		t.Fatalf("content mismatch: %s", h.Content)
	}
}

// --- Export ---

func TestAPI_ExportYAML(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "GET", "/api/projects/"+id+"/export?format=yaml", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if len(body) == 0 {
		t.Fatal("expected non-empty YAML")
	}
	if w.Header().Get("Content-Type") != "text/yaml" {
		t.Fatalf("expected text/yaml, got %s", w.Header().Get("Content-Type"))
	}
}

func TestAPI_ExportTimeline(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "GET", "/api/projects/"+id+"/export?format=timeline", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_ExportBadFormat(t *testing.T) {
	r := setupRouter(t)
	id := createTestProject(t, r)

	w := doRequest(r, "GET", "/api/projects/"+id+"/export?format=csv", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
