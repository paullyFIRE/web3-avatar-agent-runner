You are a senior Go systems engineer implementing a local GitHub issue-to-PR coding-agent runner.

Build a production-grade local daemon for this repository:

Repository: https://github.com/paullyFIRE/web3-avatar
Owner: paullyFIRE
Repo: web3-avatar
Base branch: master
Ready label: agent-ready
Authorized commenter: paullyFIRE

The daemon must run locally on macOS. It must poll GitHub every 30 seconds using the local `gh` utility, find open issues labeled `agent-ready`, create one git worktree per issue, run OpenCode Go locally using model `opencode-go/deepseek-v4-flash`, implement the fix, rely on repo hooks for checks/tests, commit using Conventional Commits, push a branch, open a ready-for-review PR into `master`, respond to PR timeline comments from `paullyFIRE`, and clean up local worktrees when PRs are merged or closed.

Do not use GitHub Actions.
Do not use cloud runners.
Do not use webhooks.
Use polling every 30 seconds.

Implementation language: Go.
Persistence: SQLite.
Dashboard: server-rendered Tailwind + HTMX.
Live logs: required.
Prometheus/metrics: not required.

Target local machine:
- macOS
- MacBook Pro
- Apple M5
- 10 CPU cores
- 32 GB RAM
- Network access allowed
- GitHub access via local `gh`
- Agent runtime: OpenCode Go
- OpenCode model: `opencode-go/deepseek-v4-flash`

Core constraints:
- Use `gh` locally for GitHub access.
- Poll every 30 seconds.
- Branch from `origin/master`.
- PRs target `master`.
- PRs must be ready for review, not draft.
- Never request `paullyFIRE` as reviewer.
- Max concurrent agents: 2.
- One issue per agent.
- Auto-retry failed jobs.
- Retry limit: 2 retries after the initial attempt.
- Retry backoff: 1 hour.
- On retry or daemon restart, resume from existing worktree if present.
- Cleanup local worktree on PR merge.
- Cleanup local worktree on PR closed without merge.
- Leave remote branches intact.
- Rely on repo pre-commit and pre-push hooks for repo checks/tests.
- Do not bypass hooks.
- Any PR timeline comment from `paullyFIRE` should trigger a follow-up agent run.
- Ignore PR comments from all other users.
- Reply to PR comments when the agent starts and when it finishes.
- Dashboard must show all status fields.
- Dashboard must stream live logs.

Recommended workspace:

~/.local/share/web3-avatar-agent-runner

The runner must not live inside the `web3-avatar` repository. It should maintain its own canonical clone and worktrees:

~/.local/share/web3-avatar-agent-runner/
  repo/
  worktrees/
    issue-<number>/
  logs/
  runner.sqlite
  config.yaml

Default configuration:

GITHUB_OWNER=paullyFIRE
GITHUB_REPO=web3-avatar
REPO_URL=https://github.com/paullyFIRE/web3-avatar
READY_LABEL=agent-ready
AUTHORIZED_COMMENTER=paullyFIRE
DISALLOWED_REVIEWER=paullyFIRE
BASE_BRANCH=master
POLL_INTERVAL_SECONDS=30
MAX_CONCURRENT_AGENTS=2
RETRY_LIMIT=2
RETRY_BACKOFF_SECONDS=3600
DELETE_REMOTE_BRANCH_ON_CLEANUP=false
CLEANUP_ON_MERGE=true
CLEANUP_ON_CLOSED=true
OPENCODE_BIN=opencode
OPENCODE_MODEL=opencode-go/deepseek-v4-flash
WORKSPACE_ROOT=~/.local/share/web3-avatar-agent-runner

Support config from:

~/.config/web3-avatar-agent-runner/config.yaml

Environment variables may override config values.

────────────────────────────────────────
1. Polling model
────────────────────────────────────────

Do not implement webhook ingestion.

Implement a poller that runs every 30 seconds.

The poller must discover:

1. open issues labeled `agent-ready`;
2. open PRs created by this runner;
3. new PR timeline comments by `paullyFIRE`;
4. closed or merged PRs for cleanup;
5. stale/running jobs that need recovery.

Use `gh` commands or `gh api`.

Issue polling command shape:

gh issue list \
  --repo paullyFIRE/web3-avatar \
  --state open \
  --label agent-ready \
  --json number,title,body,labels,author,comments,updatedAt,url

For each matching issue:

- ignore if an active job already exists;
- ignore if a PR already exists for branch `agent/issue-<number>-<slug>`;
- ignore if issue is not open;
- enqueue implementation job.

Poll PRs created by this runner:

- identify branches matching `agent/issue-*`;
- map PRs to issue numbers;
- detect PR state;
- detect merge/close for cleanup.

Poll PR timeline comments:

- only process comments from `paullyFIRE`;
- ignore all other users;
- only process new comments not recorded in `processed_comments`;
- enqueue feedback job for existing PR branch;
- reply immediately that the local agent has started processing the feedback.

Do not respond to the runner’s own comments in a loop.

────────────────────────────────────────
2. Repository and worktree layout
────────────────────────────────────────

Use this local layout:

WORKSPACE_ROOT/
  repo/
  worktrees/
    issue-<number>/
  logs/
    job-<id>-attempt-<n>.log
  runner.sqlite
  config.yaml

Canonical checkout:

WORKSPACE_ROOT/repo

Per-issue worktree:

WORKSPACE_ROOT/worktrees/issue-<issue_number>

If canonical checkout does not exist:

git clone https://github.com/paullyFIRE/web3-avatar WORKSPACE_ROOT/repo

Before creating a worktree:

git fetch origin master --prune

Branch naming:

agent/issue-<issue_number>-<slug>

Examples:

agent/issue-17-fix-avatar-upload
agent/issue-23-add-wallet-validation

Initial implementation:

- create worktree from origin/master;
- create branch if it does not already exist.

Retry/restart:

- if worktree exists, reuse it;
- if worktree is missing but local branch exists, recreate worktree from local branch;
- if local branch is missing but remote branch exists, recreate from remote branch;
- never overwrite unrelated work;
- never force-push by default.

Feedback job:

- reuse the existing worktree;
- if missing, recreate from PR branch.

────────────────────────────────────────
3. SQLite state
────────────────────────────────────────

Use SQLite for durable state.

Required tables:

jobs:
- id INTEGER PRIMARY KEY
- repo_owner TEXT
- repo_name TEXT
- issue_number INTEGER
- pr_number INTEGER NULL
- branch TEXT
- worktree_path TEXT
- job_type TEXT
  - implement_issue
  - apply_pr_feedback
  - cleanup
- state TEXT
  - queued
  - preparing_worktree
  - running_agent
  - validating
  - committing
  - pushing
  - creating_pr
  - pr_opened
  - waiting_for_review
  - applying_pr_feedback
  - retry_scheduled
  - needs_clarification
  - blocked
  - failed
  - merged
  - closed_without_merge
  - cleanup_running
  - cleanup_done
- current_phase TEXT
- attempt INTEGER
- max_attempts INTEGER
- next_retry_at DATETIME NULL
- pid INTEGER NULL
- heartbeat_at DATETIME NULL
- last_log_line TEXT
- last_error TEXT
- model TEXT
- trigger_comment_id TEXT NULL
- created_at DATETIME
- updated_at DATETIME
- started_at DATETIME NULL
- finished_at DATETIME NULL

processed_comments:
- id INTEGER PRIMARY KEY
- repo_owner TEXT
- repo_name TEXT
- pr_number INTEGER
- comment_id TEXT
- sender_login TEXT
- processed_at DATETIME
- UNIQUE(repo_owner, repo_name, comment_id)

poll_state:
- key TEXT PRIMARY KEY
- value TEXT
- updated_at DATETIME

job_attempts:
- id INTEGER PRIMARY KEY
- job_id INTEGER
- attempt INTEGER
- state TEXT
- log_path TEXT
- started_at DATETIME
- finished_at DATETIME
- exit_code INTEGER NULL
- error TEXT NULL

agents:
- id INTEGER PRIMARY KEY
- job_id INTEGER
- pid INTEGER
- state TEXT
- started_at DATETIME
- heartbeat_at DATETIME
- model TEXT
- worktree_path TEXT

Add indexes for:

- active jobs
- retry_scheduled jobs
- issue_number
- pr_number
- processed_comments.comment_id
- branch

Enforce idempotency:

- one active implementation job per issue;
- one active feedback job per PR comment;
- duplicate polls must not create duplicate jobs.

────────────────────────────────────────
4. Worker concurrency
────────────────────────────────────────

Run at most 2 agents at once.

MAX_CONCURRENT_AGENTS=2.

Each worker handles exactly one issue or one PR feedback job.

Use atomic SQLite job claiming:

- claim oldest queued job;
- respect retry schedule;
- skip if active agent count >= 2;
- update state and heartbeat durably.

A heartbeat must be written while an agent is running.

On daemon restart:

- detect jobs in active states;
- if PID is dead, recover;
- resume from existing worktree when possible;
- schedule retry if safe;
- mark failed if retries exhausted.

Active states:

- preparing_worktree
- running_agent
- validating
- committing
- pushing
- creating_pr
- applying_pr_feedback
- cleanup_running

Terminal states:

- waiting_for_review
- cleanup_done
- failed
- blocked
- needs_clarification
- closed_without_merge
- merged

────────────────────────────────────────
5. Retry policy
────────────────────────────────────────

Failures should retry automatically.

Retry limit:

- 2 retries after the initial attempt.

Backoff:

- 1 hour.

Retry behavior:

- preserve worktree;
- preserve logs;
- append attempt-specific logs;
- resume from existing worktree;
- pass previous failure/hook output back into OpenCode if appropriate;
- do not delete or reset local work unless explicitly configured.

Retry on:

- OpenCode non-zero exit;
- transient network failure;
- `gh` API failure;
- push failure;
- hook failure;
- PR creation failure.

Do not retry automatically on:

- protected file modification;
- ambiguous issue requiring clarification;
- no changes produced;
- authorization error;
- repo not accessible;
- missing OpenCode binary.

────────────────────────────────────────
6. OpenCode Go invocation
────────────────────────────────────────

Run OpenCode Go locally.

Use:

OPENCODE_BIN=opencode
OPENCODE_MODEL=opencode-go/deepseek-v4-flash

Default command template:

opencode run --model opencode-go/deepseek-v4-flash --prompt-file {{prompt_file}}

Expose this as configurable:

OPENCODE_COMMAND_TEMPLATE="opencode run --model opencode-go/deepseek-v4-flash --prompt-file {{prompt_file}}"

If the local OpenCode Go CLI uses different syntax, the runner must allow changing this template without recompilation.

The agent process should:

- run inside the issue worktree;
- receive minimal environment variables;
- have network access;
- not receive GitHub credentials unless unavoidable;
- not own commit/push/PR creation.

The wrapper owns:

- git add;
- git commit;
- git push;
- gh pr create;
- gh pr comment;
- cleanup.

Implementation prompt:

You are running locally inside a git worktree for GitHub issue #<issue_number>.

Repository:
paullyFIRE/web3-avatar

Base branch:
master

Current branch:
<branch>

Issue title:
<title>

Issue body:
<body>

Issue comments:
<comments>

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
- Never request `paullyFIRE` as reviewer.
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

PR feedback prompt:

You are running locally inside an existing PR branch for GitHub PR #<pr_number>, issue #<issue_number>.

Authorized commenter `paullyFIRE` posted this PR timeline comment:

<comment>

Existing PR context:
<title/body/recent comments>

Rules:
- Address only the requested feedback.
- Preserve prior implementation unless a change is required.
- Do not commit.
- Do not push.
- Do not create a new PR.
- Do not request reviewers.
- Never request `paullyFIRE` as reviewer.
- Follow repository conventions.
- Keep the diff minimal.
- Do not bypass hooks.

Return:
- summary of changes;
- files changed;
- whether additional clarification is needed.

────────────────────────────────────────
7. Conventional commits and PR titles
────────────────────────────────────────

Use Conventional Commits for commit messages and PR titles.

Recommended PR title format:

<type>(<scope>): resolve issue #<issue_number>

If scope is unclear:

<type>: resolve issue #<issue_number>

Default type inference:

- bug, broken, error, failure, incorrect, crash -> fix
- new behavior, enhancement, add support -> feat
- test-only change -> test
- internal cleanup -> refactor
- build/tooling/config -> chore
- documentation-only -> docs
- fallback -> fix

Examples:

fix: resolve issue #17
feat(wallet): resolve issue #22
test(avatar): resolve issue #31
refactor(api): resolve issue #44

Commit message body should include:

- issue number;
- short summary;
- generated by local OpenCode Go runner.

Do not use vague commit messages like:

- update files
- fix stuff
- changes
- agent work

────────────────────────────────────────
8. Validation and hooks
────────────────────────────────────────

The repository has pre-commit and pre-push hooks for repo checks/tests.

Rely on hooks.

Do not bypass hooks.

Flow:

1. inspect git status;
2. inspect git diff;
3. block protected file changes;
4. git add -A;
5. git commit -m "<conventional commit title>" -m "<body>";
6. allow pre-commit hook to run;
7. git push origin <branch>;
8. allow pre-push hook to run;
9. capture hook output in logs;
10. if hooks fail, mark attempt failed and retry if attempts remain.

Do not run explicit test commands unless configured later.

If no changes exist:

- comment on the issue/PR;
- mark no_change or needs_clarification;
- do not create a PR.

────────────────────────────────────────
9. Protected files
────────────────────────────────────────

Default protected paths:

- .github/workflows/**
- .env
- .env.*
- secrets/**
- infra/**
- terraform/**
- k8s/**
- helm/**

If modified:

- mark job blocked;
- do not commit;
- do not push;
- comment explaining that protected files were modified and human intervention is required.

Allow configuration override.

────────────────────────────────────────
10. PR creation
────────────────────────────────────────

After successful commit and push:

1. check if PR already exists for the branch;
2. if yes, record PR number and move to waiting_for_review;
3. if no, create PR using `gh pr create`.

Create ready-for-review PRs only.

Do not pass draft flags.

Do not request reviewers.

Never request `paullyFIRE` as reviewer.

PR body:

## Summary
<agent summary>

## Validation
- Pre-commit hook: passed
- Pre-push hook: passed

## Risk
<low|medium|high + reason>

## Agent
- Runner: local OpenCode Go
- Model: opencode-go/deepseek-v4-flash
- Machine: local MacBook Pro Apple M5, 32 GB RAM
- Attempts: <n>
- Branch: <branch>

Closes #<issue_number>

No auto-merge will occur.

After PR creation:

- comment on the issue with the PR URL;
- mark job waiting_for_review.

────────────────────────────────────────
11. PR feedback handling
────────────────────────────────────────

Poll PR timeline comments every 30 seconds.

For each open PR created by this runner:

- fetch PR comments;
- process only comments by `paullyFIRE`;
- ignore comments by everyone else;
- ignore comments already recorded in processed_comments;
- ignore bot/runner comments to avoid loops.

When a valid comment is found:

1. record processed comment id;
2. comment on the PR:
   "Local agent accepted this feedback and is starting a follow-up run.";
3. enqueue apply_pr_feedback job;
4. reuse existing worktree or recreate from branch;
5. run OpenCode Go with comment context;
6. block protected path changes;
7. commit with Conventional Commit title;
8. push to same branch;
9. reply to PR with:
   - summary;
   - hook result;
   - commit SHA;
   - whether more input is needed.

Do not create a second PR.

Do not request reviewers.

────────────────────────────────────────
12. Cleanup
────────────────────────────────────────

Poll closed PRs every 30 seconds.

If PR was created by this runner and is merged:

- remove local worktree;
- run git worktree prune;
- mark merged;
- mark cleanup_done;
- keep logs;
- keep SQLite history;
- leave remote branch intact.

If PR was closed without merge:

- remove local worktree;
- run git worktree prune;
- mark closed_without_merge;
- mark cleanup_done;
- keep logs;
- keep SQLite history;
- leave remote branch intact.

Cleanup must be idempotent.

Manual command:

agent-runner cleanup <job_id>

────────────────────────────────────────
13. Dashboard
────────────────────────────────────────

Build a basic web dashboard in Go using server-rendered HTML, Tailwind, HTMX, and SQLite.

Routes:

GET /dashboard
GET /jobs
GET /jobs/:id
GET /jobs/:id/logs
GET /agents
POST /jobs/:id/retry
POST /jobs/:id/cancel
POST /jobs/:id/cleanup

Use HTMX polling or SSE for live updates.

Required fields:

- job id
- repo
- issue number
- PR number
- job type
- state
- current phase
- branch
- worktree path
- attempt count
- max attempts
- next retry time
- started at
- elapsed time
- heartbeat time
- PID
- model
- last log line
- last error
- queued/running/waiting/failed classification

Live logs:

- stream from the attempt log file;
- dashboard should update without full page reload;
- CLI should also support tailing logs.

────────────────────────────────────────
14. CLI
────────────────────────────────────────

Provide a CLI binary:

agent-runner doctor
agent-runner start
agent-runner status
agent-runner jobs
agent-runner logs <job_id>
agent-runner retry <job_id>
agent-runner cancel <job_id>
agent-runner cleanup <job_id>

doctor must verify:

- macOS environment;
- git installed;
- gh installed;
- gh auth status succeeds;
- repo accessible;
- base branch master exists;
- opencode binary exists;
- OpenCode command template works or is configured;
- SQLite path writable;
- workspace root writable.

status must show:

- current daemon health;
- poll interval;
- running agents;
- queued jobs;
- retry-scheduled jobs;
- failed jobs;
- waiting-for-review jobs.

────────────────────────────────────────
15. macOS service
────────────────────────────────────────

Provide a launchd plist for local daemon operation.

The daemon should:

- start on login or boot, depending on installation choice;
- restart on crash;
- write logs to WORKSPACE_ROOT/logs/daemon.log;
- use config from:

~/.config/web3-avatar-agent-runner/config.yaml

Provide install/uninstall commands or scripts.

────────────────────────────────────────
16. Security model
────────────────────────────────────────

Threats:

- issue prompt injection;
- PR comment prompt injection;
- unauthorized commenter triggering work;
- local filesystem escape;
- destructive git operations;
- protected path changes;
- duplicate polling events;
- infinite feedback loops;
- accidental reviewer request;
- corrupted worktree after crash;
- network loss during push/PR creation.

Required mitigations:

- only process issues with label agent-ready;
- only process comments from paullyFIRE;
- wrapper owns commits/push/PR creation;
- never request paullyFIRE as reviewer;
- never auto-merge;
- no hook bypass;
- protected path blocklist;
- SQLite idempotency constraints;
- processed_comments table;
- branch naming restricted to agent/issue-*;
- no force-push by default;
- restart recovery;
- retry limit;
- durable logs;
- local worktree cleanup only after PR closed/merged.

Do not claim full security. Describe this as controlled local automation with explicit guardrails.

────────────────────────────────────────
17. Acceptance criteria
────────────────────────────────────────

Implementation is acceptable only if:

- `agent-runner doctor` passes on the target Mac.
- The daemon polls every 30 seconds.
- An open issue labeled `agent-ready` creates exactly one queued job.
- Max two agents run simultaneously.
- Each agent works on exactly one issue.
- Each issue gets its own worktree.
- Worktrees branch from origin/master.
- Retry happens automatically up to two retries.
- Retry waits one hour.
- Retry resumes from existing worktree.
- OpenCode Go runs locally using `opencode-go/deepseek-v4-flash`.
- Pre-commit/pre-push hooks are not bypassed.
- Commits use Conventional Commits.
- PR titles use the recommended Conventional Commit format.
- PRs target master.
- PRs are ready for review, not draft.
- No reviewer request is made for paullyFIRE.
- PR timeline comments from paullyFIRE trigger follow-up work.
- Comments from other users are ignored.
- The runner replies when feedback work starts and finishes.
- Merged PRs clean up local worktrees.
- Closed-unmerged PRs clean up local worktrees.
- Remote branches are left intact.
- Dashboard shows all required status fields.
- Dashboard streams live logs.
- Restart recovery is safe.
- Internet loss does not corrupt state.
- Duplicate polls do not create duplicate jobs.
- Protected file changes are blocked by default.