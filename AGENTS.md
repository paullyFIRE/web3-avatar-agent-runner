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

## Gotchas (hard-earned)

- **opencode requires `run` subcommand**: `opencode run -m model -f file.md --dangerously-skip-permissions "message"` — omitting `run` drops into TUI mode and hangs.
- **Don't pipe prompts via stdin**: opencode doesn't read messages from stdin. Use `-f promptFile.md` to attach the prompt file.
- **`gh pr create` has no `--json`**: parse the URL from stdout instead. Use `--body-file -` and pipe body via stdin for multi-line PR bodies.
- **`modernc.org/sqlite` doesn't support `FOR UPDATE`**: remove it — SQLite's serialized transactions suffice for atomic job claiming.
- **Always `git worktree prune` + `-f`**: stale worktree entries crash `worktree add`. Prune before add, and use `-f` liberally.
- **Set `heartbeat_at` immediately**: any running state without heartbeat gets marked stale by the poller within 5 minutes. Set on every state transition + run a periodic goroutine.
- **Template composition in Go is brittle**: `ParseFS("*.html")` put all named templates in one namespace. `{{define "content"}}` in multiple files = last-one-wins. Use self-contained standalone templates instead. If templates need config data, build the FuncMap dynamically in `New()` instead of at package level.
- **`asdf` requires `asdf set`** (not `asdf local`) on newer versions. Add `.tool-versions` to pin Go version.
- **Portless wraps the binary**: run `portless agent-runner ./agent-runner start` for `https://agent-runner.localhost`. The daemon auto-detects `$PORT`, `$HOST`, and `$PORTLESS_URL` env vars. Install portless globally via `npm install -g portless`.
- **Worktree `.git` file breaks on repo re-clone**: worktree dirs contain a `.git` file pointing to `.git/worktrees/<name>/` in the main repo. If the main repo is re-cloned, that path dies. Always call `IsWorktreeDir` before using an existing worktree dir; if invalid, `os.RemoveAll` the stale dir and create fresh.
- **`CleanWorktree` ≠ `ResetToBase`**: `git checkout -- .` + `git clean -fd` only removes unstaged/untracked files — old **commits** remain. Use `git reset --hard origin/master` to wipe everything before re-running an agent on retry.
- **Dashboard FuncMap needs runtime config**: `template.FuncMap` is evaluated at init time. To pass config (e.g., repo URLs) into templates, build the FuncMap dynamically in `New()` using `parseTemplates(cfg)`.
- **Post-agent task idempotency**: never assume a phase failed just because the daemon died. Before committing, check `git rev-list HEAD ^origin/master`. Before pushing, check `git ls-remote --heads`. Before creating a PR, check if `job.PRNumber` is already set.
- **`IsWorktreeDir` needs deep validation**: `git rev-parse --git-dir` just reads the `.git` file but doesn't verify the resolved path exists. Stat the resolved git dir path too.
- **State audit via state_logs table**: every `UpdateJob` that changes `State` or sets `LastError` automatically logs to `state_logs`. The dashboard job detail page shows a timeline sidebar with timestamps so you can tell if an error is stale or fresh.
- **SQLite `datetime('now')` is UTC, Go local time**: `datetime('now')` in SQLite returns UTC, but Go's `time.Format` and `time.Parse` use local timezone. This causes staleness checks to fail by the timezone offset. Always use `datetime('now', 'localtime')` in SQL when comparing against Go-formatted timestamps.
- **`wtPath` shadowing bug**: Using `:=` inside an inner `if` block shadows the outer variable. Always use `=` when assigning to a variable from an outer scope.
- **Commit message body line length**: Pre-commit hooks (commitlint) enforce `body-max-line-length` (400 chars). Agent summaries with long markdown lines get rejected. Truncate the summary AND each line to fit within limits.
- **Force push on PR update**: When pushing new commits to an existing PR branch, use `git push --force-with-lease` because the remote branch has different old commits. Plain `git push` fails.
- **`NeedsClarification` false positive**: The agent output sometimes uses "clarification" as part of its summary text (e.g., "build env clarification"). Check for exact phrases like "clarification needed" or "needs clarification" rather than any mention of the word. Also negate — "no clarification needed" should not trigger.
- **Stream agent logs in real-time**: Pass a `logPath` to `agent.Run()` which uses `io.MultiWriter` to tee stdout/stderr to both the in-memory buffer (for parsing) and a file (for live dashboard viewing). No more waiting until the agent finishes to see output.

## Config

Loaded from `~/.config/web3-avatar-agent-runner/config.yaml`. Environment variables override.

Defaults in `SPEC.md:76-92`.

## Verification

```bash
agent-runner doctor
```
