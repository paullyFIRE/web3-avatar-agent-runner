package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/agent"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/config"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/dashboard"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/db"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/github"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/poller"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/worker"
	"github.com/paullyFIRE/web3-avatar-agent-runner/internal/worktree"
)

type Daemon struct {
	cfg      *config.Config
	db       *db.DB
	gh       *github.Client
	wt       *worktree.Manager
	agt      *agent.Runner
	pool     *worker.Pool
	poller   *poller.Poller
	dash     *dashboard.Server
	ctx      context.Context
	cancel   context.CancelFunc
}

func New(cfg *config.Config) (*Daemon, error) {
	if err := cfg.ExpandPaths(); err != nil {
		return nil, fmt.Errorf("expand paths: %w", err)
	}

	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	logFile := filepath.Join(cfg.LogDir, "daemon.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}

	w := io.MultiWriter(f, os.Stdout)
	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dbPath := filepath.Join(cfg.WorkspaceRoot, "runner.sqlite")
	database, err := db.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	ghClient := github.NewClient(cfg)
	wtMgr := worktree.NewManager(cfg)
	agtRunner := agent.NewRunner(cfg)
	pool := worker.NewPool(cfg, database, ghClient, wtMgr, agtRunner)
	poll := poller.New(cfg, database, ghClient, pool)
	dashServer := dashboard.New(cfg, database)

	ctx, cancel := context.WithCancel(context.Background())

	return &Daemon{
		cfg:    cfg,
		db:     database,
		gh:     ghClient,
		wt:     wtMgr,
		agt:    agtRunner,
		pool:   pool,
		poller: poll,
		dash:   dashServer,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (d *Daemon) Start() error {
	slog.Info("daemon starting",
		"workspace_root", d.cfg.WorkspaceRoot,
		"dashboard", d.cfg.DashboardAddr,
		"poll_interval", d.cfg.PollIntervalSeconds,
	)

	d.pool.Start(d.ctx)

	go d.poller.Run(d.ctx)

	dashAddr := d.cfg.DashboardAddr
	dashServer := &http.Server{
		Addr:    dashAddr,
		Handler: d.dash.Routes(),
	}

	go func() {
		slog.Info("dashboard listening", "addr", dashAddr)
		if err := dashServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard error", "error", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("received signal", "signal", sig)
	case <-d.ctx.Done():
	}

	d.shutdown(dashServer)
	return nil
}

func (d *Daemon) Stop() {
	d.cancel()
}

func (d *Daemon) shutdown(srv *http.Server) {
	slog.Info("shutting down daemon")

	d.cancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("dashboard shutdown", "error", err)
	}

	d.db.Close()
	slog.Info("daemon stopped")
}
