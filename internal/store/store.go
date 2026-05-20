package store

import (
	"context"

	"github.com/ptagent/ptagent/internal/models"
)

// Store 存储层接口 — 支持 SQLite 和 Postgres 实现
type Store interface {
	// Settings
	GetSettings(ctx context.Context) (*models.Settings, error)
	UpdateSettings(ctx context.Context, s *models.Settings) error

	// Projects
	ListProjects(ctx context.Context) ([]models.ProjectSummary, error)
	GetProject(ctx context.Context, id string) (*models.ProjectDetailResponse, error)
	CreateProject(ctx context.Context, req *models.CreateProjectRequest) (*models.Project, error)
	DeleteProject(ctx context.Context, id string) error
	UpdateProjectStatus(ctx context.Context, id string, status models.ProjectStatus) (*models.Project, error)
	UpdateProjectTitle(ctx context.Context, id string, title string) (*models.Project, error)

	// Reason lease
	ClaimReason(ctx context.Context, projectID string, req *models.ReasonClaimRequest) (*models.Project, error)
	HeartbeatReason(ctx context.Context, projectID string, worker string) (*models.Project, error)
	ReleaseReason(ctx context.Context, projectID string, worker string) (*models.Project, error)

	// Facts (read via GetProject)

	// Intents
	CreateIntent(ctx context.Context, projectID string, req *models.CreateIntentRequest) (*models.Intent, error)
	HeartbeatIntent(ctx context.Context, projectID, intentID string, worker string) (*models.Intent, error)
	ReleaseIntent(ctx context.Context, projectID, intentID string, worker string) (*models.Intent, error)
	ConcludeIntent(ctx context.Context, projectID, intentID string, req *models.ConcludeRequest) (*models.ConcludeResponse, error)

	// Complete / Reopen
	CompleteProject(ctx context.Context, projectID string, req *models.CompleteRequest) (*models.Project, error)
	ReopenProject(ctx context.Context, projectID string, req *models.ReopenRequest) (*models.ReopenResponse, error)

	// Hints
	CreateHint(ctx context.Context, projectID string, content, creator string) (*models.Hint, error)

	// Export
	ExportYAML(ctx context.Context, projectID string) ([]byte, error)
	ExportTimeline(ctx context.Context, projectID string) (string, error)

	// Task Events (replay)
	RecordTaskEvent(ctx context.Context, event *models.TaskEvent) error
	ListTaskEvents(ctx context.Context, projectID string, filter *models.TaskEventFilter) ([]models.TaskEvent, error)
	GetTaskEvent(ctx context.Context, projectID string, eventID int64) (*models.TaskEvent, error)

	// Timeout cleanup
	CleanupExpiredClaims(ctx context.Context, intentTimeout, reasonTimeout int) error

	// Metrics
	CountFactTags(ctx context.Context) (map[string]models.FactTagCounts, error)

	// CTFd Instances
	ListCTFdInstances(ctx context.Context) ([]models.CTFdInstance, error)
	GetCTFdInstance(ctx context.Context, id string) (*models.CTFdInstance, error)
	AddCTFdInstance(ctx context.Context, req *models.AddCTFdInstanceRequest) (*models.CTFdInstance, error)
	DeleteCTFdInstance(ctx context.Context, id string) error

	// CTFd Project Links
	LinkProjectCTFd(ctx context.Context, link *models.CTFdProjectLink) error
	GetProjectCTFdLink(ctx context.Context, projectID string) (*models.CTFdProjectLink, error)
	SetProjectAutoSubmit(ctx context.Context, projectID string, autoSubmit bool) error
}
