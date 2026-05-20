package worktree

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
)

type Manager struct {
	cfg *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

func (m *Manager) repoDir() string {
	return filepath.Join(m.cfg.WorkspaceRoot, "repo")
}

func (m *Manager) worktreeDir(issueNumber int) string {
	return filepath.Join(m.cfg.WorkspaceRoot, "worktrees", fmt.Sprintf("issue-%d", issueNumber))
}

func (m *Manager) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func (m *Manager) gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s (in %s): %w\nstderr: %s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return stdout.String(), nil
}

func (m *Manager) EnsureClone() error {
	repoDir := m.repoDir()
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		if err := os.MkdirAll(m.cfg.WorkspaceRoot, 0755); err != nil {
			return fmt.Errorf("create workspace root: %w", err)
		}
		_, err := m.git("clone", m.cfg.RepoURL, repoDir)
		if err != nil {
			return fmt.Errorf("clone repo: %w", err)
		}
	}
	return nil
}

func (m *Manager) EnsureFetch() error {
	repoDir := m.repoDir()
	_, err := m.gitIn(repoDir, "fetch", "origin", m.cfg.BaseBranch, "--prune")
	return err
}

func (m *Manager) AddWorktree(issueNumber int, branch string) (string, error) {
	repoDir := m.repoDir()
	wtDir := m.worktreeDir(issueNumber)

	m.gitIn(repoDir, "worktree", "prune")

	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		return "", fmt.Errorf("create worktrees dir: %w", err)
	}

	_, err := m.gitIn(repoDir, "worktree", "add", "-f", "-B", branch, wtDir, fmt.Sprintf("origin/%s", m.cfg.BaseBranch))
	if err != nil {
		return "", fmt.Errorf("add worktree: %w", err)
	}

	return wtDir, nil
}

func (m *Manager) ReuseWorktree(issueNumber int, branch string) (string, error) {
	repoDir := m.repoDir()
	wtDir := m.worktreeDir(issueNumber)

	m.gitIn(repoDir, "worktree", "prune")

	if _, err := os.Stat(wtDir); err == nil && m.IsWorktreeDir(wtDir) {
		_, err := m.gitIn(wtDir, "checkout", branch)
		if err != nil {
			_, err = m.gitIn(wtDir, "checkout", "-b", branch, fmt.Sprintf("origin/%s", m.cfg.BaseBranch))
		}
		if err != nil {
			return "", fmt.Errorf("checkout in existing worktree: %w", err)
		}
		_, err = m.gitIn(wtDir, "pull", "origin", branch)
		if err != nil {
			_, err = m.gitIn(wtDir, "reset", "--hard", fmt.Sprintf("origin/%s", m.cfg.BaseBranch))
			if err != nil {
				return "", fmt.Errorf("reset worktree: %w", err)
			}
		}
		return wtDir, nil
	}

	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		return "", fmt.Errorf("create worktrees dir: %w", err)
	}

	if _, err := os.Stat(wtDir); err == nil {
		os.RemoveAll(wtDir)
	}

	err := m.checkBranchExists(repoDir, branch)
	if err == nil {
		_, err = m.gitIn(repoDir, "worktree", "add", "-f", wtDir, branch)
	} else {
		_, err = m.gitIn(repoDir, "worktree", "add", "-f", "-b", branch, wtDir, fmt.Sprintf("origin/%s", m.cfg.BaseBranch))
	}
	if err != nil {
		return "", fmt.Errorf("reuse worktree: %w", err)
	}

	return wtDir, nil
}

func (m *Manager) ReuseWorktreeFromRemote(issueNumber int, branch string) (string, error) {
	repoDir := m.repoDir()
	wtDir := m.worktreeDir(issueNumber)

	m.gitIn(repoDir, "worktree", "prune")

	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		return "", fmt.Errorf("create worktrees dir: %w", err)
	}

	_, err := m.gitIn(repoDir, "fetch", "origin", branch)
	if err != nil {
		return "", fmt.Errorf("fetch remote branch: %w", err)
	}

	_, err = m.gitIn(repoDir, "worktree", "add", "-f", "-B", branch, wtDir, fmt.Sprintf("origin/%s", branch))
	if err != nil {
		return "", fmt.Errorf("add worktree from remote: %w", err)
	}

	return wtDir, nil
}

func (m *Manager) RemoveWorktree(issueNumber int, branch string) error {
	repoDir := m.repoDir()
	wtDir := m.worktreeDir(issueNumber)

	if _, err := os.Stat(wtDir); err == nil {
		_, err := m.gitIn(repoDir, "worktree", "remove", wtDir)
		if err != nil {
			_, err = m.gitIn(repoDir, "worktree", "remove", "--force", wtDir)
			if err != nil {
				os.RemoveAll(wtDir)
				m.gitIn(repoDir, "worktree", "prune")
			}
		}
	}

	m.gitIn(repoDir, "worktree", "prune")

	return nil
}

func (m *Manager) Prune() error {
	_, err := m.gitIn(m.repoDir(), "worktree", "prune")
	return err
}

func (m *Manager) HasChanges(worktreePath string) (bool, error) {
	out, err := m.gitIn(worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) CleanWorktree(worktreePath string) error {
	m.gitIn(worktreePath, "checkout", "--", ".")
	_, err := m.gitIn(worktreePath, "clean", "-fd")
	if err != nil {
		return fmt.Errorf("clean worktree: %w", err)
	}
	return nil
}

func (m *Manager) ResetToBase(worktreePath string) error {
	_, err := m.gitIn(worktreePath, "reset", "--hard", fmt.Sprintf("origin/%s", m.cfg.BaseBranch))
	if err != nil {
		return fmt.Errorf("reset to base: %w", err)
	}
	_, err = m.gitIn(worktreePath, "clean", "-fd")
	if err != nil {
		return fmt.Errorf("clean after reset: %w", err)
	}
	return nil
}

func (m *Manager) HasCommit(worktreePath, commitMsgPrefix string) (bool, error) {
	out, err := m.gitIn(worktreePath, "log", "-1", "--format=%s%n%b")
	if err != nil {
		return false, nil
	}
	return strings.Contains(out, commitMsgPrefix), nil
}

func (m *Manager) HasLocalCommits(worktreePath string) (bool, error) {
	out, err := m.gitIn(worktreePath, "rev-list", "HEAD", fmt.Sprintf("^origin/%s", m.cfg.BaseBranch), "--count")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) != "0", nil
}

func (m *Manager) RemoteBranchExists(branch string) bool {
	out, err := m.gitIn(m.repoDir(), "ls-remote", "--heads", "origin", branch)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

func (m *Manager) GetChangedFiles(worktreePath string) ([]string, error) {
	out, err := m.gitIn(worktreePath, "diff", "--name-only")
	if err != nil {
		return nil, err
	}
	out2, err := m.gitIn(worktreePath, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}

	files := strings.Split(strings.TrimSpace(out+"\n"+out2), "\n")
	var result []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f != "" {
			result = append(result, f)
		}
	}
	return result, nil
}

func (m *Manager) StageAll(worktreePath string) error {
	_, err := m.gitIn(worktreePath, "add", "-A")
	return err
}

func (m *Manager) Commit(worktreePath string, message string) error {
	_, err := m.gitIn(worktreePath, "commit", "-m", message)
	return err
}

func (m *Manager) Push(worktreePath string, branch string) error {
	_, err := m.gitIn(worktreePath, "push", "origin", branch)
	return err
}

func (m *Manager) checkBranchExists(repoDir, branch string) error {
	_, err := m.gitIn(repoDir, "rev-parse", "--verify", "refs/heads/"+branch)
	return err
}

func (m *Manager) IsWorktreeDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = m.gitIn(path, "rev-parse", "--git-dir")
	return err == nil
}

func BranchName(issueNumber int, title string) string {
	slug := slugify(title)
	if len(slug) > 40 {
		slug = slug[:40]
	}
	slug = strings.TrimRight(slug, "-")
	return fmt.Sprintf("agent/issue-%d-%s", issueNumber, slug)
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' || r == '/' || r == '.' {
			b.WriteRune('-')
		}
	}
	return b.String()
}
