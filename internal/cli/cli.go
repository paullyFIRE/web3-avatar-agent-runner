package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/daemon"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/db"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/github"
)

func Run(args []string) error {
	if len(args) < 1 {
		printUsage()
		return nil
	}

	cmd := args[0]

	switch cmd {
	case "doctor":
		return doctor()
	case "start":
		return startDaemon()
	case "status":
		return showStatus()
	case "jobs":
		return listJobs()
	case "logs":
		if len(args) < 2 {
			return fmt.Errorf("usage: agent-runner logs <job_id>")
		}
		return tailLogs(args[1])
	case "retry":
		if len(args) < 2 {
			return fmt.Errorf("usage: agent-runner retry <job_id>")
		}
		return retryJob(args[1])
	case "cancel":
		if len(args) < 2 {
			return fmt.Errorf("usage: agent-runner cancel <job_id>")
		}
		return cancelJob(args[1])
	case "cleanup":
		if len(args) < 2 {
			return fmt.Errorf("usage: agent-runner cleanup <job_id>")
		}
		return cleanupJob(args[1])
	case "plist":
		if len(args) < 2 {
			return fmt.Errorf("usage: agent-runner plist <install|uninstall|path>")
		}
		return plistCmd(args[1])
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func printUsage() {
	fmt.Println(`Usage: agent-runner <command> [arguments]

Commands:
  doctor                    Verify prerequisites
  start                     Start the daemon
  status                    Show daemon health summary
  jobs                      List all jobs
  logs <job_id>             Tail logs for a job
  retry <job_id>            Retry a failed job
  cancel <job_id>           Cancel a pending/running job
  cleanup <job_id>          Force cleanup a job
  plist <install|uninstall|path>  Manage launchd service
  help                      Show this help`)
}

func doctor() error {
	fmt.Println("Running doctor checks...")
	ok := true

	check := func(name string, err error) {
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", name, err)
			ok = false
		} else {
			fmt.Printf("  ✓ %s\n", name)
		}
	}

	check("macOS", func() error {
		cmd := exec.Command("uname", "-s")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("failed to check OS: %w", err)
		}
		if string(out) != "Darwin\n" {
			return fmt.Errorf("expected Darwin, got %s", string(out))
		}
		return nil
	}())

	check("git installed", func() error {
		return exec.Command("git", "version").Run()
	}())

	check("gh installed", func() error {
		return exec.Command("gh", "version").Run()
	}())

	cfg := loadConfigForDoctor()

	check("gh auth status", func() error {
		ghClient := github.NewClient(cfg)
		return ghClient.AuthStatus()
	}())

	check("repo accessible", func() error {
		ghClient := github.NewClient(cfg)
		return ghClient.RepoAccessible()
	}())

	check("base branch exists", func() error {
		ghClient := github.NewClient(cfg)
		return ghClient.BaseBranchExists()
	}())

	check("opencode binary exists", func() error {
		_, err := exec.LookPath(cfg.OpenCodeBin)
		return err
	}())

	if err := cfg.ExpandPaths(); err == nil {
		check("workspace root writable", func() error {
			if err := os.MkdirAll(cfg.WorkspaceRoot, 0755); err != nil {
				return err
			}
			tmp := cfg.WorkspaceRoot + "/.write-test"
			if err := os.WriteFile(tmp, []byte("test"), 0644); err != nil {
				return err
			}
			os.Remove(tmp)
			return nil
		}())

		dbPath := cfg.WorkspaceRoot + "/runner.sqlite"
		check("SQLite path writable", func() error {
			f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			f.Close()
			return nil
		}())
	}

	fmt.Println()
	if ok {
		fmt.Println("All checks passed!")
	} else {
		fmt.Println("Some checks failed. See above for details.")
		os.Exit(1)
	}
	return nil
}

func loadConfigForDoctor() *config.Config {
	cfgPath := config.DefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Printf("  Note: config error, using defaults: %v\n", err)
		cfg = config.Default()
	}
	return cfg
}

func loadConfig() (*config.Config, error) {
	cfgPath := config.DefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func startDaemon() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	d, err := daemon.New(cfg)
	if err != nil {
		return fmt.Errorf("create daemon: %w", err)
	}

	return d.Start()
}

func showStatus() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if err := cfg.ExpandPaths(); err != nil {
		return err
	}

	dbPath := cfg.WorkspaceRoot + "/runner.sqlite"
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()

	ctx := context.Background()

	jobs, err := database.ListJobs(ctx)
	if err != nil {
		return err
	}

	var running, queued, failed, waiting, retry int
	for _, j := range jobs {
		switch {
		case j.State == "queued":
			queued++
		case j.State == "retry_scheduled":
			retry++
		case j.State == "failed" || j.State == "blocked":
			failed++
		case j.State == "waiting_for_review":
			waiting++
		case j.State == "running_agent" || j.State == "preparing_worktree" || j.State == "validating" ||
			j.State == "committing" || j.State == "pushing" || j.State == "creating_pr" ||
			j.State == "applying_pr_feedback" || j.State == "cleanup_running":
			running++
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Daemon Status:\n")
	fmt.Fprintf(w, "  Config:\t%s\n", cfg.ConfigPathUsed)
	fmt.Fprintf(w, "  Workspace:\t%s\n", cfg.WorkspaceRoot)
	fmt.Fprintf(w, "  Poll Interval:\t%d seconds\n", cfg.PollIntervalSeconds)
	fmt.Fprintf(w, "  Max Agents:\t%d\n", cfg.MaxConcurrentAgents)
	fmt.Fprintf(w, "  Dashboard:\thttp://%s\n", cfg.DashboardAddr)
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "Jobs:\n")
	fmt.Fprintf(w, "  Running:\t%d\n", running)
	fmt.Fprintf(w, "  Queued:\t%d\n", queued)
	fmt.Fprintf(w, "  Retry Scheduled:\t%d\n", retry)
	fmt.Fprintf(w, "  Waiting for Review:\t%d\n", waiting)
	fmt.Fprintf(w, "  Failed:\t%d\n", failed)
	fmt.Fprintf(w, "  Total:\t%d\n", len(jobs))
	w.Flush()

	return nil
}

func listJobs() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.ExpandPaths(); err != nil {
		return err
	}

	dbPath := cfg.WorkspaceRoot + "/runner.sqlite"
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()

	jobs, err := database.ListJobs(context.Background())
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tIssue\tType\tState\tAttempt\tBranch")
	fmt.Fprintln(w, "--\t-----\t----\t-----\t-------\t------")
	for _, j := range jobs {
		branch := "-"
		if j.Branch != nil {
			branch = *j.Branch
		}
		fmt.Fprintf(w, "%d\t#%d\t%s\t%s\t%d/%d\t%s\n",
			j.ID, j.IssueNumber, j.JobType, j.State, j.Attempt, j.MaxAttempts, branch)
	}
	w.Flush()
	return nil
}

func tailLogs(idStr string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.ExpandPaths(); err != nil {
		return err
	}

	// Try to find the log file by looking for job-<id> patterns
	logDir := cfg.LogDir
	files, err := os.ReadDir(logDir)
	if err != nil {
		return fmt.Errorf("read log dir: %w", err)
	}

	var logFile string
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "job-"+idStr+"-") {
			logFile = filepath.Join(logDir, f.Name())
			break
		}
	}

	if logFile == "" {
		return fmt.Errorf("no log file found for job %s", idStr)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		return fmt.Errorf("read log: %w", err)
	}
	fmt.Print(string(content))
	return nil
}

func plistCmd(action string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.ExpandPaths(); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.web3-avatar-agent-runner.daemon</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>start</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s/daemon.log</string>
	<key>StandardErrorPath</key>
	<string>%s/daemon.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin:%s</string>
	</dict>
	<key>WorkingDirectory</key>
	<string>%s</string>
</dict>
</plist>
`, exe, cfg.LogDir, cfg.LogDir, filepath.Dir(exe), cfg.WorkspaceRoot)

	switch action {
	case "install":
		home, _ := os.UserHomeDir()
		plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.web3-avatar-agent-runner.daemon.plist")
		if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
			return err
		}
		fmt.Println("Plist installed at", plistPath)
		return exec.Command("launchctl", "load", plistPath).Run()

	case "uninstall":
		home, _ := os.UserHomeDir()
		plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.web3-avatar-agent-runner.daemon.plist")
		exec.Command("launchctl", "unload", plistPath).Run()
		os.Remove(plistPath)
		fmt.Println("Plist uninstalled")
		return nil

	case "path":
		fmt.Print(plist)
		return nil

	default:
		return fmt.Errorf("unknown plist action: %s (use install, uninstall, or path)", action)
	}
}

func retryJob(idStr string) error {
	return updateJobState(idStr, "queued")
}

func cancelJob(idStr string) error {
	return updateJobState(idStr, "failed")
}

func cleanupJob(idStr string) error {
	return updateJobState(idStr, "cleanup_done")
}

func updateJobState(idStr, newState string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.ExpandPaths(); err != nil {
		return err
	}

	dbPath := cfg.WorkspaceRoot + "/runner.sqlite"
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()

	id := parseInt64(idStr)
	job, err := database.GetJob(context.Background(), id)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("job %s not found", idStr)
	}

	now := time.Now()
	database.UpdateJob(context.Background(), id, db.JobUpdate{
		State:      &newState,
		FinishedAt: &now,
	})

	fmt.Printf("Job %s updated to %s\n", idStr, newState)
	return nil
}

func parseInt64(s string) int64 {
	var id int64
	fmt.Sscanf(s, "%d", &id)
	return id
}


