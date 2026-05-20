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
agent-runner doctor        # verify prerequisites
agent-runner start          # start daemon
agent-runner status         # daemon health summary
agent-runner jobs           # list all jobs
agent-runner logs <job_id>  # tail logs for a job
agent-runner retry <job_id>
agent-runner cancel <job_id>
agent-runner cleanup <job_id>
```

## Implementation order (inferred from SPEC structure)

1. Module init + config loading (YAML + env overrides)
2. SQLite schema + migrations
3. GitHub polling (`gh issue list`, `gh pr list`, `gh api`)
4. Worktree management (clone, fetch, worktree add/remove, branch naming `agent/issue-<N>-<slug>`)
5. Job queue + worker pool (max 2, atomic SQLite claim)
6. Agent launch (`opencode run --model ... --prompt-file`)
7. Commit/push/PR creation flow
8. Retry logic
9. PR feedback loop (poll comments, enqueue feedback job)
10. Cleanup (worktree removal on merge/close)
11. Dashboard (HTMX + SSE)
12. `launchd` plist + install/uninstall

## Config

Loaded from `~/.config/web3-avatar-agent-runner/config.yaml`. Environment variables override.

Defaults in `SPEC.md:76-92`.

## Verification

```bash
agent-runner doctor
```
