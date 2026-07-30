<!-- agent-team:handoff:start -->
<!-- agent-team:handoff:version=2026-07-30-rpt1 -->
## Agent-Team Peer Handoff

At the start of every session in this repo:

1. Run `agent-team handoff-check` — a fail-closed preflight (sync state, not on main, clean tree); add `--ff` to also fast-forward `main`. It does not replace `git-sync`/`git pull --ff-only`, which also move un-pushed peer work.
2. Read PROGRESS.md, especially the Status Board, before choosing work.
3. Before claiming, run `agent-team handoff-check <task-slug>` to catch an existing claim branch; then set the task owner + in-progress in PROGRESS.md and push the claim branch.
4. Before stopping, update Status Board and append a Handoff Log entry.

Codex CLI does not expand @PROGRESS.md inside AGENTS.md. Treat this as an explicit instruction to read PROGRESS.md with tools; do not rely on native include/import behavior.

### Report agent-team failures upstream

If agent-team's own machinery fails—for example `stamp-handoff`, `upgrade`, `handoff-check`, a wrapper, reviewer routing, audit, or these managed instructions—separate that defect from the project bug and gather only minimal evidence: the command, sanitized `agent-team detect .` output, `TEAM_RESULT` / exact error, and expected versus actual behavior. Replace the repository root with `.`, and remove usernames, hostnames, and project identifiers unless they are essential.

Prepare a report with the [upstream downstream-report template](https://github.com/MicHuang/agent-team/issues/new?template=downstream-report.md). If GitHub write access is unavailable, prepare the same copy-paste draft. Before creating the external issue, show the exact draft to the user and get explicit confirmation. Do not run `gh`, call an API, or submit through a browser until the user confirms that exact draft.

Never send full PROGRESS.md, project source or business content, secrets, or private paths and identities unless the user explicitly approves the minimal necessary excerpt. The upstream issue is canonical; keep only its link and a short status in local PROGRESS.md.
<!-- agent-team:handoff:end -->

## Project Context

Read `CLAUDE.md` and `README.md` directly before changing code. Codex CLI does
not expand file includes from this file, so treat those as explicit files to
open with tools rather than implicit imports.
