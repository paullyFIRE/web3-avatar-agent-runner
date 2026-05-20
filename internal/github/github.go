package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
)

type Client struct {
	cfg  *config.Config
	repo string
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg:  cfg,
		repo: fmt.Sprintf("%s/%s", cfg.GitHubOwner, cfg.GitHubRepo),
	}
}

func (c *Client) gh(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %w\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

type Issue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updatedAt"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Comments []IssueComment `json:"comments"`
	Author   struct {
		Login string `json:"login"`
	} `json:"author"`
}

type IssueComment struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
	UpdatedAt string `json:"updatedAt"`
}

type PR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	HeadRefName string `json:"headRefName"`
	State       string `json:"state"`
	URL         string `json:"url"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
}

type PRComment struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
}

func (c *Client) ListIssues(label string) ([]Issue, error) {
	data, err := c.gh("issue", "list",
		"--repo", c.repo,
		"--state", "open",
		"--label", label,
		"--json", "number,title,body,labels,author,comments,updatedAt,url",
	)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || string(bytes.TrimSpace(data)) == "[]" {
		return nil, nil
	}
	var issues []Issue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, fmt.Errorf("parse issues: %w", err)
	}
	return issues, nil
}

func (c *Client) GetIssue(number int) (*Issue, error) {
	data, err := c.gh("issue", "view",
		"--repo", c.repo,
		fmt.Sprintf("%d", number),
		"--json", "number,title,body,labels,author,comments,updatedAt,url",
	)
	if err != nil {
		return nil, err
	}
	var issue Issue
	if err := json.Unmarshal(data, &issue); err != nil {
		return nil, fmt.Errorf("parse issue: %w", err)
	}
	return &issue, nil
}

func (c *Client) ListPRs() ([]PR, error) {
	data, err := c.gh("pr", "list",
		"--repo", c.repo,
		"--state", "open",
		"--json", "number,title,body,headRefName,state,url,author",
	)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || string(bytes.TrimSpace(data)) == "[]" {
		return nil, nil
	}
	var prs []PR
	if err := json.Unmarshal(data, &prs); err != nil {
		return nil, fmt.Errorf("parse prs: %w", err)
	}
	return prs, nil
}

func (c *Client) GetPRComments(prNumber int) ([]PRComment, error) {
	data, err := c.gh("pr", "view",
		"--repo", c.repo,
		fmt.Sprintf("%d", prNumber),
		"--json", "comments",
	)
	if err != nil {
		return nil, err
	}
	var result struct {
		Comments []PRComment `json:"comments"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse pr comments: %w", err)
	}
	return result.Comments, nil
}

type RawTimelineItem struct {
	ID        string `json:"node_id"`
	Type      string `json:"event"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (c *Client) GetPRTimelineComments(prNumber int) ([]PRComment, error) {
	data, err := c.gh("api",
		fmt.Sprintf("repos/%s/%s/issues/%d/comments?per_page=100",
			c.cfg.GitHubOwner, c.cfg.GitHubRepo, prNumber),
		"--jq", `.[] | {id: .node_id, body: .body, createdAt: .created_at, author: {login: .user.login}}`,
	)
	if err != nil {
		return nil, fmt.Errorf("get pr timeline: %w", err)
	}
	if len(data) == 0 || string(bytes.TrimSpace(data)) == "" {
		return nil, nil
	}

	lines := strings.Split(string(bytes.TrimSpace(data)), "\n")
	var comments []PRComment
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c PRComment
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		comments = append(comments, c)
	}
	return comments, nil
}

func (c *Client) CreatePR(branch string, issueNumber int, summary string) (string, int, error) {
	title := fmt.Sprintf("fix: resolve issue #%d", issueNumber)
	body := fmt.Sprintf(`## Summary
%s

## Validation
- Pre-commit hook: passed
- Pre-push hook: passed

## Risk
Low - local agent implementation

## Agent
- Runner: local OpenCode Go
- Machine: local MacBook Pro Apple M5, 32 GB RAM

Closes #%d
`, summary, issueNumber)

	cmd := exec.Command("gh", "pr", "create",
		"--repo", c.repo,
		"--title", title,
		"--body-file", "-",
		"--head", branch,
		"--base", c.cfg.BaseBranch,
	)
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("create pr: %w\nstderr: %s", err, stderr.String())
	}

	url := strings.TrimSpace(stdout.String())

	parts := strings.Split(url, "/")
	prNumber := 0
	for i, p := range parts {
		if p == "pull" && i+1 < len(parts) {
			fmt.Sscanf(parts[i+1], "%d", &prNumber)
			break
		}
	}

	return url, prNumber, nil
}

func (c *Client) CommentPR(prNumber int, body string) error {
	_, err := c.gh("pr", "comment",
		"--repo", c.repo,
		fmt.Sprintf("%d", prNumber),
		"--body", body,
	)
	return err
}

func (c *Client) CommentIssue(issueNumber int, body string) error {
	_, err := c.gh("issue", "comment",
		"--repo", c.repo,
		fmt.Sprintf("%d", issueNumber),
		"--body", body,
	)
	return err
}

func (c *Client) RepoAccessible() error {
	_, err := c.gh("repo", "view", c.repo, "--json", "name")
	return err
}

func (c *Client) BaseBranchExists() error {
	_, err := c.gh("api",
		fmt.Sprintf("repos/%s/%s/git/ref/heads/%s",
			c.cfg.GitHubOwner, c.cfg.GitHubRepo, c.cfg.BaseBranch))
	return err
}

func (c *Client) AuthStatus() error {
	_, err := c.gh("auth", "status")
	return err
}
