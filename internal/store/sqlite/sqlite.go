package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ptagent/ptagent/internal/models"
	"github.com/ptagent/ptagent/internal/store"
)

var _ store.Store = (*SQLiteStore)(nil)

// SQLiteStore SQLite 存储实现
type SQLiteStore struct {
	db *sql.DB
}

// New 创建 SQLite store
func New(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite concurrency: limit connections to avoid "database is locked"
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close 关闭数据库连接
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		intent_timeout INTEGER NOT NULL DEFAULT 30,
		reason_timeout INTEGER NOT NULL DEFAULT 30
	);
	INSERT OR IGNORE INTO settings (id, intent_timeout, reason_timeout) VALUES (1, 30, 30);

	CREATE TABLE IF NOT EXISTS projects (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		reason_worker TEXT,
		reason_trigger TEXT,
		reason_started_at DATETIME,
		reason_last_heartbeat_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS facts (
		id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		description TEXT NOT NULL,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		PRIMARY KEY (project_id, id)
	);

	CREATE TABLE IF NOT EXISTS intents (
		id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		from_facts TEXT NOT NULL,
		to_fact TEXT,
		description TEXT NOT NULL,
		creator TEXT NOT NULL,
		worker TEXT,
		last_heartbeat_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		concluded_at DATETIME,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		PRIMARY KEY (project_id, id)
	);

	CREATE TABLE IF NOT EXISTS hints (
		id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		content TEXT NOT NULL,
		creator TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		PRIMARY KEY (project_id, id)
	);

	CREATE INDEX IF NOT EXISTS idx_intents_open ON intents(project_id) WHERE to_fact IS NULL;
	CREATE INDEX IF NOT EXISTS idx_facts_project ON facts(project_id);

	CREATE TABLE IF NOT EXISTS scoped_counters (
		project_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		value INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		PRIMARY KEY (project_id, kind)
	);

	CREATE TABLE IF NOT EXISTS global_counters (
		name TEXT PRIMARY KEY,
		value INTEGER NOT NULL DEFAULT 0
	);
	INSERT OR IGNORE INTO global_counters (name, value) VALUES ('project', 0);

	CREATE TABLE IF NOT EXISTS task_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL,
		task_type TEXT NOT NULL,
		intent_id TEXT NOT NULL DEFAULT '',
		worker TEXT NOT NULL,
		phase TEXT NOT NULL,
		prompt TEXT NOT NULL DEFAULT '',
		output TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_task_events_project ON task_events(project_id, created_at);

	CREATE TABLE IF NOT EXISTS ctfd_instances (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		token TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS ctfd_project_links (
		project_id TEXT PRIMARY KEY,
		ctfd_instance_id TEXT NOT NULL,
		ctfd_challenge_id INTEGER NOT NULL,
		auto_submit INTEGER NOT NULL DEFAULT 1,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		FOREIGN KEY (ctfd_instance_id) REFERENCES ctfd_instances(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS agent_config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		llm_base_url TEXT NOT NULL DEFAULT '',
		llm_api_key TEXT NOT NULL DEFAULT '',
		llm_model TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO agent_config (id, llm_base_url, llm_api_key, llm_model, updated_at) VALUES (1, '', '', '', CURRENT_TIMESTAMP);

	CREATE TABLE IF NOT EXISTS tool_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL,
		task_type TEXT NOT NULL,
		intent_id TEXT NOT NULL DEFAULT '',
		worker TEXT NOT NULL,
		tool TEXT NOT NULL,
		args TEXT NOT NULL DEFAULT '',
		output TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_tool_events_project ON tool_events(project_id, created_at);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteStore) GetSettings(ctx context.Context) (*models.Settings, error) {
	var settings models.Settings
	err := s.db.QueryRowContext(ctx, "SELECT intent_timeout, reason_timeout FROM settings WHERE id = 1").
		Scan(&settings.IntentTimeout, &settings.ReasonTimeout)
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

func (s *SQLiteStore) UpdateSettings(ctx context.Context, settings *models.Settings) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE settings SET intent_timeout = ?, reason_timeout = ? WHERE id = 1",
		settings.IntentTimeout, settings.ReasonTimeout)
	return err
}

func (s *SQLiteStore) ListProjects(ctx context.Context) ([]models.ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.title, p.status, p.created_at,
			p.reason_worker, p.reason_trigger, p.reason_started_at, p.reason_last_heartbeat_at,
			(SELECT COUNT(*) FROM facts WHERE project_id = p.id) as fact_count,
			(SELECT COUNT(*) FROM intents WHERE project_id = p.id) as intent_count,
			(SELECT COUNT(*) FROM intents WHERE project_id = p.id AND to_fact IS NULL AND worker IS NOT NULL) as working_count,
			(SELECT COUNT(*) FROM intents WHERE project_id = p.id AND to_fact IS NULL AND worker IS NULL) as unclaimed_count,
			(SELECT COUNT(*) FROM hints WHERE project_id = p.id) as hint_count
		FROM projects p ORDER BY p.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []models.ProjectSummary
	for rows.Next() {
		var ps models.ProjectSummary
		var rWorker, rTrigger sql.NullString
		var rStarted, rHeartbeat sql.NullTime
		err := rows.Scan(
			&ps.ID, &ps.Title, &ps.Status, &ps.CreatedAt,
			&rWorker, &rTrigger, &rStarted, &rHeartbeat,
			&ps.FactCount, &ps.IntentCount, &ps.WorkingIntentCount,
			&ps.UnclaimedIntentCount, &ps.HintCount,
		)
		if err != nil {
			return nil, err
		}
		if rWorker.Valid {
			ps.Reason = &models.ReasonLease{
				Worker:          rWorker.String,
				Trigger:         rTrigger.String,
				StartedAt:       rStarted.Time,
				LastHeartbeatAt: rHeartbeat.Time,
			}
		}
		projects = append(projects, ps)
	}
	return projects, nil
}

func (s *SQLiteStore) CreateProject(ctx context.Context, req *models.CreateProjectRequest) (*models.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	id := fmt.Sprintf("proj_%03d", s.nextProjectSeq(ctx))
	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx,
		"INSERT INTO projects (id, title, status, created_at) VALUES (?, ?, 'active', ?)",
		id, strings.TrimSpace(req.Title), now)
	if err != nil {
		return nil, err
	}

	// 写入 origin 和 goal 作为特殊 Fact
	_, err = tx.ExecContext(ctx,
		"INSERT INTO facts (id, project_id, description) VALUES ('origin', ?, ?)",
		id, strings.TrimSpace(req.Origin))
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO facts (id, project_id, description) VALUES ('goal', ?, ?)",
		id, strings.TrimSpace(req.Goal))
	if err != nil {
		return nil, err
	}

	// 写入 hints
	for i, h := range req.Hints {
		hintID := fmt.Sprintf("h%03d", i+1)
		_, err = tx.ExecContext(ctx,
			"INSERT INTO hints (id, project_id, content, creator, created_at) VALUES (?, ?, ?, ?, ?)",
			hintID, id, strings.TrimSpace(h.Content), strings.TrimSpace(h.Creator), now)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &models.Project{
		ID:        id,
		Title:     req.Title,
		Status:    models.ProjectStatusActive,
		CreatedAt: now,
	}, nil
}

func (s *SQLiteStore) GetProject(ctx context.Context, id string) (*models.ProjectDetailResponse, error) {
	var p models.Project
	var rWorker, rTrigger sql.NullString
	var rStarted, rHeartbeat sql.NullTime

	err := s.db.QueryRowContext(ctx,
		"SELECT id, title, status, created_at, reason_worker, reason_trigger, reason_started_at, reason_last_heartbeat_at FROM projects WHERE id = ?", id).
		Scan(&p.ID, &p.Title, &p.Status, &p.CreatedAt, &rWorker, &rTrigger, &rStarted, &rHeartbeat)
	if err != nil {
		return nil, err
	}
	if rWorker.Valid {
		p.Reason = &models.ReasonLease{
			Worker:          rWorker.String,
			Trigger:         rTrigger.String,
			StartedAt:       rStarted.Time,
			LastHeartbeatAt: rHeartbeat.Time,
		}
	}

	// Facts
	factRows, err := s.db.QueryContext(ctx, "SELECT id, description FROM facts WHERE project_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer factRows.Close()

	var facts []models.Fact
	for factRows.Next() {
		var f models.Fact
		if err := factRows.Scan(&f.ID, &f.Description); err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}

	// Intents
	intentRows, err := s.db.QueryContext(ctx,
		"SELECT id, from_facts, to_fact, description, creator, worker, last_heartbeat_at, created_at, concluded_at FROM intents WHERE project_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer intentRows.Close()

	var intents []models.Intent
	for intentRows.Next() {
		var i models.Intent
		var fromStr string
		var toFact sql.NullString
		var worker sql.NullString
		var hb sql.NullTime
		var concluded sql.NullTime

		if err := intentRows.Scan(&i.ID, &fromStr, &toFact, &i.Description, &i.Creator, &worker, &hb, &i.CreatedAt, &concluded); err != nil {
			return nil, err
		}
		i.From = strings.Split(fromStr, ",")
		if toFact.Valid {
			i.To = &toFact.String
		}
		if worker.Valid {
			i.Worker = &worker.String
		}
		if hb.Valid {
			i.LastHeartbeatAt = &hb.Time
		}
		if concluded.Valid {
			i.ConcludedAt = &concluded.Time
		}
		intents = append(intents, i)
	}

	// Hints
	hintRows, err := s.db.QueryContext(ctx, "SELECT id, content, creator, created_at FROM hints WHERE project_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer hintRows.Close()

	var hints []models.Hint
	for hintRows.Next() {
		var h models.Hint
		if err := hintRows.Scan(&h.ID, &h.Content, &h.Creator, &h.CreatedAt); err != nil {
			return nil, err
		}
		hints = append(hints, h)
	}

	return &models.ProjectDetailResponse{
		Project: p,
		Facts:   facts,
		Intents: intents,
		Hints:   hints,
	}, nil
}

func (s *SQLiteStore) DeleteProject(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) UpdateProjectStatus(ctx context.Context, id string, status models.ProjectStatus) (*models.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "UPDATE projects SET status = ? WHERE id = ?", status, id)
	if err != nil {
		return nil, err
	}

	// stopped 时清空所有 open intent 的 worker 和 reason lease
	if status == models.ProjectStatusStopped {
		_, err = tx.ExecContext(ctx,
			"UPDATE intents SET worker = NULL WHERE project_id = ? AND to_fact IS NULL", id)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx,
			"UPDATE projects SET reason_worker = NULL, reason_trigger = NULL, reason_started_at = NULL, reason_last_heartbeat_at = NULL WHERE id = ?", id)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return s.getProjectMeta(ctx, id)
}

func (s *SQLiteStore) UpdateProjectTitle(ctx context.Context, id string, title string) (*models.Project, error) {
	_, err := s.db.ExecContext(ctx, "UPDATE projects SET title = ? WHERE id = ?", strings.TrimSpace(title), id)
	if err != nil {
		return nil, err
	}
	return s.getProjectMeta(ctx, id)
}

func (s *SQLiteStore) CreateIntent(ctx context.Context, projectID string, req *models.CreateIntentRequest) (*models.Intent, error) {
	id := fmt.Sprintf("i%03d", s.nextIntentSeq(ctx, projectID))
	now := time.Now().UTC()
	fromStr := strings.Join(req.From, ",")

	var worker sql.NullString
	var hb sql.NullTime
	if req.Worker != nil && *req.Worker != "" {
		worker = sql.NullString{String: *req.Worker, Valid: true}
		hb = sql.NullTime{Time: now, Valid: true}
	}

	_, err := s.db.ExecContext(ctx,
		"INSERT INTO intents (id, project_id, from_facts, description, creator, worker, last_heartbeat_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, projectID, fromStr, strings.TrimSpace(req.Description), strings.TrimSpace(req.Creator), worker, hb, now)
	if err != nil {
		return nil, err
	}

	intent := &models.Intent{
		ID:          id,
		From:        req.From,
		Description: req.Description,
		Creator:     req.Creator,
		CreatedAt:   now,
	}
	if worker.Valid {
		intent.Worker = &worker.String
		intent.LastHeartbeatAt = &hb.Time
	}
	return intent, nil
}

func (s *SQLiteStore) HeartbeatIntent(ctx context.Context, projectID, intentID string, worker string) (*models.Intent, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		"UPDATE intents SET worker = ?, last_heartbeat_at = ? WHERE project_id = ? AND id = ? AND to_fact IS NULL AND (worker IS NULL OR worker = ?)",
		worker, now, projectID, intentID, worker)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("intent heartbeat failed: already claimed by another worker or concluded")
	}
	return s.getIntent(ctx, projectID, intentID)
}

func (s *SQLiteStore) ReleaseIntent(ctx context.Context, projectID, intentID string, worker string) (*models.Intent, error) {
	_, err := s.db.ExecContext(ctx,
		"UPDATE intents SET worker = NULL WHERE project_id = ? AND id = ? AND to_fact IS NULL AND worker = ?",
		projectID, intentID, worker)
	if err != nil {
		return nil, err
	}
	return s.getIntent(ctx, projectID, intentID)
}

func (s *SQLiteStore) ConcludeIntent(ctx context.Context, projectID, intentID string, req *models.ConcludeRequest) (*models.ConcludeResponse, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 在事务内获取序列号，确保并发安全（不会产生重复 ID）
	_, err = tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO scoped_counters (project_id, kind, value) VALUES (?, ?, 0)",
		projectID, "fact")
	if err != nil {
		return nil, fmt.Errorf("init fact counter: %w", err)
	}
	var seq int
	err = tx.QueryRowContext(ctx,
		"UPDATE scoped_counters SET value = value + 1 WHERE project_id = ? AND kind = ? RETURNING value",
		projectID, "fact").Scan(&seq)
	if err != nil {
		return nil, fmt.Errorf("increment fact counter: %w", err)
	}
	factID := fmt.Sprintf("f%03d", seq)

	// 创建新 Fact
	_, err = tx.ExecContext(ctx,
		"INSERT INTO facts (id, project_id, description) VALUES (?, ?, ?)",
		factID, projectID, strings.TrimSpace(req.Description))
	if err != nil {
		return nil, err
	}

	// 更新 Intent
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		"UPDATE intents SET to_fact = ?, worker = ?, concluded_at = ? WHERE project_id = ? AND id = ? AND to_fact IS NULL",
		factID, req.Worker, now, projectID, intentID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	intent, _ := s.getIntent(ctx, projectID, intentID)
	return &models.ConcludeResponse{
		Fact:   models.Fact{ID: factID, Description: req.Description},
		Intent: *intent,
	}, nil
}

func (s *SQLiteStore) CompleteProject(ctx context.Context, projectID string, req *models.CompleteRequest) (*models.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 在事务内获取序列号，确保并发安全
	_, err = tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO scoped_counters (project_id, kind, value) VALUES (?, ?, 0)",
		projectID, "intent")
	if err != nil {
		return nil, fmt.Errorf("init intent counter: %w", err)
	}
	var seq int
	err = tx.QueryRowContext(ctx,
		"UPDATE scoped_counters SET value = value + 1 WHERE project_id = ? AND kind = ? RETURNING value",
		projectID, "intent").Scan(&seq)
	if err != nil {
		return nil, fmt.Errorf("increment intent counter: %w", err)
	}
	intentID := fmt.Sprintf("i%03d", seq)

	// 创建 complete intent (from → goal)
	now := time.Now().UTC()
	fromStr := strings.Join(req.From, ",")

	_, err = tx.ExecContext(ctx,
		"INSERT INTO intents (id, project_id, from_facts, to_fact, description, creator, worker, last_heartbeat_at, created_at, concluded_at) VALUES (?, ?, ?, 'goal', ?, ?, ?, ?, ?, ?)",
		intentID, projectID, fromStr, strings.TrimSpace(req.Description), req.Worker, req.Worker, now, now, now)
	if err != nil {
		return nil, err
	}

	// 更新项目状态为 completed，清空 reason
	_, err = tx.ExecContext(ctx,
		"UPDATE projects SET status = 'completed', reason_worker = NULL, reason_trigger = NULL, reason_started_at = NULL, reason_last_heartbeat_at = NULL WHERE id = ?", projectID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return s.getProjectMeta(ctx, projectID)
}

func (s *SQLiteStore) ReopenProject(ctx context.Context, projectID string, req *models.ReopenRequest) (*models.ReopenResponse, error) {
	// 查找完成边（事务外查询，避免死锁）
	var completeIntentID, completeFrom string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, from_facts FROM intents WHERE project_id = ? AND to_fact = 'goal'", projectID).
		Scan(&completeIntentID, &completeFrom)
	if err != nil {
		return nil, fmt.Errorf("no completion edge found: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 在事务内获取序列号，确保并发安全
	_, err = tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO scoped_counters (project_id, kind, value) VALUES (?, ?, 0)",
		projectID, "fact")
	if err != nil {
		return nil, fmt.Errorf("init fact counter: %w", err)
	}
	var factSeq int
	err = tx.QueryRowContext(ctx,
		"UPDATE scoped_counters SET value = value + 1 WHERE project_id = ? AND kind = ? RETURNING value",
		projectID, "fact").Scan(&factSeq)
	if err != nil {
		return nil, fmt.Errorf("increment fact counter: %w", err)
	}
	factID := fmt.Sprintf("f%03d", factSeq)

	_, err = tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO scoped_counters (project_id, kind, value) VALUES (?, ?, 0)",
		projectID, "intent")
	if err != nil {
		return nil, fmt.Errorf("init intent counter: %w", err)
	}
	var intentSeq int
	err = tx.QueryRowContext(ctx,
		"UPDATE scoped_counters SET value = value + 1 WHERE project_id = ? AND kind = ? RETURNING value",
		projectID, "intent").Scan(&intentSeq)
	if err != nil {
		return nil, fmt.Errorf("increment intent counter: %w", err)
	}
	intentID := fmt.Sprintf("i%03d", intentSeq)

	// 删除完成边
	_, err = tx.ExecContext(ctx, "DELETE FROM intents WHERE project_id = ? AND id = ?", projectID, completeIntentID)
	if err != nil {
		return nil, err
	}

	// 新建纠错 Fact
	_, err = tx.ExecContext(ctx,
		"INSERT INTO facts (id, project_id, description) VALUES (?, ?, ?)",
		factID, projectID, strings.TrimSpace(req.Description))
	if err != nil {
		return nil, err
	}

	// 新建 external_feedback intent
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		"INSERT INTO intents (id, project_id, from_facts, to_fact, description, creator, worker, last_heartbeat_at, created_at, concluded_at) VALUES (?, ?, ?, ?, 'external_feedback', ?, ?, ?, ?, ?)",
		intentID, projectID, completeFrom, factID, req.Creator, req.Creator, now, now, now)
	if err != nil {
		return nil, err
	}

	// 项目改回 active
	_, err = tx.ExecContext(ctx,
		"UPDATE projects SET status = 'active', reason_worker = NULL, reason_trigger = NULL, reason_started_at = NULL, reason_last_heartbeat_at = NULL WHERE id = ?", projectID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	p, _ := s.getProjectMeta(ctx, projectID)
	fromFacts := strings.Split(completeFrom, ",")
	return &models.ReopenResponse{
		Project: *p,
		Fact:    models.Fact{ID: factID, Description: req.Description},
		Intent: models.Intent{
			ID:          intentID,
			From:        fromFacts,
			To:          &factID,
			Description: "external_feedback",
			Creator:     req.Creator,
			Worker:      &req.Creator,
			CreatedAt:   now,
			ConcludedAt: &now,
		},
	}, nil
}

func (s *SQLiteStore) ClaimReason(ctx context.Context, projectID string, req *models.ReasonClaimRequest) (*models.Project, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		"UPDATE projects SET reason_worker = ?, reason_trigger = ?, reason_started_at = ?, reason_last_heartbeat_at = ? WHERE id = ? AND reason_worker IS NULL AND status = 'active'",
		req.Worker, req.Trigger, now, now, projectID)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("reason claim failed: already claimed or project not active")
	}
	return s.getProjectMeta(ctx, projectID)
}

func (s *SQLiteStore) HeartbeatReason(ctx context.Context, projectID string, worker string) (*models.Project, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE projects SET reason_last_heartbeat_at = ? WHERE id = ? AND reason_worker = ?",
		now, projectID, worker)
	if err != nil {
		return nil, err
	}
	return s.getProjectMeta(ctx, projectID)
}

func (s *SQLiteStore) ReleaseReason(ctx context.Context, projectID string, worker string) (*models.Project, error) {
	_, err := s.db.ExecContext(ctx,
		"UPDATE projects SET reason_worker = NULL, reason_trigger = NULL, reason_started_at = NULL, reason_last_heartbeat_at = NULL WHERE id = ? AND reason_worker = ?",
		projectID, worker)
	if err != nil {
		return nil, err
	}
	return s.getProjectMeta(ctx, projectID)
}

func (s *SQLiteStore) CreateHint(ctx context.Context, projectID string, content, creator string) (*models.Hint, error) {
	id := fmt.Sprintf("h%03d", s.nextHintSeq(ctx, projectID))
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO hints (id, project_id, content, creator, created_at) VALUES (?, ?, ?, ?, ?)",
		id, projectID, strings.TrimSpace(content), strings.TrimSpace(creator), now)
	if err != nil {
		return nil, err
	}
	return &models.Hint{ID: id, Content: content, Creator: creator, CreatedAt: now}, nil
}

func (s *SQLiteStore) ExportYAML(ctx context.Context, projectID string) ([]byte, error) {
	detail, err := s.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	sb.WriteString("project:\n")
	sb.WriteString(fmt.Sprintf("  id: %s\n", detail.Project.ID))
	sb.WriteString(fmt.Sprintf("  title: %q\n", detail.Project.Title))
	sb.WriteString(fmt.Sprintf("  status: %s\n", detail.Project.Status))

	// origin and goal
	for _, f := range detail.Facts {
		if f.ID == "origin" {
			sb.WriteString(fmt.Sprintf("  origin: %q\n", f.Description))
		}
		if f.ID == "goal" {
			sb.WriteString(fmt.Sprintf("  goal: %q\n", f.Description))
		}
	}

	// hints
	if len(detail.Hints) > 0 {
		sb.WriteString("\nhints:\n")
		for _, h := range detail.Hints {
			sb.WriteString(fmt.Sprintf("  - content: %q\n", h.Content))
			sb.WriteString(fmt.Sprintf("    creator: %q\n", h.Creator))
		}
	}

	// facts
	sb.WriteString("\nfacts:\n")
	for _, f := range detail.Facts {
		sb.WriteString(fmt.Sprintf("  - id: %s\n", f.ID))
		sb.WriteString(fmt.Sprintf("    description: %q\n", f.Description))
	}

	// intents
	if len(detail.Intents) > 0 {
		sb.WriteString("\nintents:\n")
		for _, i := range detail.Intents {
			sb.WriteString(fmt.Sprintf("  - id: %s\n", i.ID))
			fromParts := make([]string, len(i.From))
			for j, f := range i.From {
				fromParts[j] = f
			}
			sb.WriteString(fmt.Sprintf("    from: [%s]\n", strings.Join(fromParts, ", ")))
			if i.To != nil {
				sb.WriteString(fmt.Sprintf("    to: %s\n", *i.To))
			} else {
				sb.WriteString("    to: null\n")
			}
			sb.WriteString(fmt.Sprintf("    description: %q\n", i.Description))
			sb.WriteString(fmt.Sprintf("    creator: %q\n", i.Creator))
			if i.Worker != nil {
				sb.WriteString(fmt.Sprintf("    worker: %q\n", *i.Worker))
			} else {
				sb.WriteString("    worker: null\n")
			}
		}
	}

	return []byte(sb.String()), nil
}

func (s *SQLiteStore) ExportTimeline(ctx context.Context, projectID string) (string, error) {
	detail, err := s.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Timeline: %s ===\n\n", detail.Project.Title))
	sb.WriteString(fmt.Sprintf("Status: %s\n", detail.Project.Status))
	sb.WriteString(fmt.Sprintf("Created: %s\n\n", detail.Project.CreatedAt.Format(time.RFC3339)))

	// Facts
	sb.WriteString("--- Facts ---\n")
	for _, f := range detail.Facts {
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", f.ID, f.Description))
	}

	// Intents
	sb.WriteString("\n--- Intents ---\n")
	for _, i := range detail.Intents {
		status := "open"
		if i.To != nil {
			status = fmt.Sprintf("concluded -> %s", *i.To)
		} else if i.Worker != nil {
			status = fmt.Sprintf("working (%s)", *i.Worker)
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s  (%s)\n", i.ID, i.Description, status))
		sb.WriteString(fmt.Sprintf("    from: %v  created: %s  by: %s\n", i.From, i.CreatedAt.Format(time.RFC3339), i.Creator))
	}

	// Hints
	if len(detail.Hints) > 0 {
		sb.WriteString("\n--- Hints ---\n")
		for _, h := range detail.Hints {
			sb.WriteString(fmt.Sprintf("  [%s] %s (by %s)\n", h.ID, h.Content, h.Creator))
		}
	}

	return sb.String(), nil
}

func (s *SQLiteStore) CleanupExpiredClaims(ctx context.Context, intentTimeout, reasonTimeout int) error {
	now := time.Now().UTC()

	// 清理超时的 intent worker
	intentDeadline := now.Add(-time.Duration(intentTimeout) * time.Second)
	_, err := s.db.ExecContext(ctx,
		"UPDATE intents SET worker = NULL WHERE to_fact IS NULL AND worker IS NOT NULL AND last_heartbeat_at < ?",
		intentDeadline)
	if err != nil {
		return err
	}

	// 清理超时的 reason lease
	reasonDeadline := now.Add(-time.Duration(reasonTimeout) * time.Second)
	_, err = s.db.ExecContext(ctx,
		"UPDATE projects SET reason_worker = NULL, reason_trigger = NULL, reason_started_at = NULL, reason_last_heartbeat_at = NULL WHERE reason_worker IS NOT NULL AND reason_last_heartbeat_at < ?",
		reasonDeadline)
	return err
}

func (s *SQLiteStore) CountFactTags(ctx context.Context) (map[string]models.FactTagCounts, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id,
			SUM(CASE WHEN description LIKE '[SUCCESS]%' THEN 1 ELSE 0 END),
			SUM(CASE WHEN description LIKE '[FAILURE]%' THEN 1 ELSE 0 END),
			SUM(CASE WHEN description LIKE '[BLOCKER]%' THEN 1 ELSE 0 END)
		FROM facts GROUP BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]models.FactTagCounts)
	for rows.Next() {
		var pid string
		var tc models.FactTagCounts
		if err := rows.Scan(&pid, &tc.SuccessCount, &tc.FailureCount, &tc.BlockerCount); err != nil {
			return nil, err
		}
		result[pid] = tc
	}
	return result, rows.Err()
}

// --- Task Events ---

func (s *SQLiteStore) RecordTaskEvent(ctx context.Context, event *models.TaskEvent) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO task_events (project_id, task_type, intent_id, worker, phase, prompt, output, error, duration_ms, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ProjectID, event.TaskType, event.IntentID, event.Worker,
		event.Phase, event.Prompt, event.Output, event.Error, event.DurationMs, now)
	if err != nil {
		return err
	}
	event.ID, _ = result.LastInsertId()
	event.CreatedAt = now
	return nil
}

func (s *SQLiteStore) ListTaskEvents(ctx context.Context, projectID string, filter *models.TaskEventFilter) ([]models.TaskEvent, error) {
	query := "SELECT id, project_id, task_type, intent_id, worker, phase, prompt, output, error, duration_ms, created_at FROM task_events WHERE project_id = ?"
	args := []interface{}{projectID}

	if filter != nil {
		if filter.TaskType != "" {
			query += " AND task_type = ?"
			args = append(args, filter.TaskType)
		}
		if filter.Worker != "" {
			query += " AND worker = ?"
			args = append(args, filter.Worker)
		}
		if filter.Phase != "" {
			query += " AND phase = ?"
			args = append(args, filter.Phase)
		}
	}

	query += " ORDER BY created_at DESC"

	limit := 100
	offset := 0
	if filter != nil {
		if filter.Limit > 0 && filter.Limit <= 500 {
			limit = filter.Limit
		}
		if filter.Offset > 0 {
			offset = filter.Offset
		}
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.TaskEvent
	for rows.Next() {
		var e models.TaskEvent
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.TaskType, &e.IntentID, &e.Worker, &e.Phase, &e.Prompt, &e.Output, &e.Error, &e.DurationMs, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) GetTaskEvent(ctx context.Context, projectID string, eventID int64) (*models.TaskEvent, error) {
	var e models.TaskEvent
	err := s.db.QueryRowContext(ctx,
		"SELECT id, project_id, task_type, intent_id, worker, phase, prompt, output, error, duration_ms, created_at FROM task_events WHERE project_id = ? AND id = ?",
		projectID, eventID).
		Scan(&e.ID, &e.ProjectID, &e.TaskType, &e.IntentID, &e.Worker, &e.Phase, &e.Prompt, &e.Output, &e.Error, &e.DurationMs, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// --- helpers ---

func (s *SQLiteStore) getProjectMeta(ctx context.Context, id string) (*models.Project, error) {
	var p models.Project
	var rWorker, rTrigger sql.NullString
	var rStarted, rHeartbeat sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, title, status, created_at, reason_worker, reason_trigger, reason_started_at, reason_last_heartbeat_at FROM projects WHERE id = ?", id).
		Scan(&p.ID, &p.Title, &p.Status, &p.CreatedAt, &rWorker, &rTrigger, &rStarted, &rHeartbeat)
	if err != nil {
		return nil, err
	}
	if rWorker.Valid {
		p.Reason = &models.ReasonLease{
			Worker:          rWorker.String,
			Trigger:         rTrigger.String,
			StartedAt:       rStarted.Time,
			LastHeartbeatAt: rHeartbeat.Time,
		}
	}
	return &p, nil
}

func (s *SQLiteStore) getIntent(ctx context.Context, projectID, intentID string) (*models.Intent, error) {
	var i models.Intent
	var fromStr string
	var toFact, worker sql.NullString
	var hb, concluded sql.NullTime

	err := s.db.QueryRowContext(ctx,
		"SELECT id, from_facts, to_fact, description, creator, worker, last_heartbeat_at, created_at, concluded_at FROM intents WHERE project_id = ? AND id = ?",
		projectID, intentID).
		Scan(&i.ID, &fromStr, &toFact, &i.Description, &i.Creator, &worker, &hb, &i.CreatedAt, &concluded)
	if err != nil {
		return nil, err
	}
	i.From = strings.Split(fromStr, ",")
	if toFact.Valid {
		i.To = &toFact.String
	}
	if worker.Valid {
		i.Worker = &worker.String
	}
	if hb.Valid {
		i.LastHeartbeatAt = &hb.Time
	}
	if concluded.Valid {
		i.ConcludedAt = &concluded.Time
	}
	return &i, nil
}

func (s *SQLiteStore) nextFactSeq(ctx context.Context, projectID string) int {
	return s.nextScopedSeq(ctx, projectID, "fact")
}

func (s *SQLiteStore) nextIntentSeq(ctx context.Context, projectID string) int {
	return s.nextScopedSeq(ctx, projectID, "intent")
}

func (s *SQLiteStore) nextHintSeq(ctx context.Context, projectID string) int {
	return s.nextScopedSeq(ctx, projectID, "hint")
}

// nextScopedSeq 原子递增作用域计数器（单条 SQL 保证原子性）
func (s *SQLiteStore) nextScopedSeq(ctx context.Context, projectID, kind string) int {
	s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO scoped_counters (project_id, kind, value) VALUES (?, ?, 0)",
		projectID, kind)
	var val int
	s.db.QueryRowContext(ctx,
		"UPDATE scoped_counters SET value = value + 1 WHERE project_id = ? AND kind = ? RETURNING value",
		projectID, kind).Scan(&val)
	return val
}

// nextProjectSeq 原子递增全局项目计数器（单条 SQL 保证原子性）
func (s *SQLiteStore) nextProjectSeq(ctx context.Context) int {
	var val int
	s.db.QueryRowContext(ctx,
		"UPDATE global_counters SET value = value + 1 WHERE name = 'project' RETURNING value").Scan(&val)
	return val
}

func generateShortID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
}

// --- CTFd Instances ---

func (s *SQLiteStore) ListCTFdInstances(ctx context.Context) ([]models.CTFdInstance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, url, created_at FROM ctfd_instances ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var instances []models.CTFdInstance
	for rows.Next() {
		var inst models.CTFdInstance
		if err := rows.Scan(&inst.ID, &inst.Name, &inst.URL, &inst.CreatedAt); err != nil {
			return nil, err
		}
		instances = append(instances, inst)
	}
	return instances, rows.Err()
}

func (s *SQLiteStore) GetCTFdInstance(ctx context.Context, id string) (*models.CTFdInstance, error) {
	var inst models.CTFdInstance
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, url, token, created_at FROM ctfd_instances WHERE id = ?", id).
		Scan(&inst.ID, &inst.Name, &inst.URL, &inst.Token, &inst.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *SQLiteStore) AddCTFdInstance(ctx context.Context, req *models.AddCTFdInstanceRequest) (*models.CTFdInstance, error) {
	id := generateShortID()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO ctfd_instances (id, name, url, token, created_at) VALUES (?, ?, ?, ?, ?)",
		id, req.Name, strings.TrimRight(req.URL, "/"), req.Token, now)
	if err != nil {
		return nil, err
	}
	return &models.CTFdInstance{ID: id, Name: req.Name, URL: req.URL, CreatedAt: now}, nil
}

func (s *SQLiteStore) DeleteCTFdInstance(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM ctfd_instances WHERE id = ?", id)
	return err
}

// --- CTFd Project Links ---

func (s *SQLiteStore) LinkProjectCTFd(ctx context.Context, link *models.CTFdProjectLink) error {
	autoSubmit := 0
	if link.AutoSubmit {
		autoSubmit = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO ctfd_project_links (project_id, ctfd_instance_id, ctfd_challenge_id, auto_submit)
		 VALUES (?, ?, ?, ?)`,
		link.ProjectID, link.CTFdInstanceID, link.CTFdChallengeID, autoSubmit)
	return err
}

func (s *SQLiteStore) GetProjectCTFdLink(ctx context.Context, projectID string) (*models.CTFdProjectLink, error) {
	var link models.CTFdProjectLink
	var autoSubmit int
	err := s.db.QueryRowContext(ctx,
		"SELECT project_id, ctfd_instance_id, ctfd_challenge_id, auto_submit FROM ctfd_project_links WHERE project_id = ?",
		projectID).Scan(&link.ProjectID, &link.CTFdInstanceID, &link.CTFdChallengeID, &autoSubmit)
	if err != nil {
		return nil, err
	}
	link.AutoSubmit = autoSubmit == 1
	return &link, nil
}

func (s *SQLiteStore) SetProjectAutoSubmit(ctx context.Context, projectID string, autoSubmit bool) error {
	v := 0
	if autoSubmit {
		v = 1
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE ctfd_project_links SET auto_submit = ? WHERE project_id = ?", v, projectID)
	return err
}

// --- Agent Config ---

func (s *SQLiteStore) GetAgentConfig(ctx context.Context) (*models.AgentConfig, error) {
	var cfg models.AgentConfig
	err := s.db.QueryRowContext(ctx,
		"SELECT llm_base_url, llm_api_key, llm_model FROM agent_config WHERE id = 1").
		Scan(&cfg.LLMBaseURL, &cfg.LLMAPIKey, &cfg.LLMModel)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *SQLiteStore) UpdateAgentConfig(ctx context.Context, cfg *models.AgentConfig) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE agent_config SET llm_base_url = ?, llm_api_key = ?, llm_model = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1",
		cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	return err
}

// --- Tool Events ---

func (s *SQLiteStore) RecordToolEvent(ctx context.Context, event *models.ToolEvent) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_events (project_id, task_type, intent_id, worker, tool, args, output, error, duration_ms, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ProjectID, event.TaskType, event.IntentID, event.Worker, event.Tool, event.Args, event.Output, event.Error, event.DurationMs, now)
	if err != nil {
		return err
	}
	event.ID, _ = result.LastInsertId()
	event.CreatedAt = now
	return nil
}

func (s *SQLiteStore) ListToolEvents(ctx context.Context, projectID string, filter *models.ToolEventFilter) ([]models.ToolEvent, error) {
	query := "SELECT id, project_id, task_type, intent_id, worker, tool, args, output, error, duration_ms, created_at FROM tool_events WHERE project_id = ?"
	args := []interface{}{projectID}

	if filter != nil {
		if filter.Tool != "" {
			query += " AND tool = ?"
			args = append(args, filter.Tool)
		}
	}

	query += " ORDER BY created_at DESC"

	limit := 100
	offset := 0
	if filter != nil {
		if filter.Limit > 0 && filter.Limit <= 500 {
			limit = filter.Limit
		}
		if filter.Offset > 0 {
			offset = filter.Offset
		}
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.ToolEvent
	for rows.Next() {
		var e models.ToolEvent
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.TaskType, &e.IntentID, &e.Worker, &e.Tool, &e.Args, &e.Output, &e.Error, &e.DurationMs, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
