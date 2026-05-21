package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/agent"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/db"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/github"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/worktree"
)

type Pool struct {
	cfg      *config.Config
	db       *db.DB
	gh       *github.Client
	wt       *worktree.Manager
	agentRun *agent.Runner

	sem    chan struct{}
	wg     sync.WaitGroup
	logger *slog.Logger
}

func NewPool(cfg *config.Config, database *db.DB, ghClient *github.Client, wtMgr *worktree.Manager, agt *agent.Runner) *Pool {
	return &Pool{
		cfg:      cfg,
		db:       database,
		gh:       ghClient,
		wt:       wtMgr,
		agentRun: agt,
		sem:      make(chan struct{}, cfg.MaxConcurrentAgents),
		logger:   slog.With("component", "worker-pool"),
	}
}

func (p *Pool) Start(ctx context.Context) {
	p.wg.Add(1)
	go p.claimLoop(ctx)
}

func (p *Pool) Wait() {
	p.wg.Wait()
}

func (p *Pool) EnqueueImplementJob(issueNumber int, title string) error {
	branch := worktree.BranchName(issueNumber, title)
	maxAttempts := p.cfg.RetryLimit + 1

	existing, err := p.db.GetActiveJobForBranch(context.Background(), branch)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	_, err = p.db.CreateJob(context.Background(), db.CreateJobParams{
		RepoOwner:   p.cfg.GitHubOwner,
		RepoName:    p.cfg.GitHubRepo,
		IssueNumber: issueNumber,
		JobType:     "implement_issue",
		Branch:      branch,
		MaxAttempts: maxAttempts,
		Model:       p.cfg.OpenCodeModel,
	})
	return err
}

func (p *Pool) EnqueueFeedbackJob(prNumber, issueNumber int, commentID, commentBody string) error {
	existing, err := p.db.GetActiveFeedbackJob(context.Background(), prNumber, commentID)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}

	_, err = p.db.CreateJob(context.Background(), db.CreateJobParams{
		RepoOwner:        p.cfg.GitHubOwner,
		RepoName:         p.cfg.GitHubRepo,
		IssueNumber:      issueNumber,
		JobType:          "apply_pr_feedback",
		TriggerCommentID: commentID,
		MaxAttempts:      p.cfg.RetryLimit + 1,
		Model:            p.cfg.OpenCodeModel,
	})
	return err
}

func (p *Pool) EnqueueCleanupJob(issueNumber int, prNumber int) error {
	_, err := p.db.CreateJob(context.Background(), db.CreateJobParams{
		RepoOwner:   p.cfg.GitHubOwner,
		RepoName:    p.cfg.GitHubRepo,
		IssueNumber: issueNumber,
		JobType:     "cleanup",
		MaxAttempts: 1,
		Model:       p.cfg.OpenCodeModel,
	})
	return err
}

func (p *Pool) claimLoop(ctx context.Context) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := p.db.ClaimNextJob(ctx)
		if err != nil {
			p.logger.Error("claim job", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if job == nil {
			time.Sleep(2 * time.Second)
			continue
		}

		select {
		case p.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}

		p.wg.Add(1)
		go func(j *db.Job) {
			defer p.wg.Done()
			defer func() { <-p.sem }()
			p.processJob(ctx, j)
		}(job)
	}
}

func (p *Pool) processJob(ctx context.Context, job *db.Job) {
	log := p.logger.With("job_id", job.ID, "type", job.JobType)

	switch job.JobType {
	case "implement_issue":
		p.implementIssue(ctx, job)
	case "apply_pr_feedback":
		p.applyFeedback(ctx, job)
	case "cleanup":
		p.runCleanup(ctx, job)
	default:
		log.Error("unknown job type")
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("failed"), LastError: strPtr("unknown job type")})
	}
}

func (p *Pool) implementIssue(ctx context.Context, job *db.Job) {
	log := p.logger.With("job_id", job.ID, "issue", job.IssueNumber)
	issueNumber := job.IssueNumber

	phase := ""
	if job.CurrentPhase != nil {
		phase = *job.CurrentPhase
	}

	log.Info("starting implement flow", "phase", phase, "attempt", job.Attempt)

	fromState := job.State

	hb := time.Now()
	if phase == "" {
		p.db.LogState(ctx, job.ID, fromState, "preparing_worktree", "")
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("preparing_worktree"), HeartbeatAt: &hb})
	}

	// === CHECKPOINT: Worktree ===
	var wtPath string
	if phase != "" && job.WorktreePath != nil && p.wt.IsWorktreeDir(*job.WorktreePath) {
		wtPath = *job.WorktreePath
		log.Info("resuming with existing worktree", "path", wtPath)
		if phase == "worktree" || phase == "agent" {
			p.wt.ResetToBase(wtPath)
		}
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{CurrentPhase: strPtr("worktree"), HeartbeatAt: timePtr(time.Now())})
	}

	if wtPath == "" {
		if err := p.wt.EnsureClone(); err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("ensure clone: %w", err))
			return
		}
		if err := p.wt.EnsureFetch(); err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("ensure fetch: %w", err))
			return
		}

		branch := *job.Branch

		var err error
		wtPath, err = p.wt.AddWorktree(issueNumber, branch)
		if err != nil {
			wtPath, err = p.wt.ReuseWorktree(issueNumber, branch)
			if err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("worktree setup: %w", err))
				return
			}
		}

		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{WorktreePath: &wtPath, CurrentPhase: strPtr("worktree")})
		log = log.With("worktree", wtPath)
	} else {
		log = log.With("worktree", wtPath)
	}

	// === CHECKPOINT: Agent run ===
	branch := *job.Branch
	if phase == "" || phase == "worktree" || phase == "agent" {
		hb = time.Now()
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:        strPtr("running_agent"),
			CurrentPhase: strPtr("agent"),
			HeartbeatAt:  &hb,
		})

		if job.Attempt == 1 {
			if err := p.gh.CommentIssue(issueNumber, "🤖 Starting work on this issue..."); err != nil {
				log.Warn("failed to comment start on issue", "error", err)
			}
		}

		issue, err := p.gh.GetIssue(issueNumber)
		if err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("get issue: %w", err))
			return
		}

		var comments []string
		for _, c := range issue.Comments {
			comments = append(comments, fmt.Sprintf("%s: %s", c.Author.Login, c.Body))
		}

		prompt := p.agentRun.GenerateImplementPrompt(issueNumber, issue.Title, issue.Body, comments, branch)
		promptFile := filepath.Join(wtPath, ".opencode-prompt.md")
		os.WriteFile(promptFile, []byte(prompt), 0644)

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go p.heartbeatLoop(ctx, job.ID)

		result, err := p.agentRun.Run(ctx, wtPath, promptFile)

		logPath := filepath.Join(p.cfg.LogDir, fmt.Sprintf("job-%d-attempt-%d.log", job.ID, job.Attempt))
		os.MkdirAll(p.cfg.LogDir, 0755)
		var logData string
		if result != nil {
			logData = result.RawOutput
			if result.RawError != "" {
				logData += "\n--- stderr ---\n" + result.RawError
			}
		}
		if err != nil {
			logData += fmt.Sprintf("\n--- error ---\n%v\n", err)
		}
		if logData != "" {
			os.WriteFile(logPath, []byte(logData), 0644)
		}

		if err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("agent run: %w", err))
			return
		}

		if result.NeedsClarification {
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
				State:      strPtr("needs_clarification"),
				LastError:  &result.ClarificationMsg,
				FinishedAt: timePtr(time.Now()),
			})
			log.Warn("needs clarification", "msg", result.ClarificationMsg)
			return
		}

		// === CHECKPOINT: Validate ===
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("validating"), CurrentPhase: strPtr("validated")})

		hasChanges, err := p.wt.HasChanges(wtPath)
		if err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("check changes: %w", err))
			return
		}
		if !hasChanges {
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
				State:      strPtr("needs_clarification"),
				LastError:  strPtr("no changes produced by agent"),
				FinishedAt: timePtr(time.Now()),
			})
			return
		}

		changedFiles, err := p.wt.GetChangedFiles(wtPath)
		if err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("get changed files: %w", err))
			return
		}

		for _, f := range changedFiles {
			if match := p.cfg.IsPathProtected(f); match != nil {
				p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
					State:      strPtr("blocked"),
					LastError:  strPtr(fmt.Sprintf("protected path modified: %s (matched pattern: %s)", match.File, match.Pattern)),
					FinishedAt: timePtr(time.Now()),
				})
				log.Warn("blocked - protected file", "file", f, "pattern", match.Pattern)
				return
			}
		}

		// === CHECKPOINT: Stage + Commit ===
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("committing"), CurrentPhase: strPtr("committed")})

		didCommit := false
		committed, _ := p.wt.HasLocalCommits(wtPath)
		if !committed {
			if err := p.wt.StageAll(wtPath); err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("stage: %w", err))
				return
			}
			commitMsg := p.buildCommitMsg(job, result.Summary)
			if err := p.wt.Commit(wtPath, commitMsg); err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("commit: %w", err))
				return
			}
			didCommit = true
		} else {
			log.Info("commit already exists, skipping")
		}

		// === CHECKPOINT: Push ===
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("pushing"), CurrentPhase: strPtr("pushed")})

		remoteExists := p.wt.RemoteBranchExists(branch)
		if didCommit && remoteExists {
			if err := p.wt.ForcePush(wtPath, branch); err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("force push: %w", err))
				return
			}
		} else if didCommit || !remoteExists {
			if err := p.wt.Push(wtPath, branch); err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("push: %w", err))
				return
			}
		} else {
			log.Info("remote branch already exists, skipping push")
		}

		// === CHECKPOINT: PR ===
		prNumber := job.PRNumber
		prURL := ""
		if prNumber == nil {
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("creating_pr")})
			prNumber, prURL, err = p.findOrCreatePR(branch, issueNumber, result.Summary)
			if err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("create pr: %w", err))
				return
			}
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{CurrentPhase: strPtr("pr")})
		} else {
			log.Info("PR already exists", "pr", *prNumber)
			prs, listErr := p.gh.ListPRs()
			if listErr == nil {
				for _, pr := range prs {
					if pr.Number == *prNumber {
						prURL = pr.URL
						break
					}
				}
			}
		}

		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:    strPtr("waiting_for_review"),
			PRNumber: prNumber,
		})
		log.Info("implement flow complete", "pr", prNumber, "url", prURL)
		return
	}

	// === RESUME from later phases ===
	if phase == "validated" || phase == "committed" || phase == "pushed" || phase == "pr" {
		prNumber := job.PRNumber
		prURL := ""
		if prNumber == nil {
			newPR, url, err := p.findOrCreatePR(branch, issueNumber, "resuming previous implementation attempt")
			if err != nil {
				p.handleFailure(ctx, job, fmt.Errorf("resume create pr: %w", err))
				return
			}
			prNumber = newPR
			prURL = url
		} else {
			prs, listErr := p.gh.ListPRs()
			if listErr == nil {
				for _, pr := range prs {
					if pr.Number == *prNumber {
						prURL = pr.URL
						break
					}
				}
			}
		}
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:    strPtr("waiting_for_review"),
			PRNumber: prNumber,
		})
		log.Info("job already completed, updated state to waiting_for_review", "pr", prNumber, "url", prURL)
	}
}

func (p *Pool) applyFeedback(ctx context.Context, job *db.Job) {
	log := p.logger.With("job_id", job.ID, "issue", job.IssueNumber)
	issueNumber := job.IssueNumber

	log.Info("starting feedback flow")

	fetchPR := func() (*github.PR, error) {
		prs, err := p.gh.ListPRs()
		if err != nil {
			return nil, err
		}
		for _, pr := range prs {
			if pr.Number == *job.PRNumber {
				return &pr, nil
			}
		}
		return nil, fmt.Errorf("pr %d not found", *job.PRNumber)
	}

	pr, err := fetchPR()
	if err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("fetch pr: %w", err))
		return
	}

	p.gh.CommentPR(*job.PRNumber, "Local agent accepted this feedback and is starting a follow-up run.")

	branch := pr.HeadRefName

	p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("preparing_worktree")})

	if err := p.wt.EnsureClone(); err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("ensure clone: %w", err))
		return
	}
	if err := p.wt.EnsureFetch(); err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("ensure fetch: %w", err))
		return
	}

	wtPath, err := p.wt.ReuseWorktree(issueNumber, branch)
	if err != nil {
		wtPath, err = p.wt.ReuseWorktreeFromRemote(issueNumber, branch)
		if err != nil {
			p.handleFailure(ctx, job, fmt.Errorf("worktree setup: %w", err))
			return
		}
	}

	p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
		WorktreePath: &wtPath,
		State:        strPtr("applying_pr_feedback"),
	})

	commentBody := ""
	if job.TriggerCommentID != nil {
		comments, err := p.gh.GetPRComments(*job.PRNumber)
		if err == nil {
			for _, c := range comments {
				if c.ID == *job.TriggerCommentID {
					commentBody = c.Body
					break
				}
			}
		}
	}

	prompt := p.agentRun.GenerateFeedbackPrompt(
		*job.PRNumber, issueNumber, commentBody,
		pr.Title, pr.Body,
	)
	promptFile := filepath.Join(wtPath, ".opencode-prompt.md")
	os.WriteFile(promptFile, []byte(prompt), 0644)

	result, err := p.agentRun.Run(ctx, wtPath, promptFile)
	if err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("agent run: %w", err))
		return
	}

	if result.NeedsClarification {
		p.gh.CommentPR(*job.PRNumber, fmt.Sprintf("Clarification needed: %s", result.ClarificationMsg))
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:      strPtr("needs_clarification"),
			LastError:  &result.ClarificationMsg,
			FinishedAt: timePtr(time.Now()),
		})
		return
	}

	hasChanges, err := p.wt.HasChanges(wtPath)
	if err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("check changes: %w", err))
		return
	}
	if !hasChanges {
		p.gh.CommentPR(*job.PRNumber, "No changes were needed based on the feedback.")
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:      strPtr("waiting_for_review"),
			FinishedAt: timePtr(time.Now()),
		})
		return
	}

	changedFiles, err := p.wt.GetChangedFiles(wtPath)
	if err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("get changed files: %w", err))
		return
	}
	for _, f := range changedFiles {
		if match := p.cfg.IsPathProtected(f); match != nil {
			p.gh.CommentPR(*job.PRNumber, fmt.Sprintf("Cannot modify protected file: %s", match.File))
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
				State:      strPtr("blocked"),
				LastError:  strPtr(fmt.Sprintf("protected path: %s", match.File)),
				FinishedAt: timePtr(time.Now()),
			})
			return
		}
	}

	if err := p.wt.StageAll(wtPath); err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("stage: %w", err))
		return
	}

	commitMsg := p.buildCommitMsg(job, result.Summary)
	if err := p.wt.Commit(wtPath, commitMsg); err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("commit: %w", err))
		return
	}

	if err := p.wt.Push(wtPath, branch); err != nil {
		p.handleFailure(ctx, job, fmt.Errorf("push: %w", err))
		return
	}

	reply := fmt.Sprintf("Feedback implemented.\n\nSummary: %s\nCommit: %s", result.Summary, commitMsg)
	if len(result.FilesChanged) > 0 {
		reply += "\nFiles changed: " + strings.Join(result.FilesChanged, ", ")
	}
	p.gh.CommentPR(*job.PRNumber, reply)

	p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
		State:      strPtr("waiting_for_review"),
		FinishedAt: timePtr(time.Now()),
	})
	log.Info("feedback flow complete")
}

func (p *Pool) runCleanup(ctx context.Context, job *db.Job) {
	log := p.logger.With("job_id", job.ID, "issue", job.IssueNumber)

	p.db.UpdateJob(ctx, job.ID, db.JobUpdate{State: strPtr("cleanup_running")})

	prs, err := p.gh.ListPRs()
	var targetPR *github.PR
	if err == nil {
		for _, pr := range prs {
			if pr.Number == *job.PRNumber {
				targetPR = &pr
				break
			}
		}
	}

	branch := ""
	if job.Branch != nil {
		branch = *job.Branch
	} else if targetPR != nil {
		branch = targetPR.HeadRefName
	}

	if branch != "" {
		if err := p.wt.RemoveWorktree(job.IssueNumber, branch); err != nil {
			log.Warn("cleanup worktree", "error", err)
		}
	}

	state := "cleanup_done"
	if targetPR != nil && targetPR.State == "MERGED" {
		state = "merged"
	} else {
		state = "closed_without_merge"
	}

	p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
		State:      strPtr(state),
		FinishedAt: timePtr(time.Now()),
	})
	log.Info("cleanup complete", "final_state", state)
}

func (p *Pool) heartbeatLoop(ctx context.Context, jobID int64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			p.db.UpdateJob(context.Background(), jobID, db.JobUpdate{HeartbeatAt: &now})
		}
	}
}

func (p *Pool) handleFailure(ctx context.Context, job *db.Job, err error) {
	log := p.logger.With("job_id", job.ID)
	errStr := err.Error()

	if job.Attempt < job.MaxAttempts {
		nextRetry := time.Now().Add(time.Duration(p.cfg.RetryBackoffSeconds) * time.Second)
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:       strPtr("retry_scheduled"),
			LastError:   &errStr,
			NextRetryAt: &nextRetry,
		})
		log.Warn("scheduled retry", "attempt", job.Attempt, "max", job.MaxAttempts, "next_retry", nextRetry)
	} else {
		p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
			State:      strPtr("failed"),
			LastError:  &errStr,
			FinishedAt: timePtr(time.Now()),
		})
		log.Error("job failed", "error", errStr)

		switch job.JobType {
		case "implement_issue":
			if cerr := p.gh.CommentIssue(job.IssueNumber, fmt.Sprintf("🤖 Failed to implement: %s", errStr)); cerr != nil {
				log.Warn("failed to comment failure on issue", "error", cerr)
			}
		case "apply_pr_feedback":
			if job.PRNumber != nil {
				if cerr := p.gh.CommentPR(*job.PRNumber, fmt.Sprintf("🤖 Failed to apply feedback: %s", errStr)); cerr != nil {
					log.Warn("failed to comment failure on pr", "error", cerr)
				}
			}
		}
	}
}

func (p *Pool) buildCommitMsg(job *db.Job, summary string) string {
	issueRef := fmt.Sprintf("#%d", job.IssueNumber)
	body := summary
	if len(body) > 350 {
		body = body[:350] + "..."
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if len(line) > 400 {
			lines[i] = line[:397] + "..."
		}
	}
	body = strings.Join(lines, "\n")
	return fmt.Sprintf("fix: resolve issue %s\n\n%s\n\nGenerated by local OpenCode Go runner.", issueRef, body)
}

func (p *Pool) findOrCreatePR(branch string, issueNumber int, summary string) (*int, string, error) {
	prs, err := p.gh.ListPRs()
	if err == nil {
		for _, pr := range prs {
			if pr.HeadRefName == branch {
				if pr.State == "MERGED" {
					return &pr.Number, pr.URL, nil
				}
				p.gh.CommentIssue(issueNumber, fmt.Sprintf("Updated PR: %s", pr.URL))
				return &pr.Number, pr.URL, nil
			}
		}
	}

	prURL, prNumber, err := p.gh.CreatePR(branch, issueNumber, summary)
	if err != nil {
		return nil, "", err
	}

	p.gh.CommentIssue(issueNumber, fmt.Sprintf("PR created: %s", prURL))
	return &prNumber, prURL, nil
}

func strPtr(s string) *string { return &s }

func timePtr(t time.Time) *time.Time { return &t }

func (p *Pool) updateJobState(ctx context.Context, jobID int64, u db.JobUpdate, fromState, message string) {
	if u.State != nil {
		p.db.LogState(ctx, jobID, fromState, *u.State, message)
	}
	p.db.UpdateJob(ctx, jobID, u)
}
