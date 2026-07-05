# PROGRESS - twincut

<!-- agent-team:progress-schema=2026-06-28-u1 -->

> Peer-handoff file. Start by reading Status Board; finish by updating Status Board and appending Handoff Log.
> Protocol: agent-team/docs/peer-handoff.md. Owner handles: claude / codex.
> Leader instance = `<handle>@<machine>` (which tool on which machine); use it in Handoff Log titles.

---

## Status Board  _(overwrite this section to reflect current reality)_

**Current milestone:** Sync/restamp and documentation hygiene.

### Task table

| # | 任务 | owner | status | 备注 |
|---|------|-------|--------|------|
| T1 | Sync to Stage 11 baseline | codex@macmini-yiqi | done | Preserved earlier stamp in stash `codex-pre-sync-stamp`, fetched origin, and created branch `codex/sync-restamp-hygiene` from `origin/main`. |
| T2 | Re-apply agent-team stamp | codex@macmini-yiqi | done | Re-ran `agent-team stamp-handoff /Users/zhabs/Tools/twincut` on Stage 11; generated `PROGRESS.md`, `AGENTS.md`, and updated `CLAUDE.md`. |
| T3 | Refresh stale project docs | codex@macmini-yiqi | done | Updated `CLAUDE.md`, `README.md`, and `AGENTS.md` so they reflect the Go UI, Makefile/test suite, and Stage 11 typed event contract. |
| T4 | Hygiene cleanup | codex@macmini-yiqi | done | Removed tracked generated quarantine CSV headers under `installers/_QUARANTINE/`. |

### Task DoD convention
- Each pending/in-progress task must name verification commands, manual checks, service/credential dependencies, and what counts as blocked.
- Blocked entries must include the blocking reason, verified facts, next suggested command, and whether user input is required.

### Next up
- Review draft PR #15: https://github.com/MicHuang/twincut/pull/15
- Optional follow-up: install Pillow/imagehash locally if full pHash smoke coverage is needed outside CI.

### How to claim
- Run `agent-team handoff-check <task-slug>` first (sync state + existing-claim check), then branch `<handle>/<task>` off synced main, set owner + in-progress, commit `chore(progress): claim <task>`, and push the branch before working. See agent-team/docs/peer-handoff.md §3.

### Blocked / waiting on
- None currently. Local pHash-dependent smoke sections may skip unless Pillow/imagehash are installed; CI installs them.

## Archive Index

Closed milestone history lives in docs/progress/archive/. Keep this root PROGRESS.md focused on hot state: current milestone, active/pending/blocked work, immediate next steps, and recent handoff entries.

| Milestone | File | Status | Notes |
|---|---|---|---|
| Initial peer-handoff setup | docs/progress/archive/initial-peer-handoff.md | not-started | Create this archive only after the first milestone closes. |

---

## Handoff Log  _(append only, newest on top)_

### 2026-07-05 `[codex]@macmini-yiqi` — sync, restamp, docs hygiene
- Preserved pre-sync stamp edits in stash `codex-pre-sync-stamp`, fetched origin, and created branch `codex/sync-restamp-hygiene` from `origin/main`.
- Re-ran `agent-team stamp-handoff /Users/zhabs/Tools/twincut` on the Stage 11 baseline.
- Updated docs to remove stale "tiny/bash-only/no tests" guidance and describe the current Bash CLI + Go UI + Makefile/test-suite shape.
- Removed tracked generated quarantine CSV headers under `installers/_QUARANTINE/`.
- Verification: `make test` passed; `tests/events_contract.sh` PASS=18; `tests/p1_stage11_smoke.sh` PASS=6; `tests/p0_smoke.sh` 25 passed; `tests/p1_thumb_smoke.sh` 28 passed; `tests/p1_thumb_phash_smoke.sh` PASS=17 with pHash-dependent sections skipped because Pillow/imagehash are not installed; `tests/p1_stage9_smoke.sh` skipped because Pillow is not installed; `make build` built `bin/twincut-ui` 8.2M; `git diff --check` passed.
- Sync state: pushed branch `codex/sync-restamp-hygiene`; draft PR #15 opened.

### 2026-07-05 `[codex]@macmini-yiqi` — stamp handoff and assess baseline
- Earlier pre-sync assessment found local `main@d5c5e60` behind `origin/main@1443310` by 1. Local `make test` failed in the old JSON-events restore contract; a temp snapshot of `origin/main@1443310` passed `make test`, `tests/events_contract.sh`, `tests/p1_stage11_smoke.sh`, `tests/p0_smoke.sh`, `tests/p1_thumb_smoke.sh`, and `make build`. `tests/p1_stage9_smoke.sh` and pHash-dependent sections skipped locally without Pillow/imagehash.
