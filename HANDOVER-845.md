# Handover: Issue #845 — Add zod/v4 enforcement across all packages

## Objective

Update all packages in the `web3-avatar` monorepo to use Zod v4 (`zod/v4` imports) consistently, and extend the existing import guard to cover all packages.

## Worktree

Location: `~/.local/share/web3-avatar-agent-runner/worktrees/issue-845/`
Branch: `agent/issue-845-high-add-zod-v4-enforcement-across-all-p`
Base: `master`

**No implementation work has been done yet.** Only `.opencode-prompt.md` was modified (by the agent runner framework).

## Current state

### package.json versions

| Package | Current zod version | File:line |
|---|---|---|
| `apps/cordex` | `^3.25.51` | apps/cordex/package.json:51 |
| `apps/discord-bot` | `^3.25.76` | apps/discord-bot/package.json:40 |
| `apps/worker` | `^3.22.4` | apps/worker/package.json:19 |
| `packages/server` | `^4.1.12` (correct) | packages/server/package.json:68 |
| `apps/portal` | none (no zod dep) | N/A |

### Files importing `zod` (not `zod/v4`) — 5 files to update

| File | Current import | Action |
|---|---|---|
| `apps/cordex/src/lib/environment.ts:1` | `import z from 'zod'` | → `zod/v4` |
| `apps/cordex/src/routes/auth/discord/callback.tsx:3` | `import { z } from 'zod'` | → `zod/v4` |
| `apps/discord-bot/src/config/environment.ts:1` | `import { z } from 'zod'` | → `zod/v4` |
| `apps/discord-bot/src/utils/hustle-session-cache.ts:4` | `import { z } from 'zod'` | → `zod/v4` |
| `apps/worker/src/env.ts:2` | `import z from 'zod'` | → `zod/v4` |

### Import guard script

File: `packages/server/scripts/check-zod-imports.mjs`

Currently only scans `packages/server/src/`. Needs to also scan:
- `apps/cordex/src/`
- `apps/discord-bot/src/`
- `apps/worker/src/`
- `apps/portal/src/`

The script logic is correct (flags `'zod'` and `'zod/v3'` imports), just needs multi-directory support.

## Required changes

1. **Update `apps/cordex/package.json`** — change `"zod": "^3.25.51"` to `"zod": "^4.1.12"` (line 51)
2. **Update `apps/discord-bot/package.json`** — change `"zod": "^3.25.76"` to `"zod": "^4.1.12"` (line 40)
3. **Update `apps/worker/package.json`** — change `"zod": "^3.22.4"` to `"zod": "^4.1.12"` (line 19)
4. **Update 5 import statements** in cordex, discord-bot, and worker source files (see table above)
5. **Extend `check-zod-imports.mjs`** — refactor to accept a list of source directories and run the check across all 5 packages

## Gotchas

- `apps/portal` has no zod dependency and no zod imports in source, but the issue action says to include it in the script anyway
- `apps/portal/src/` has no `import ... from 'zod'` — the script will pass cleanly for it
- Use the same zod version as server (`^4.1.12`) for consistency
- The check script uses `import.meta.dirname` (Node 21+), which works fine
- After changes, run `pnpm install` to update lockfile if CI requires it
- Do NOT commit or push — the agent runner handles that

## Verification

```bash
cd ~/.local/share/web3-avatar-agent-runner/worktrees/issue-845
pnpm run check:zod-imports
```
