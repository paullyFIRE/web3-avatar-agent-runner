# web3-avatar-agent-runner

Local daemon that polls GitHub issues, runs OpenCode Go agents to implement fixes, and opens PRs.

## Architecture

- **Language**: Go. Single binary (`agent-runner`).
- **Persistence**: SQLite via `runner.sqlite`.
- **Dashboard**: Go server-rendered HTML + Tailwind + HTMX.
- **GitHub**: Local `gh` CLI only — no API tokens, no webhooks, no GitHub Actions.
- **Agent**: `opencode` binary invoked per-issue with `--prompt-file`.
- **Daemon**: macOS `launchd` plist.

## Key design decisions (from SPEC.md)

- Polls GitHub every 30s for issues labeled `agent-ready`; never uses webhooks.
- One git worktree per issue under `~/.local/share/web3-avatar-agent-runner/worktrees/`.
- Wrapper owns `git add/commit/push`, `gh pr create`, cleanup — agent only implements.
- Max 2 concurrent agents. Retry limit 2, backoff 1h.
- PRs target `master`, ready-for-review (never draft), never request `paullyFIRE` as reviewer.
- PR timeline comments from `paullyFIRE` trigger feedback jobs; all other users ignored.
- Conventional commits. Protected path blocklist prevents changes to `.github/workflows/`, `.env*`, `secrets/`, `infra/`, `terraform/`, `k8s/`, `helm/`.

## CLI commands

```
agent-runner doctor                  # verify prerequisites
agent-runner start                    # start daemon
agent-runner status                   # daemon health summary
agent-runner jobs                     # list all jobs
agent-runner logs <job_id>            # tail logs for a job
agent-runner retry <job_id>
agent-runner cancel <job_id>
agent-runner cleanup <job_id>
agent-runner plist <install|uninstall|path>  # manage launchd service
```

## Project structure

```
cmd/agent-runner/main.go
internal/
  config/   YAML config + env overrides
  db/       SQLite schema + queries for jobs, comments, agents, attempts, poll_state
  github/   gh CLI wrapper (issues, PRs, comments, PR creation)
  worktree/ clone, fetch, worktree add/remove, branch naming agent/issue-<N>-<slug>
  agent/    opencode process runner with prompt generation
  worker/   job pool (max 2), implement/feedback/cleanup flows, retry logic
  poller/   30s ticker polls issues, PRs, comments, stale jobs
  daemon/   orchestrator with graceful shutdown, dashboard HTTP server
  dashboard/ Tailwind CDN + HTMX server-rendered routes
  cli/      command dispatch for doctor, start, status, jobs, logs, retry, cancel, cleanup, plist
```

## Config

Loaded from `~/.config/web3-avatar-agent-runner/config.yaml`. Environment variables override.

Defaults in `SPEC.md:76-92`.

## Verification

```bash
agent-runner doctor
```
