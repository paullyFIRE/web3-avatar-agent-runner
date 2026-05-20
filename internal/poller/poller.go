package poller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/db"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/github"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/worker"
)

type Poller struct {
	cfg    *config.Config
	db     *db.DB
	gh     *github.Client
	pool   *worker.Pool
	logger *slog.Logger
	mu     sync.Mutex
}

func New(cfg *config.Config, database *db.DB, ghClient *github.Client, pool *worker.Pool) *Poller {
	return &Poller{
		cfg:    cfg,
		db:     database,
		gh:     ghClient,
		pool:   pool,
		logger: slog.With("component", "poller"),
	}
}

func (p *Poller) Run(ctx context.Context) {
	p.logger.Info("poller started", "interval_seconds", p.cfg.PollIntervalSeconds)

	p.pollOnce(ctx)

	ticker := time.NewTicker(time.Duration(p.cfg.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("poller stopped")
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *Poller) pollOnce(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	log := p.logger

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		if err := p.pollIssues(ctx); err != nil {
			log.Error("poll issues", "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := p.pollPRsAndCleanup(ctx); err != nil {
			log.Error("poll prs/cleanup", "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := p.pollComments(ctx); err != nil {
			log.Error("poll comments", "error", err)
		}
	}()

	wg.Wait()

	if err := p.recoverStaleJobs(ctx); err != nil {
		log.Error("recover stale jobs", "error", err)
	}
}

func (p *Poller) pollIssues(ctx context.Context) error {
	issues, err := p.gh.ListIssues(p.cfg.ReadyLabel)
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}

	for _, issue := range issues {
		if err := p.pool.EnqueueImplementJob(issue.Number, issue.Title); err != nil {
			p.logger.Warn("enqueue implement job", "issue", issue.Number, "error", err)
		}
	}

	return nil
}

func (p *Poller) pollPRsAndCleanup(ctx context.Context) error {
	prs, err := p.gh.ListPRs()
	if err != nil {
		return fmt.Errorf("list prs: %w", err)
	}

	prByNumber := make(map[int]github.PR)
	for _, pr := range prs {
		prByNumber[pr.Number] = pr
	}

	waitingJobs, err := p.db.GetJobsByState(ctx, "waiting_for_review")
	if err == nil {
		for _, job := range waitingJobs {
			if job.PRNumber == nil {
				continue
			}
			pr, ok := prByNumber[*job.PRNumber]
			if !ok {
				continue
			}
			switch pr.State {
			case "MERGED":
				p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
					State:      strPtr("merged"),
					FinishedAt: timePtr(time.Now()),
				})
				p.logger.Info("pr merged", "job_id", job.ID, "pr", pr.Number)
				if p.cfg.CleanupOnMerge {
					p.pool.EnqueueCleanupJob(job.IssueNumber, pr.Number)
				}
			case "CLOSED":
				p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
					State:      strPtr("closed_without_merge"),
					FinishedAt: timePtr(time.Now()),
				})
				p.logger.Info("pr closed without merge", "job_id", job.ID, "pr", pr.Number)
				if p.cfg.CleanupOnClosed {
					p.pool.EnqueueCleanupJob(job.IssueNumber, pr.Number)
				}
			}
		}
	}

	for _, pr := range prs {
		if !strings.HasPrefix(pr.HeadRefName, "agent/issue-") {
			continue
		}

		var issueNumber int
		_, err := fmt.Sscanf(pr.HeadRefName, "agent/issue-%d-", &issueNumber)
		if err != nil {
			continue
		}

		if pr.State == "MERGED" || pr.State == "CLOSED" {
			if p.cfg.CleanupOnMerge && pr.State == "MERGED" {
				p.pool.EnqueueCleanupJob(issueNumber, pr.Number)
			}
			if p.cfg.CleanupOnClosed && pr.State == "CLOSED" {
				p.pool.EnqueueCleanupJob(issueNumber, pr.Number)
			}
		}
	}

	return nil
}

func (p *Poller) pollComments(ctx context.Context) error {
	prs, err := p.gh.ListPRs()
	if err != nil {
		return fmt.Errorf("list prs for comments: %w", err)
	}

	for _, pr := range prs {
		if !strings.HasPrefix(pr.HeadRefName, "agent/issue-") || pr.Author.Login == "" {
			continue
		}

		var issueNumber int
		_, err := fmt.Sscanf(pr.HeadRefName, "agent/issue-%d-", &issueNumber)
		if err != nil {
			continue
		}

		comments, err := p.gh.GetPRTimelineComments(pr.Number)
		if err != nil {
			p.logger.Warn("get pr comments", "pr", pr.Number, "error", err)
			comments, err = p.gh.GetPRComments(pr.Number)
			if err != nil {
				continue
			}
		}

		for _, c := range comments {
			if c.Author.Login != p.cfg.AuthorizedCommenter {
				continue
			}

			processed, err := p.db.IsCommentProcessed(ctx, p.cfg.GitHubOwner, p.cfg.GitHubRepo, c.ID)
			if err != nil || processed {
				continue
			}

			p.db.RecordProcessedComment(ctx, db.ProcessedComment{
				RepoOwner:   p.cfg.GitHubOwner,
				RepoName:    p.cfg.GitHubRepo,
				PRNumber:    pr.Number,
				CommentID:   c.ID,
				SenderLogin: c.Author.Login,
			})

			p.gh.CommentPR(pr.Number, "Local agent accepted this feedback and is starting a follow-up run.")

			if err := p.pool.EnqueueFeedbackJob(pr.Number, issueNumber, c.ID, c.Body); err != nil {
				p.logger.Warn("enqueue feedback job", "pr", pr.Number, "error", err)
			}
		}
	}

	return nil
}

func (p *Poller) recoverStaleJobs(ctx context.Context) error {
	jobs, err := p.db.GetStaleJobs(ctx)
	if err != nil {
		return fmt.Errorf("get stale jobs: %w", err)
	}

	for _, job := range jobs {
		p.logger.Warn("recovering stale job", "job_id", job.ID, "state", job.State)

		if job.Attempt < job.MaxAttempts {
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
				State:       strPtr("retry_scheduled"),
				LastError:   strPtr("recovered from stale state after daemon restart"),
				NextRetryAt: timePtr(time.Now()),
			})
		} else {
			p.db.UpdateJob(ctx, job.ID, db.JobUpdate{
				State:      strPtr("failed"),
				LastError:  strPtr("stale job, retries exhausted"),
				FinishedAt: timePtr(time.Now()),
			})
		}
	}

	return nil
}

func strPtr(s string) *string { return &s }

func timePtr(t time.Time) *time.Time { return &t }
