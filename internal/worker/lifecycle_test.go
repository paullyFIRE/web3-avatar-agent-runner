package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/agent"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/db"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/github"
)

type mockGH struct {
	prs []github.PR
}

func (m *mockGH) ListPRs() ([]github.PR, error) {
	return m.prs, nil
}

func (m *mockGH) GetIssue(number int) (*github.Issue, error) {
	return &github.Issue{Number: number, Title: "test", Body: "test body"}, nil
}

func (m *mockGH) CommentIssue(issueNumber int, body string) error {
	return nil
}

func (m *mockGH) CommentPR(prNumber int, body string) error {
	return nil
}

func (m *mockGH) GetPRComments(prNumber int) ([]github.PRComment, error) {
	return nil, nil
}

func (m *mockGH) CreatePR(branch string, issueNumber int, summary string) (string, int, error) {
	return "https://github.com/test/repo/pull/1", 1, nil
}

type mockWT struct {
	issueCounter int
}

func (m *mockWT) BranchName(issueNumber int, title string) string {
	return fmt.Sprintf("agent/issue-%d-%s", issueNumber, "test")
}

func (m *mockWT) IsWorktreeDir(path string) bool {
	return false
}

func (m *mockWT) ResetToBase(worktreePath string) error {
	return nil
}

func (m *mockWT) HasChanges(worktreePath string) (bool, error) {
	return false, nil
}

func (m *mockWT) GetChangedFiles(worktreePath string) ([]string, error) {
	return nil, nil
}

func (m *mockWT) StageAll(worktreePath string) error {
	return nil
}

func (m *mockWT) Commit(worktreePath, message string) error {
	return nil
}

func (m *mockWT) HasLocalCommits(worktreePath string) (bool, error) {
	return false, nil
}

func (m *mockWT) RemoteBranchExists(branch string) bool {
	return false
}

func (m *mockWT) Push(worktreePath, branch string) error {
	return nil
}

func (m *mockWT) ForcePush(worktreePath, branch string) error {
	return nil
}

func (m *mockWT) EnsureClone() error {
	return nil
}

func (m *mockWT) EnsureFetch() error {
	return nil
}

func (m *mockWT) AddWorktree(issueNumber int, branch string) (string, error) {
	dir, _ := os.MkdirTemp("", "wt-*")
	return dir, nil
}

func (m *mockWT) ReuseWorktree(issueNumber int, branch string) (string, error) {
	return m.AddWorktree(issueNumber, branch)
}

func (m *mockWT) ReuseWorktreeFromRemote(issueNumber int, branch string) (string, error) {
	return m.AddWorktree(issueNumber, branch)
}

func (m *mockWT) RemoveWorktree(issueNumber int, branch string) error {
	return nil
}

type mockAgent struct{}

func (m *mockAgent) Run(ctx context.Context, worktreePath, promptFile, logPath string) (*agent.Result, error) {
	return &agent.Result{Summary: "test implementation"}, nil
}

func (m *mockAgent) GenerateImplementPrompt(issueNumber int, title, body string, comments []string, branch string) string {
	return "implement " + title
}

func (m *mockAgent) GenerateFeedbackPrompt(prNumber, issueNumber int, comment string, title, body string) string {
	return "feedback for " + title
}

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir, err := os.MkdirTemp("", "agent-runner-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	database, err := db.New(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func setupTestPool(t *testing.T, database *db.DB) *Pool {
	t.Helper()
	cfg := config.Default()
	cfg.DashboardAddr = "127.0.0.1:0"

	gh := &mockGH{}
	wt := &mockWT{}
	agt := &mockAgent{}

	return NewPool(cfg, database, gh, wt, agt)
}

func TestEnqueueImplementJob_DedupByIssue(t *testing.T) {
	database := setupTestDB(t)
	pool := setupTestPool(t, database)

	err := pool.EnqueueImplementJob(42, "test issue")
	if err != nil {
		t.Fatal(err)
	}

	jobs, err := database.GetActiveJobsByIssue(context.Background(), 42, "implement_issue")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	err = pool.EnqueueImplementJob(42, "test issue again")
	if err != nil {
		t.Fatal(err)
	}

	jobs, err = database.GetActiveJobsByIssue(context.Background(), 42, "implement_issue")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected still 1 job after duplicate enqueue, got %d", len(jobs))
	}
}

func TestEnqueueImplementJob_DifferentIssues(t *testing.T) {
	database := setupTestDB(t)
	pool := setupTestPool(t, database)

	pool.EnqueueImplementJob(42, "first issue")
	pool.EnqueueImplementJob(99, "second issue")

	jobs42, _ := database.GetActiveJobsByIssue(context.Background(), 42, "implement_issue")
	jobs99, _ := database.GetActiveJobsByIssue(context.Background(), 99, "implement_issue")

	if len(jobs42) != 1 {
		t.Errorf("expected 1 job for issue 42, got %d", len(jobs42))
	}
	if len(jobs99) != 1 {
		t.Errorf("expected 1 job for issue 99, got %d", len(jobs99))
	}
}

func TestEnqueueImplementJob_ExistingPRBlocks(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	gh := &mockGH{
		prs: []github.PR{
			{
				Number:      100,
				HeadRefName: "agent/issue-42-test",
				State:       "OPEN",
				URL:         "https://github.com/test/repo/pull/100",
			},
		},
	}
	wt := &mockWT{}
	agt := &mockAgent{}
	cfg := config.Default()
	pool := NewPool(cfg, database, gh, wt, agt)

	branch := wt.BranchName(42, "test issue")
	t.Logf("branch: %s", branch)

	prs, _ := gh.ListPRs()
	for _, pr := range prs {
		t.Logf("PR #%d headRef: %s, match: %v", pr.Number, pr.HeadRefName, pr.HeadRefName == branch)
	}

	err := pool.EnqueueImplementJob(42, "test issue")
	if err != nil {
		t.Fatal(err)
	}

	jobs, _ := database.GetActiveJobsByIssue(ctx, 42, "implement_issue")
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs (PR blocks creation), got %d", len(jobs))
		for _, j := range jobs {
			t.Logf("  job #%d state=%s branch=%v", j.ID, j.State, *j.Branch)
		}
	}
}

func TestActiveStates_NeedsClarificationBlocksNewJobs(t *testing.T) {
	database := setupTestDB(t)
	pool := setupTestPool(t, database)

	pool.EnqueueImplementJob(5, "needs clarification issue")
	jobs, _ := database.GetActiveJobsByIssue(context.Background(), 5, "implement_issue")
	state := "needs_clarification"
	database.UpdateJob(context.Background(), jobs[0].ID, db.JobUpdate{State: &state})

	err := pool.EnqueueImplementJob(5, "same issue again")
	if err != nil {
		t.Fatal(err)
	}

	jobs, _ = database.GetActiveJobsByIssue(context.Background(), 5, "implement_issue")
	if len(jobs) != 1 {
		t.Errorf("expected 1 job (needs_clarification blocks new), got %d", len(jobs))
	}
}

func TestActiveStates_WaitingForReviewBlocksNewJobs(t *testing.T) {
	database := setupTestDB(t)
	pool := setupTestPool(t, database)

	pool.EnqueueImplementJob(7, "pr open issue")
	jobs, _ := database.GetActiveJobsByIssue(context.Background(), 7, "implement_issue")
	state := "waiting_for_review"
	database.UpdateJob(context.Background(), jobs[0].ID, db.JobUpdate{State: &state})

	err := pool.EnqueueImplementJob(7, "same issue")
	if err != nil {
		t.Fatal(err)
	}

	jobs, _ = database.GetActiveJobsByIssue(context.Background(), 7, "implement_issue")
	if len(jobs) != 1 {
		t.Errorf("expected 1 job (waiting_for_review blocks new), got %d", len(jobs))
	}
}

func TestJobLifecycle_StateLogs(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	job, err := database.CreateJob(ctx, db.CreateJobParams{
		RepoOwner: "test", RepoName: "repo", IssueNumber: 1,
		JobType: "implement_issue", Branch: "agent/issue-1-test", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	states := []string{"preparing_worktree", "running_agent", "waiting_for_review"}
	for _, s := range states {
		database.UpdateJob(ctx, job.ID, db.JobUpdate{State: &s})
	}

	logs, _ := database.GetStateLogs(ctx, job.ID)
	if len(logs) < 3 {
		t.Fatalf("expected at least 3 state logs, got %d", len(logs))
	}

	for i, l := range logs {
		t.Logf("log %d: %v -> %s", i, l.FromState, l.ToState)
	}
}
