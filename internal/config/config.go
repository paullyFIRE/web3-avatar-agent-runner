package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GitHubOwner             string   `yaml:"github_owner"`
	GitHubRepo              string   `yaml:"github_repo"`
	RepoURL                 string   `yaml:"repo_url"`
	ReadyLabel              string   `yaml:"ready_label"`
	AuthorizedCommenter     string   `yaml:"authorized_commenter"`
	ReviewBots              []string `yaml:"review_bots"`
	DisallowedReviewer      string   `yaml:"disallowed_reviewer"`
	BaseBranch              string   `yaml:"base_branch"`
	PollIntervalSeconds     int      `yaml:"poll_interval_seconds"`
	MaxConcurrentAgents     int      `yaml:"max_concurrent_agents"`
	RetryLimit              int      `yaml:"retry_limit"`
	RetryBackoffSeconds     int      `yaml:"retry_backoff_seconds"`
	DeleteRemoteBranchOnCleanup bool  `yaml:"delete_remote_branch_on_cleanup"`
	CleanupOnMerge          bool     `yaml:"cleanup_on_merge"`
	CleanupOnClosed         bool     `yaml:"cleanup_on_closed"`
	OpenCodeBin             string   `yaml:"opencode_bin"`
	OpenCodeModel           string   `yaml:"opencode_model"`
	OpenCodeCommandTemplate string   `yaml:"opencode_command_template"`
	WorkspaceRoot           string   `yaml:"workspace_root"`
	ProtectedPaths          []string `yaml:"protected_paths"`
	DashboardAddr           string   `yaml:"dashboard_addr"`
	LogDir                  string   `yaml:"log_dir"`
	ConfigPathUsed          string   `yaml:"-"`
}

func Default() *Config {
	home, _ := os.UserHomeDir()
	ws := filepath.Join(home, ".local", "share", "web3-avatar-agent-runner")
	return &Config{
		GitHubOwner:             "paullyFIRE",
		GitHubRepo:              "web3-avatar",
		RepoURL:                 "https://github.com/paullyFIRE/web3-avatar",
		ReadyLabel:              "agent-ready",
		AuthorizedCommenter:     "paullyFIRE",
		DisallowedReviewer:      "paullyFIRE",
		BaseBranch:              "master",
		PollIntervalSeconds:     30,
		MaxConcurrentAgents:     2,
		RetryLimit:              2,
		RetryBackoffSeconds:     3600,
		DeleteRemoteBranchOnCleanup: false,
		CleanupOnMerge:          true,
		CleanupOnClosed:         true,
		OpenCodeBin:             "opencode",
		OpenCodeModel:           "opencode-go/deepseek-v4-flash",
		OpenCodeCommandTemplate: "opencode run -m opencode-go/deepseek-v4-flash -f {{prompt_file}} --dangerously-skip-permissions Implement the changes described in the attached prompt file.",
		WorkspaceRoot:           ws,
		ProtectedPaths: []string{
			".github/workflows/**",
			".env",
			".env.*",
			"secrets/**",
			"infra/**",
			"terraform/**",
			"k8s/**",
			"helm/**",
		},
		DashboardAddr: "127.0.0.1:8123",
		LogDir:        filepath.Join(ws, "logs"),
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	cfg.ConfigPathUsed = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.applyEnvOverrides()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyEnvOverrides()
	return cfg, nil
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "web3-avatar-agent-runner", "config.yaml")
}

func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("GITHUB_OWNER"); v != "" {
		c.GitHubOwner = v
	}
	if v := os.Getenv("GITHUB_REPO"); v != "" {
		c.GitHubRepo = v
	}
	if v := os.Getenv("REPO_URL"); v != "" {
		c.RepoURL = v
	}
	if v := os.Getenv("READY_LABEL"); v != "" {
		c.ReadyLabel = v
	}
	if v := os.Getenv("AUTHORIZED_COMMENTER"); v != "" {
		c.AuthorizedCommenter = v
	}
	if v := os.Getenv("DISALLOWED_REVIEWER"); v != "" {
		c.DisallowedReviewer = v
	}
	if v := os.Getenv("BASE_BRANCH"); v != "" {
		c.BaseBranch = v
	}
	if v := os.Getenv("POLL_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.PollIntervalSeconds = n
		}
	}
	if v := os.Getenv("MAX_CONCURRENT_AGENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxConcurrentAgents = n
		}
	}
	if v := os.Getenv("RETRY_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RetryLimit = n
		}
	}
	if v := os.Getenv("RETRY_BACKOFF_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RetryBackoffSeconds = n
		}
	}
	if v := os.Getenv("DELETE_REMOTE_BRANCH_ON_CLEANUP"); v != "" {
		c.DeleteRemoteBranchOnCleanup = v == "true" || v == "1"
	}
	if v := os.Getenv("CLEANUP_ON_MERGE"); v != "" {
		c.CleanupOnMerge = v == "true" || v == "1"
	}
	if v := os.Getenv("CLEANUP_ON_CLOSED"); v != "" {
		c.CleanupOnClosed = v == "true" || v == "1"
	}
	if v := os.Getenv("OPENCODE_BIN"); v != "" {
		c.OpenCodeBin = v
	}
	if v := os.Getenv("OPENCODE_MODEL"); v != "" {
		c.OpenCodeModel = v
	}
	if v := os.Getenv("OPENCODE_COMMAND_TEMPLATE"); v != "" {
		c.OpenCodeCommandTemplate = v
	}
	if v := os.Getenv("WORKSPACE_ROOT"); v != "" {
		c.WorkspaceRoot = v
	}
	if v := os.Getenv("DASHBOARD_ADDR"); v != "" {
		c.DashboardAddr = v
	}
	if v := os.Getenv("REVIEW_BOTS"); v != "" {
		c.ReviewBots = strings.Split(v, ",")
	}
}

func (c *Config) IsAuthorizedCommenter(login string) bool {
	if login == c.AuthorizedCommenter {
		return true
	}
	for _, bot := range c.ReviewBots {
		if login == bot {
			return true
		}
	}
	return false
}

func (c *Config) ExpandPaths() error {
	expand := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, p[2:])
		}
		return p
	}
	c.WorkspaceRoot = expand(c.WorkspaceRoot)
	c.LogDir = expand(c.LogDir)
	return nil
}

type ProtectedPathMatch struct {
	Pattern string
	File    string
}

func (c *Config) IsPathProtected(filePath string) *ProtectedPathMatch {
	clean := strings.TrimPrefix(filePath, "./")
	for _, pattern := range c.ProtectedPaths {
		if matched, _ := filepath.Match(pattern, clean); matched {
			return &ProtectedPathMatch{Pattern: pattern, File: clean}
		}
		if strings.HasPrefix(clean, strings.TrimSuffix(pattern, "**")) {
			return &ProtectedPathMatch{Pattern: pattern, File: clean}
		}
		if strings.HasPrefix(clean, strings.TrimSuffix(pattern, "*")) {
			return &ProtectedPathMatch{Pattern: pattern, File: clean}
		}
	}
	return nil
}
