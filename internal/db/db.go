package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	conn.SetMaxOpenConns(1)
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repo_owner TEXT NOT NULL,
		repo_name TEXT NOT NULL,
		issue_number INTEGER NOT NULL,
		pr_number INTEGER,
		branch TEXT,
		worktree_path TEXT,
		job_type TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'queued',
		current_phase TEXT,
		attempt INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 3,
		next_retry_at DATETIME,
		pid INTEGER,
		heartbeat_at DATETIME,
		last_log_line TEXT,
		last_error TEXT,
		model TEXT,
		trigger_comment_id TEXT,
		created_at DATETIME NOT NULL DEFAULT (datetime('now', 'localtime')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now', 'localtime')),
		started_at DATETIME,
		finished_at DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state);
	CREATE INDEX IF NOT EXISTS idx_jobs_issue_number ON jobs(issue_number);
	CREATE INDEX IF NOT EXISTS idx_jobs_pr_number ON jobs(pr_number);
	CREATE INDEX IF NOT EXISTS idx_jobs_branch ON jobs(branch);
	CREATE INDEX IF NOT EXISTS idx_jobs_next_retry ON jobs(next_retry_at);

	CREATE TABLE IF NOT EXISTS processed_comments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repo_owner TEXT NOT NULL,
		repo_name TEXT NOT NULL,
		pr_number INTEGER NOT NULL,
		comment_id TEXT NOT NULL,
		sender_login TEXT NOT NULL,
		processed_at DATETIME NOT NULL DEFAULT (datetime('now', 'localtime')),
		UNIQUE(repo_owner, repo_name, comment_id)
	);

	CREATE INDEX IF NOT EXISTS idx_processed_comments_comment ON processed_comments(repo_owner, repo_name, comment_id);

	CREATE TABLE IF NOT EXISTS poll_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT (datetime('now', 'localtime'))
	);

	CREATE TABLE IF NOT EXISTS job_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER NOT NULL,
		attempt INTEGER NOT NULL,
		state TEXT,
		log_path TEXT,
		started_at DATETIME,
		finished_at DATETIME,
		exit_code INTEGER,
		error TEXT,
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	);

	CREATE TABLE IF NOT EXISTS agents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER NOT NULL,
		pid INTEGER,
		state TEXT,
		started_at DATETIME NOT NULL DEFAULT (datetime('now', 'localtime')),
		heartbeat_at DATETIME,
		model TEXT,
		worktree_path TEXT,
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	);

	CREATE TABLE IF NOT EXISTS state_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER NOT NULL,
		from_state TEXT,
		to_state TEXT NOT NULL,
		message TEXT,
		created_at DATETIME NOT NULL DEFAULT (datetime('now', 'localtime')),
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	);

	CREATE INDEX IF NOT EXISTS idx_state_logs_job ON state_logs(job_id);
	`

	_, err := d.conn.Exec(schema)
	return err
}

type Job struct {
	ID               int64          `json:"id"`
	RepoOwner        string         `json:"repo_owner"`
	RepoName         string         `json:"repo_name"`
	IssueNumber      int            `json:"issue_number"`
	PRNumber         *int           `json:"pr_number,omitempty"`
	Branch           *string        `json:"branch,omitempty"`
	WorktreePath     *string        `json:"worktree_path,omitempty"`
	JobType          string         `json:"job_type"`
	State            string         `json:"state"`
	CurrentPhase     *string        `json:"current_phase,omitempty"`
	Attempt          int            `json:"attempt"`
	MaxAttempts      int            `json:"max_attempts"`
	NextRetryAt      *time.Time     `json:"next_retry_at,omitempty"`
	PID              *int           `json:"pid,omitempty"`
	HeartbeatAt      *time.Time     `json:"heartbeat_at,omitempty"`
	LastLogLine      *string        `json:"last_log_line,omitempty"`
	LastError        *string        `json:"last_error,omitempty"`
	Model            *string        `json:"model,omitempty"`
	TriggerCommentID *string        `json:"trigger_comment_id,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
}

type CreateJobParams struct {
	RepoOwner        string
	RepoName         string
	IssueNumber      int
	JobType          string
	Branch           string
	MaxAttempts      int
	Model            string
	TriggerCommentID string
}

func (d *DB) CreateJob(ctx context.Context, p CreateJobParams) (*Job, error) {
	row := d.conn.QueryRowContext(ctx, `
		INSERT INTO jobs (repo_owner, repo_name, issue_number, job_type, branch, max_attempts, model, trigger_comment_id, attempt, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 'queued')
		RETURNING id, repo_owner, repo_name, issue_number, job_type, branch, state, attempt, max_attempts, model, trigger_comment_id, created_at, updated_at
	`, p.RepoOwner, p.RepoName, p.IssueNumber, p.JobType, p.Branch, p.MaxAttempts, p.Model, p.TriggerCommentID)

	var j Job
	var branch, model, trigComment sql.NullString
	err := row.Scan(&j.ID, &j.RepoOwner, &j.RepoName, &j.IssueNumber, &j.JobType, &branch, &j.State, &j.Attempt, &j.MaxAttempts, &model, &trigComment, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}
	if branch.Valid {
		j.Branch = &branch.String
	}
	if model.Valid {
		j.Model = &model.String
	}
	if trigComment.Valid {
		j.TriggerCommentID = &trigComment.String
	}
	return &j, nil
}

func (d *DB) ClaimNextJob(ctx context.Context) (*Job, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT id FROM jobs
		WHERE state = 'queued'
		   OR (state = 'retry_scheduled' AND (next_retry_at IS NULL OR next_retry_at <= datetime('now', 'localtime')))
		ORDER BY created_at ASC
		LIMIT 1
	`)
	var id int64
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("claim scan: %w", err)
	}

	var j Job
	var branch, wtPath, phase, lastLog, lastErr, model, trigComment sql.NullString
	var prNum sql.NullInt64
	var pid sql.NullInt64
	var nextRetry, heartbeat, started, finished sql.NullString

	err = tx.QueryRowContext(ctx, `
		UPDATE jobs SET state = 'preparing_worktree', attempt = attempt + 1, started_at = datetime('now', 'localtime'), updated_at = datetime('now', 'localtime')
		WHERE id = ?
		RETURNING id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
	`, id).Scan(
		&j.ID, &j.RepoOwner, &j.RepoName, &j.IssueNumber, &prNum, &branch, &wtPath,
		&j.JobType, &j.State, &phase, &j.Attempt, &j.MaxAttempts, &nextRetry,
		&pid, &heartbeat, &lastLog, &lastErr, &model, &trigComment,
		&j.CreatedAt, &j.UpdatedAt, &started, &finished,
	)
	if err != nil {
		return nil, fmt.Errorf("claim update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("claim commit: %w", err)
	}

	if prNum.Valid {
		n := int(prNum.Int64)
		j.PRNumber = &n
	}
	if branch.Valid {
		j.Branch = &branch.String
	}
	if wtPath.Valid {
		j.WorktreePath = &wtPath.String
	}
	if phase.Valid {
		j.CurrentPhase = &phase.String
	}
	if pid.Valid {
		n := int(pid.Int64)
		j.PID = &n
	}
	if lastLog.Valid {
		j.LastLogLine = &lastLog.String
	}
	if lastErr.Valid {
		j.LastError = &lastErr.String
	}
	if model.Valid {
		j.Model = &model.String
	}
	if trigComment.Valid {
		j.TriggerCommentID = &trigComment.String
	}
	if nextRetry.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", nextRetry.String)
		j.NextRetryAt = &t
	}
	if heartbeat.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", heartbeat.String)
		j.HeartbeatAt = &t
	}
	if started.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", started.String)
		j.StartedAt = &t
	}
	if finished.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", finished.String)
		j.FinishedAt = &t
	}

	return &j, nil
}

func (d *DB) GetJob(ctx context.Context, id int64) (*Job, error) {
	return scanJob(d.conn.QueryRowContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs WHERE id = ?
	`, id))
}

func (d *DB) ListJobs(ctx context.Context) ([]*Job, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

type JobUpdate struct {
	State        *string
	PRNumber     *int
	WorktreePath *string
	CurrentPhase *string
	PID          *int
	HeartbeatAt  *time.Time
	LastLogLine  *string
	LastError    *string
	NextRetryAt  *time.Time
	FinishedAt   *time.Time
	UpdatedAt    *time.Time
}

func (d *DB) UpdateJob(ctx context.Context, id int64, u JobUpdate) error {
	if u.State != nil {
		var currentState string
		d.conn.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, id).Scan(&currentState)
		if currentState != *u.State {
			d.LogState(ctx, id, currentState, *u.State, "")
		}
	}
	if u.LastError != nil && *u.LastError != "" {
		var currentState string
		d.conn.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, id).Scan(&currentState)
		msg := "error: " + *u.LastError
		d.LogState(ctx, id, currentState, currentState, msg)
	}

	sets := "updated_at = datetime('now', 'localtime')"
	args := []any{}

	if u.State != nil {
		sets += ", state = ?"
		args = append(args, *u.State)
	}
	if u.PRNumber != nil {
		sets += ", pr_number = ?"
		args = append(args, *u.PRNumber)
	}
	if u.WorktreePath != nil {
		sets += ", worktree_path = ?"
		args = append(args, *u.WorktreePath)
	}
	if u.CurrentPhase != nil {
		sets += ", current_phase = ?"
		args = append(args, *u.CurrentPhase)
	}
	if u.PID != nil {
		sets += ", pid = ?"
		args = append(args, *u.PID)
	}
	if u.HeartbeatAt != nil {
		sets += ", heartbeat_at = ?"
		args = append(args, u.HeartbeatAt.Format("2006-01-02 15:04:05"))
	}
	if u.LastLogLine != nil {
		sets += ", last_log_line = ?"
		args = append(args, *u.LastLogLine)
	}
	if u.LastError != nil {
		sets += ", last_error = ?"
		args = append(args, *u.LastError)
	}
	if u.NextRetryAt != nil {
		sets += ", next_retry_at = ?"
		args = append(args, u.NextRetryAt.Format("2006-01-02 15:04:05"))
	}
	if u.FinishedAt != nil {
		sets += ", finished_at = ?"
		args = append(args, u.FinishedAt.Format("2006-01-02 15:04:05"))
	}

	args = append(args, id)
	q := fmt.Sprintf("UPDATE jobs SET %s WHERE id = ?", sets)
	_, err := d.conn.ExecContext(ctx, q, args...)
	return err
}

func (d *DB) GetActiveJobCount(ctx context.Context) (int, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs WHERE state IN (
			'preparing_worktree', 'running_agent', 'validating', 'committing', 'pushing', 'creating_pr',
			'applying_pr_feedback', 'cleanup_running'
		)
	`).Scan(&count)
	return count, err
}

func (d *DB) GetActiveJobsByIssue(ctx context.Context, issueNumber int, jobType string) ([]*Job, error) {
	activeStates := []string{
		"queued", "preparing_worktree", "running_agent", "validating", "committing",
		"pushing", "creating_pr", "applying_pr_feedback", "retry_scheduled",
		"needs_clarification", "waiting_for_review", "cleanup_running",
	}
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs
		WHERE issue_number = ? AND job_type = ? AND state IN (`+inClause(len(activeStates))+`)
	`, append([]any{issueNumber, jobType}, strSliceToAny(activeStates)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (d *DB) GetActiveJobForBranch(ctx context.Context, branch string) (*Job, error) {
	return scanJob(d.conn.QueryRowContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs
		WHERE branch = ? AND state IN (
			'queued', 'preparing_worktree', 'running_agent', 'validating', 'committing', 'pushing', 'creating_pr',
			'applying_pr_feedback', 'waiting_for_review', 'retry_scheduled', 'needs_clarification', 'cleanup_running'
		)
	`, branch))
}

func (d *DB) GetActiveFeedbackJob(ctx context.Context, prNumber int, commentID string) ([]*Job, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs
		WHERE pr_number = ? AND trigger_comment_id = ? AND state IN (
			'queued', 'preparing_worktree', 'running_agent', 'validating', 'committing', 'pushing',
			'applying_pr_feedback', 'retry_scheduled'
		)
	`, prNumber, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (d *DB) GetStaleJobs(ctx context.Context) ([]*Job, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs
		WHERE state IN (
			'preparing_worktree', 'running_agent', 'validating', 'committing', 'pushing', 'creating_pr',
			'applying_pr_feedback', 'cleanup_running'
		)
		AND (heartbeat_at IS NULL OR heartbeat_at <= datetime('now', 'localtime', '-5 minutes'))
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (d *DB) GetJobsByState(ctx context.Context, states ...string) ([]*Job, error) {
	if len(states) == 0 {
		return nil, nil
	}
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, repo_owner, repo_name, issue_number, pr_number, branch, worktree_path, job_type, state, current_phase,
			attempt, max_attempts, next_retry_at, pid, heartbeat_at, last_log_line, last_error, model, trigger_comment_id,
			created_at, updated_at, started_at, finished_at
		FROM jobs WHERE state IN (`+inClause(len(states))+`)
		ORDER BY created_at DESC
	`, strSliceToAny(states)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

type ProcessedComment struct {
	RepoOwner   string
	RepoName    string
	PRNumber    int
	CommentID   string
	SenderLogin string
}

func (d *DB) IsCommentProcessed(ctx context.Context, repoOwner, repoName, commentID string) (bool, error) {
	var count int
	err := d.conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM processed_comments
		WHERE repo_owner = ? AND repo_name = ? AND comment_id = ?
	`, repoOwner, repoName, commentID).Scan(&count)
	return count > 0, err
}

func (d *DB) RecordProcessedComment(ctx context.Context, c ProcessedComment) error {
	_, err := d.conn.ExecContext(ctx, `
		INSERT OR IGNORE INTO processed_comments (repo_owner, repo_name, pr_number, comment_id, sender_login)
		VALUES (?, ?, ?, ?, ?)
	`, c.RepoOwner, c.RepoName, c.PRNumber, c.CommentID, c.SenderLogin)
	return err
}

func (d *DB) GetPollState(ctx context.Context, key string) (string, error) {
	var value string
	err := d.conn.QueryRowContext(ctx, `SELECT value FROM poll_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (d *DB) SetPollState(ctx context.Context, key, value string) error {
	_, err := d.conn.ExecContext(ctx, `
		INSERT INTO poll_state (key, value, updated_at) VALUES (?, ?, datetime('now', 'localtime'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now', 'localtime')
	`, key, value)
	return err
}

type JobAttempt struct {
	ID        int64
	JobID     int64
	Attempt   int
	State     string
	LogPath   string
	StartedAt time.Time
	FinishedAt *time.Time
	ExitCode  *int
	Error     *string
}

func (d *DB) RecordJobAttempt(ctx context.Context, jobID int64, attempt int, state, logPath string) error {
	_, err := d.conn.ExecContext(ctx, `
		INSERT INTO job_attempts (job_id, attempt, state, log_path, started_at)
		VALUES (?, ?, ?, ?, datetime('now', 'localtime'))
	`, jobID, attempt, state, logPath)
	return err
}

func (d *DB) FinishJobAttempt(ctx context.Context, jobID int64, attempt int, exitCode int, errStr string) error {
	_, err := d.conn.ExecContext(ctx, `
		UPDATE job_attempts SET exit_code = ?, error = ?, finished_at = datetime('now', 'localtime')
		WHERE job_id = ? AND attempt = ?
	`, exitCode, errStr, jobID, attempt)
	return err
}

type Agent struct {
	ID          int64
	JobID       int64
	PID         *int
	State       string
	StartedAt   time.Time
	HeartbeatAt *time.Time
	Model       string
	WorktreePath string
}

func (d *DB) RegisterAgent(ctx context.Context, jobID int64, pid int, model, worktreePath string) (*Agent, error) {
	row := d.conn.QueryRowContext(ctx, `
		INSERT INTO agents (job_id, pid, state, model, worktree_path, heartbeat_at)
		VALUES (?, ?, 'running', ?, ?, datetime('now', 'localtime'))
		RETURNING id, job_id, pid, state, started_at, heartbeat_at, model, worktree_path
	`, jobID, pid, model, worktreePath)

	var a Agent
	var hb sql.NullString
	err := row.Scan(&a.ID, &a.JobID, &a.PID, &a.State, &a.StartedAt, &hb, &a.Model, &a.WorktreePath)
	if err != nil {
		return nil, fmt.Errorf("register agent: %w", err)
	}
	if hb.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", hb.String)
		a.HeartbeatAt = &t
	}
	return &a, nil
}

func (d *DB) UpdateAgentHeartbeat(ctx context.Context, agentID int64) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE agents SET heartbeat_at = datetime('now', 'localtime') WHERE id = ?`, agentID)
	return err
}

func (d *DB) ResetJobForRetry(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, `
		UPDATE jobs SET state='queued', attempt=0, last_error='', pid=NULL, heartbeat_at=NULL, current_phase=NULL, finished_at=NULL, next_retry_at=NULL
		WHERE id = ?
	`, id)
	return err
}

func (d *DB) DeleteJob(ctx context.Context, id int64) error {
	d.LogState(ctx, id, "", "deleted", "job deleted by user")
	_, err := d.conn.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	return err
}

type StateLog struct {
	ID        int64      `json:"id"`
	JobID     int64      `json:"job_id"`
	FromState *string    `json:"from_state"`
	ToState   string     `json:"to_state"`
	Message   *string    `json:"message"`
	CreatedAt time.Time  `json:"created_at"`
}

func (d *DB) LogState(ctx context.Context, jobID int64, fromState, toState, message string) error {
	var f, m *string
	if fromState != "" {
		f = &fromState
	}
	if message != "" {
		m = &message
	}
	_, err := d.conn.ExecContext(ctx, `
		INSERT INTO state_logs (job_id, from_state, to_state, message)
		VALUES (?, ?, ?, ?)
	`, jobID, f, toState, m)
	return err
}

func (d *DB) GetStateLogs(ctx context.Context, jobID int64) ([]StateLog, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, job_id, from_state, to_state, message, created_at
		FROM state_logs
		WHERE job_id = ?
		ORDER BY id DESC
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query state logs: %w", err)
	}
	defer rows.Close()

	var logs []StateLog
	for rows.Next() {
		var l StateLog
		var from, msg sql.NullString
		var created sql.NullString
		if err := rows.Scan(&l.ID, &l.JobID, &from, &l.ToState, &msg, &created); err != nil {
			return nil, fmt.Errorf("scan state log: %w", err)
		}
		if from.Valid {
			l.FromState = &from.String
		}
		if msg.Valid {
			l.Message = &msg.String
		}
		if created.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", created.String)
			l.CreatedAt = t
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var branch, wtPath, phase, lastLog, lastErr, model, trigComment sql.NullString
	var prNum sql.NullInt64
	var pid sql.NullInt64
	var nextRetry, heartbeat, started, finished sql.NullString

	err := row.Scan(
		&j.ID, &j.RepoOwner, &j.RepoName, &j.IssueNumber, &prNum, &branch, &wtPath,
		&j.JobType, &j.State, &phase, &j.Attempt, &j.MaxAttempts, &nextRetry,
		&pid, &heartbeat, &lastLog, &lastErr, &model, &trigComment,
		&j.CreatedAt, &j.UpdatedAt, &started, &finished,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan job: %w", err)
	}

	if prNum.Valid {
		n := int(prNum.Int64)
		j.PRNumber = &n
	}
	if branch.Valid {
		j.Branch = &branch.String
	}
	if wtPath.Valid {
		j.WorktreePath = &wtPath.String
	}
	if phase.Valid {
		j.CurrentPhase = &phase.String
	}
	if pid.Valid {
		n := int(pid.Int64)
		j.PID = &n
	}
	if lastLog.Valid {
		j.LastLogLine = &lastLog.String
	}
	if lastErr.Valid {
		j.LastError = &lastErr.String
	}
	if model.Valid {
		j.Model = &model.String
	}
	if trigComment.Valid {
		j.TriggerCommentID = &trigComment.String
	}
	if nextRetry.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", nextRetry.String)
		j.NextRetryAt = &t
	}
	if heartbeat.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", heartbeat.String)
		j.HeartbeatAt = &t
	}
	if started.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", started.String)
		j.StartedAt = &t
	}
	if finished.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", finished.String)
		j.FinishedAt = &t
	}

	return &j, nil
}

func inClause(n int) string {
	if n == 0 {
		return "NULL"
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return joinStrings(parts, ",")
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}

func strSliceToAny(s []string) []any {
	r := make([]any, len(s))
	for i, v := range s {
		r[i] = v
	}
	return r
}
