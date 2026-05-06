package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ptagent/ptagent/internal/models"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// default settings
	settings, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.IntentTimeout != 30 || settings.ReasonTimeout != 30 {
		t.Fatalf("expected defaults 30/30, got %d/%d", settings.IntentTimeout, settings.ReasonTimeout)
	}

	// update
	err = s.UpdateSettings(ctx, &models.Settings{IntentTimeout: 60, ReasonTimeout: 120})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}
	settings, _ = s.GetSettings(ctx)
	if settings.IntentTimeout != 60 || settings.ReasonTimeout != 120 {
		t.Fatalf("expected 60/120, got %d/%d", settings.IntentTimeout, settings.ReasonTimeout)
	}
}

func TestCreateAndGetProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, err := s.CreateProject(ctx, &models.CreateProjectRequest{
		Title:  "Test Project",
		Origin: "http://target.example.com",
		Goal:   "Find SQL injection",
		Hints:  []models.CreateHintParams{{Content: "Try admin panel", Creator: "user"}},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if proj.ID != "proj_001" {
		t.Fatalf("expected proj_001, got %s", proj.ID)
	}
	if proj.Title != "Test Project" {
		t.Fatalf("expected 'Test Project', got %s", proj.Title)
	}
	if proj.Status != models.ProjectStatusActive {
		t.Fatalf("expected active, got %s", proj.Status)
	}

	// get detail
	detail, err := s.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if len(detail.Facts) != 2 {
		t.Fatalf("expected 2 facts (origin, goal), got %d", len(detail.Facts))
	}
	if len(detail.Hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(detail.Hints))
	}
	if detail.Hints[0].Content != "Try admin panel" {
		t.Fatalf("hint content mismatch: %s", detail.Hints[0].Content)
	}
}

func TestProjectSequentialIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "P1", Origin: "o1", Goal: "g1"})
	p2, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "P2", Origin: "o2", Goal: "g2"})

	if p1.ID != "proj_001" || p2.ID != "proj_002" {
		t.Fatalf("expected proj_001, proj_002, got %s, %s", p1.ID, p2.ID)
	}
}

func TestListProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _ = s.CreateProject(ctx, &models.CreateProjectRequest{Title: "A", Origin: "o", Goal: "g"})
	_, _ = s.CreateProject(ctx, &models.CreateProjectRequest{Title: "B", Origin: "o", Goal: "g"})

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2, got %d", len(projects))
	}
	// each project has 2 facts (origin, goal)
	if projects[0].FactCount != 2 {
		t.Fatalf("expected 2 facts, got %d", projects[0].FactCount)
	}
}

func TestDeleteProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "Del", Origin: "o", Goal: "g"})
	err := s.DeleteProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = s.GetProject(ctx, proj.ID)
	if err == nil {
		t.Fatal("expected error getting deleted project")
	}
}

func TestUpdateProjectTitleAndStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "Old", Origin: "o", Goal: "g"})

	updated, err := s.UpdateProjectTitle(ctx, proj.ID, "New Title")
	if err != nil {
		t.Fatalf("update title: %v", err)
	}
	if updated.Title != "New Title" {
		t.Fatalf("expected 'New Title', got %s", updated.Title)
	}

	stopped, err := s.UpdateProjectStatus(ctx, proj.ID, models.ProjectStatusStopped)
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if stopped.Status != models.ProjectStatusStopped {
		t.Fatalf("expected stopped, got %s", stopped.Status)
	}
}

func TestIntentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	// create intent
	intent, err := s.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From:        []string{"origin"},
		Description: "Scan port 80",
		Creator:     "worker-1",
	})
	if err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if intent.ID != "i001" {
		t.Fatalf("expected i001, got %s", intent.ID)
	}
	if !intent.IsOpen() {
		t.Fatal("intent should be open")
	}
	if intent.IsClaimed() {
		t.Fatal("intent should not be claimed")
	}

	// heartbeat (claim)
	claimed, err := s.HeartbeatIntent(ctx, proj.ID, intent.ID, "worker-1")
	if err != nil {
		t.Fatalf("heartbeat intent: %v", err)
	}
	if !claimed.IsClaimed() {
		t.Fatal("intent should be claimed after heartbeat")
	}

	// release
	released, err := s.ReleaseIntent(ctx, proj.ID, intent.ID, "worker-1")
	if err != nil {
		t.Fatalf("release intent: %v", err)
	}
	if released.IsClaimed() {
		t.Fatal("intent should not be claimed after release")
	}

	// conclude
	resp, err := s.ConcludeIntent(ctx, proj.ID, intent.ID, &models.ConcludeRequest{
		Worker:      "worker-1",
		Description: "Port 80 is open, running Apache 2.4",
	})
	if err != nil {
		t.Fatalf("conclude intent: %v", err)
	}
	if resp.Fact.ID != "f001" {
		t.Fatalf("expected fact f001, got %s", resp.Fact.ID)
	}
	if resp.Intent.IsOpen() {
		t.Fatal("intent should be closed after conclude")
	}
}

func TestIntentSequentialIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	i1, _ := s.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{From: []string{"origin"}, Description: "d1", Creator: "w"})
	i2, _ := s.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{From: []string{"origin"}, Description: "d2", Creator: "w"})

	if i1.ID != "i001" || i2.ID != "i002" {
		t.Fatalf("expected i001, i002, got %s, %s", i1.ID, i2.ID)
	}
}

func TestReasonLease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	// claim
	p, err := s.ClaimReason(ctx, proj.ID, &models.ReasonClaimRequest{Worker: "w1", Trigger: "explore_done"})
	if err != nil {
		t.Fatalf("claim reason: %v", err)
	}
	if p.Reason == nil || p.Reason.Worker != "w1" {
		t.Fatal("reason should be claimed by w1")
	}

	// heartbeat
	p, err = s.HeartbeatReason(ctx, proj.ID, "w1")
	if err != nil {
		t.Fatalf("heartbeat reason: %v", err)
	}
	if p.Reason == nil {
		t.Fatal("reason should still be active")
	}

	// release
	p, err = s.ReleaseReason(ctx, proj.ID, "w1")
	if err != nil {
		t.Fatalf("release reason: %v", err)
	}
	if p.Reason != nil {
		t.Fatal("reason should be nil after release")
	}
}

func TestCompleteAndReopen(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	// create fact via conclude
	intent, _ := s.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w1",
	})
	s.ConcludeIntent(ctx, proj.ID, intent.ID, &models.ConcludeRequest{
		Worker: "w1", Description: "found vuln",
	})

	// complete
	p, err := s.CompleteProject(ctx, proj.ID, &models.CompleteRequest{
		From: []string{"f001"}, Description: "mission done", Worker: "w1",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if p.Status != models.ProjectStatusCompleted {
		t.Fatalf("expected completed, got %s", p.Status)
	}

	// reopen
	resp, err := s.ReopenProject(ctx, proj.ID, &models.ReopenRequest{
		Description: "new info found", Creator: "user",
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if resp.Project.Status != models.ProjectStatusActive {
		t.Fatalf("expected active after reopen, got %s", resp.Project.Status)
	}
}

func TestCreateHint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	h, err := s.CreateHint(ctx, proj.ID, "Try SQLi on login page", "user")
	if err != nil {
		t.Fatalf("create hint: %v", err)
	}
	if h.ID != "h001" {
		t.Fatalf("expected h001, got %s", h.ID)
	}
	if h.Content != "Try SQLi on login page" {
		t.Fatalf("content mismatch: %s", h.Content)
	}
}

func TestExportYAML(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{
		Title:  "Export Test",
		Origin: "http://example.com",
		Goal:   "Find XSS",
		Hints:  []models.CreateHintParams{{Content: "Check forms", Creator: "user"}},
	})

	data, err := s.ExportYAML(ctx, proj.ID)
	if err != nil {
		t.Fatalf("export yaml: %v", err)
	}

	yaml := string(data)
	if !contains(yaml, "Export Test") {
		t.Fatal("YAML should contain project title")
	}
	if !contains(yaml, "http://example.com") {
		t.Fatal("YAML should contain origin")
	}
	if !contains(yaml, "Find XSS") {
		t.Fatal("YAML should contain goal")
	}
	if !contains(yaml, "Check forms") {
		t.Fatal("YAML should contain hint")
	}
}

func TestExportTimeline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "Timeline Test", Origin: "o", Goal: "g"})

	timeline, err := s.ExportTimeline(ctx, proj.ID)
	if err != nil {
		t.Fatalf("export timeline: %v", err)
	}
	if !contains(timeline, "Timeline Test") {
		t.Fatal("timeline should contain project title")
	}
	if !contains(timeline, "active") {
		t.Fatal("timeline should contain status")
	}
}

func TestCleanupExpiredClaims(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	// create and claim intent
	intent, _ := s.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w1",
	})
	s.HeartbeatIntent(ctx, proj.ID, intent.ID, "w1")

	// manually backdate the heartbeat to ensure expiration
	s.db.ExecContext(ctx,
		"UPDATE intents SET last_heartbeat_at = datetime('now', '-60 seconds') WHERE project_id = ? AND id = ?",
		proj.ID, intent.ID)

	// cleanup with 30s timeout → heartbeat is 60s old → should expire
	err := s.CleanupExpiredClaims(ctx, 30, 30)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// check intent is released
	detail, _ := s.GetProject(ctx, proj.ID)
	for _, i := range detail.Intents {
		if i.ID == intent.ID && i.IsClaimed() {
			t.Fatal("intent should be released after cleanup")
		}
	}
}

func TestStopProjectReleasesWorkers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj, _ := s.CreateProject(ctx, &models.CreateProjectRequest{Title: "T", Origin: "o", Goal: "g"})

	// create and claim intent
	intent, _ := s.CreateIntent(ctx, proj.ID, &models.CreateIntentRequest{
		From: []string{"origin"}, Description: "scan", Creator: "w1",
	})
	s.HeartbeatIntent(ctx, proj.ID, intent.ID, "w1")

	// claim reason
	s.ClaimReason(ctx, proj.ID, &models.ReasonClaimRequest{Worker: "w1", Trigger: "test"})

	// stop project
	p, err := s.UpdateProjectStatus(ctx, proj.ID, models.ProjectStatusStopped)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if p.Reason != nil {
		t.Fatal("reason should be cleared on stop")
	}

	// check intent worker is cleared
	detail, _ := s.GetProject(ctx, proj.ID)
	for _, i := range detail.Intents {
		if i.ID == intent.ID && i.IsClaimed() {
			t.Fatal("intent should be released on stop")
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
