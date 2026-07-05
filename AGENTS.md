<!-- agent-team:handoff:start -->
<!-- agent-team:handoff:version=2026-06-28-u1 -->
## Agent-Team Peer Handoff

At the start of every session in this repo:

1. Run `agent-team handoff-check` — a fail-closed preflight (sync state, not on main, clean tree); add `--ff` to also fast-forward `main`. It does not replace `git-sync`/`git pull --ff-only`, which also move un-pushed peer work.
2. Read PROGRESS.md, especially the Status Board, before choosing work.
3. Before claiming, run `agent-team handoff-check <task-slug>` to catch an existing claim branch; then set the task owner + in-progress in PROGRESS.md and push the claim branch.
4. Before stopping, update Status Board and append a Handoff Log entry.

Codex CLI does not expand @PROGRESS.md inside AGENTS.md. Treat this as an explicit instruction to read PROGRESS.md with tools; do not rely on native include/import behavior.
<!-- agent-team:handoff:end -->

## Project Context

Read `CLAUDE.md` and `README.md` directly before changing code. Codex CLI does
not expand file includes from this file, so treat those as explicit files to
open with tools rather than implicit imports.
