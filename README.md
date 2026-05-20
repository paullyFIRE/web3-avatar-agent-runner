# web3-avatar-agent-runner

Local daemon that polls GitHub for issues labeled `agent-ready`, runs OpenCode Go agents to implement fixes, and opens PRs. No webhooks, no cloud runners, no GitHub Actions — just `gh` CLI and a local Go binary.

## Prerequisites

- **Go 1.26+**
- **gh CLI** — authenticated against GitHub
- **opencode binary** — on `$PATH` or configured in `opencode_bin`
- **git**

## Quick start

```bash
# Build
make build

# Verify everything is set up
make doctor

# Start the daemon (runs until Ctrl+C)
make run
```

The daemon polls GitHub every 30s. Open the dashboard at [http://127.0.0.1:8123](http://127.0.0.1:8123).

## Configuration

Config is loaded from `~/.config/web3-avatar-agent-runner/config.yaml`. Environment variables override.

```yaml
github_owner: paullyFIRE
github_repo: web3-avatar
ready_label: agent-ready
authorized_commenter: paullyFIRE
base_branch: master
poll_interval_seconds: 30
max_concurrent_agents: 2
retry_limit: 2
retry_backoff_seconds: 3600
opencode_bin: opencode
opencode_model: opencode-go/deepseek-v4-flash
dashboard_addr: 127.0.0.1:8123
```

See defaults in `internal/config/config.go:38-73`.

## CLI commands

| Command | Description |
|---|---|
| `doctor` | Verify all prerequisites (gh auth, git, opencode, paths) |
| `start` | Start the daemon (poller + worker pool + dashboard) |
| `status` | Show daemon health and job counts |
| `jobs` | List all jobs |
| `logs <id>` | Print logs for a specific job |
| `retry <id>` | Retry a failed job |
| `cancel <id>` | Cancel a queued or running job |
| `cleanup <id>` | Force cleanup a job's worktree |
| `plist install` | Install and load a launchd service |
| `plist uninstall` | Unload and remove the launchd service |
| `plist path` | Print the plist XML |

## How it works

1. Every 30s the poller queries GitHub for open issues labeled `agent-ready`
2. Each new issue gets a queued `implement_issue` job, a git worktree under `~/.local/share/web3-avatar-agent-runner/worktrees/`, and a branch named `agent/issue-<N>-<slug>`
3. The worker pool runs up to 2 agents concurrently — each invokes `opencode` with a prompt containing the issue details
4. The wrapper handles: `git add/commit/push` and `gh pr create`
5. PRs target `master`, are ready-for-review (never draft), and never request `paullyFIRE` as reviewer
6. PR timeline comments from `paullyFIRE` trigger feedback jobs; all other users are ignored
7. Merged or closed PRs trigger automatic worktree cleanup
8. Failed jobs retry automatically up to 2 times with a 1-hour backoff

## Project structure

```
cmd/agent-runner/main.go          Entrypoint
internal/
  config/   YAML config + env overrides
  db/       SQLite schema + queries
  github/   gh CLI wrapper
  worktree/ git worktree management
  agent/    opencode process runner
  worker/   job pool + implement/feedback/cleanup flows
  poller/   30s polling loop
  daemon/   orchestrator + graceful shutdown
  dashboard/ Tailwind CDN + HTMX web UI
  cli/      command dispatch
```

## launchd service

```bash
# Install as a user launchd agent (starts on login)
agent-runner plist install

# Remove
agent-runner plist uninstall
```

## Security

Controlled local automation with explicit guardrails:
- Only processes issues labeled `agent-ready`
- Only responds to PR comments from `paullyFIRE`
- Wrapper owns all destructive operations (commit, push, PR creation)
- Protected path blocklist: `.github/workflows/`, `.env*`, secrets, infra, terraform, k8s, helm
- Non-bypassable pre-commit/pre-push hooks
- No auto-merge, no draft PRs, no reviewer requests
- Retry limits prevent runaway agents
