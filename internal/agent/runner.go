package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
)

type Result struct {
	Summary            string
	FilesChanged       []string
	ValidationNotes    string
	NeedsClarification bool
	ClarificationMsg   string
	RawOutput          string
	RawError           string
	PID                int
}

type Runner struct {
	cfg  *config.Config
	OnPid func(pid int) // called immediately after the agent process starts
}

func NewRunner(cfg *config.Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) SetOnPid(fn func(pid int)) {
	r.OnPid = fn
}

func (r *Runner) Run(ctx context.Context, worktreePath, promptFile, logPath string) (*Result, error) {
	timeout := 15 * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.cfg.OpenCodeBin,
		"run",
		"-m", r.cfg.OpenCodeModel,
		"-f", promptFile,
		"--dangerously-skip-permissions",
		"Implement the changes described in the attached prompt file.",
	)
	cmd.Dir = worktreePath
	cmd.Env = append(os.Environ(),
		"OPENCODE_ALLOW_NETWORK=true",
	)

	var stdout, stderr bytes.Buffer

	if logPath != "" {
		os.MkdirAll(filepath.Dir(logPath), 0755)
		f, err := os.Create(logPath)
		if err == nil {
			defer f.Close()
			cmd.Stdout = io.MultiWriter(&stdout, f)
			cmd.Stderr = io.MultiWriter(&stderr, f)
		} else {
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
		}
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent: %w", err)
	}

	result := &Result{PID: cmd.Process.Pid}
	if r.OnPid != nil {
		r.OnPid(cmd.Process.Pid)
	}

	err := cmd.Wait()

	output := stdout.String()
	errOutput := stderr.String()

	result.RawOutput = output
	result.RawError = errOutput

	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("agent cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("agent exited with error: %w\nstdout: %s\nstderr: %s", err, output, errOutput)
	}

	result.Summary = parseSummary(output)
	result.FilesChanged = parseFilesChanged(output)
	result.ValidationNotes = parseValidationNotes(output)
	result.NeedsClarification = strings.Contains(strings.ToLower(result.Summary), "clarification needed") ||
		strings.Contains(strings.ToLower(result.Summary), "needs clarification") ||
		strings.Contains(strings.ToLower(result.ValidationNotes), "ambiguous")
	if result.NeedsClarification {
		lowerSum := strings.ToLower(result.Summary)
		lowerVal := strings.ToLower(result.ValidationNotes)
		if strings.Contains(lowerSum, "no clarification needed") ||
			strings.Contains(lowerSum, "clarification needed: no") ||
			strings.Contains(lowerSum, "clarification needed: none") ||
			strings.Contains(lowerSum, "clarification needed: false") ||
			strings.Contains(lowerSum, "not needed") ||
			strings.Contains(lowerVal, "no clarification needed") ||
			strings.Contains(lowerVal, "clarification needed: no") ||
			strings.Contains(lowerVal, "clarification needed: none") ||
			strings.Contains(lowerVal, "clarification needed: false") ||
			strings.Contains(lowerVal, "not needed") {
			result.NeedsClarification = false
		}
	}
	if result.NeedsClarification {
		result.ClarificationMsg = result.Summary
		if result.ValidationNotes != "" {
			result.ClarificationMsg += "\n\n" + result.ValidationNotes
		}
	}

	if errOutput != "" {
		if result.ValidationNotes != "" {
			result.ValidationNotes += "\n"
		}
		result.ValidationNotes += "stderr: " + errOutput
	}

	return result, nil
}

func (r *Runner) GenerateImplementPrompt(issueNumber int, title, body string, comments []string, branch string) string {
	var commentsBlock string
	for i, c := range comments {
		commentsBlock += fmt.Sprintf("Comment %d:\n%s\n\n", i+1, c)
	}

	return fmt.Sprintf(`You are running locally inside a git worktree for GitHub issue #%d.

Repository:
%s/%s

Base branch:
%s

Current branch:
%s

Issue title:
%s

Issue body:
%s

Issue comments:
%s

Rules:
- Follow repository conventions and existing code style.
- Inspect relevant files before editing.
- Implement the smallest safe fix.
- Add or update tests when appropriate.
- Do not modify unrelated files.
- Do not commit.
- Do not push.
- Do not create a PR.
- Do not request reviewers.
- Never request paullyFIRE as reviewer.
- Do not edit protected files unless the issue explicitly requires it.
- If the issue is ambiguous, stop and write a clear clarification request.
- Network access is allowed.
- Repo pre-commit and pre-push hooks will run outside this agent.
- Do not bypass hooks.
- Prefer simple, maintainable changes over broad refactors.
- Avoid new dependencies unless clearly justified.

Return:
- implementation summary;
- files changed;
- validation notes if you ran anything manually;
- whether clarification is needed.
`,
		issueNumber,
		r.cfg.GitHubOwner, r.cfg.GitHubRepo,
		r.cfg.BaseBranch,
		branch,
		title,
		body,
		commentsBlock,
	)
}

func (r *Runner) GenerateFeedbackPrompt(prNumber, issueNumber int, comment string, title, body string) string {
	return fmt.Sprintf(`You are running locally inside an existing PR branch for GitHub PR #%d, issue #%d.

Authorized commenter paullyFIRE posted this PR timeline comment:

%s

Existing PR context:
%s

%s

Rules:
- Address only the requested feedback.
- Preserve prior implementation unless a change is required.
- Do not commit.
- Do not push.
- Do not create a new PR.
- Do not request reviewers.
- Never request paullyFIRE as reviewer.
- Follow repository conventions.
- Keep the diff minimal.
- Do not bypass hooks.

Return:
- summary of changes;
- files changed;
- whether additional clarification is needed.
`,
		prNumber, issueNumber,
		comment,
		title,
		body,
	)
}

func parseSummary(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- implementation summary:") {
			return strings.TrimPrefix(trimmed, "- implementation summary:")
		}
		if strings.HasPrefix(trimmed, "implementation summary:") {
			return strings.TrimPrefix(trimmed, "implementation summary:")
		}
	}
	return output
}

func parseFilesChanged(output string) []string {
	var files []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- files changed:") {
			f := strings.TrimPrefix(trimmed, "- files changed:")
			for _, part := range strings.Split(f, ",") {
				files = append(files, strings.TrimSpace(part))
			}
		}
	}
	return files
}

func parseValidationNotes(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- validation notes:") {
			return strings.TrimPrefix(trimmed, "- validation notes:")
		}
	}
	return ""
}

func extractClarification(output string) string {
	lines := strings.Split(output, "\n")
	var msgLines []string
	recording := false
	for _, line := range lines {
		if strings.Contains(line, "clarification") || strings.Contains(line, "ambiguous") {
			recording = true
		}
		if recording {
			msgLines = append(msgLines, line)
		}
		if len(msgLines) > 10 {
			break
		}
	}
	return strings.Join(msgLines, "\n")
}
