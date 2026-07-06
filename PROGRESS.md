# PROGRESS - twincut

<!-- agent-team:progress-schema=2026-06-28-u1 -->

> Peer-handoff file. Start by reading Status Board; finish by updating Status Board and appending Handoff Log.
> Protocol: agent-team/docs/peer-handoff.md. Owner handles: claude / codex.
> Leader instance = `<handle>@<machine>` (which tool on which machine); use it in Handoff Log titles.

---

## Status Board  _(overwrite this section to reflect current reality)_

**Current milestone:** Remediation of 2026-07-05 full-repo assessment findings (plan ready, execution NOT started).

### Task table

| # | 任务 | owner | status | 备注 |
|---|------|-------|--------|------|
| R-W1 | Remediation Wave 1: matching-engine correctness (vid_eq rewrite + read-crash class) | — | pending | Plan: `docs/superpowers/plans/2026-07-05-twincut-remediation.md` Tasks 1-2. DoD: `bash tests/vid_eq_smoke.sh` + `bash tests/backup_selfcheck_smoke.sh` green, `make test` green, Tier-1 reviewer-gemini pass on PR. Execute via superpowers:subagent-driven-development. |
| R-W2 | Remediation Wave 2: Go UI security/robustness (origin guard, panic, apply validation) | — | pending | Plan Tasks 3-4. DoD: `cd ui && go test ./...` green incl. new origin/history/apply tests, Tier-1 review pass. Independent of W1. |
| R-W3 | Remediation Wave 3: CI drift + bash hygiene + shellcheck gate | — | pending | Plan Tasks 5-7, **in order, after W1 merges** (CI list references W1 smokes). DoD: CI runs run_tests.py + all smokes; `shellcheck --severity=warning` clean; Tier-1 review pass. |
| T1 | Sync to Stage 11 baseline | codex@macmini-yiqi | done | Preserved earlier stamp in stash `codex-pre-sync-stamp`, fetched origin, and created branch `codex/sync-restamp-hygiene` from `origin/main`. |
| T2 | Re-apply agent-team stamp | codex@macmini-yiqi | done | Re-ran `agent-team stamp-handoff /Users/zhabs/Tools/twincut` on Stage 11; generated `PROGRESS.md`, `AGENTS.md`, and updated `CLAUDE.md`. |
| T3 | Refresh stale project docs | codex@macmini-yiqi | done | Updated `CLAUDE.md`, `README.md`, and `AGENTS.md` so they reflect the Go UI, Makefile/test suite, and Stage 11 typed event contract. |
| T4 | Hygiene cleanup | codex@macmini-yiqi | done | Removed tracked generated quarantine CSV headers under `installers/_QUARANTINE/`. |

### Task DoD convention
- Each pending/in-progress task must name verification commands, manual checks, service/credential dependencies, and what counts as blocked.
- Blocked entries must include the blocking reason, verified facts, next suggested command, and whether user input is required.

### Next up
- **Execute remediation plan** `docs/superpowers/plans/2026-07-05-twincut-remediation.md` (Wave 1 first; user decision 2026-07-05: subagent-driven execution, do NOT start until user green-lights). Read the plan's Global Constraints before touching anything — bash 3.2 compat, Go-owned event contract, no new features.
- Agent-team follow-up: improve `reviewer-claude` dispatch diagnostics for hook/missing-agent failures observed while reviewing PR #15.
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

### 2026-07-05 `[claude]@mac-joyce` — full-repo assessment + remediation plan (execution deferred)
- Ran a full no-new-features assessment of `main@5917d06`. Highest-severity verified findings: (1) `bin/vid_eq.sh` reads ffprobe duration/size into swapped vars → similar-video only ever matches byte-identical sizes, and w/h are never compared (single `read` on 3-line output); (2) `EQUAL:yes` is never emitted → `--video-fast-strict` can confirm zero pairs; (3) `--report/--fix-backup-dupes` crashes (exit 1, no `run_end`) on any non-video file — `read < <(empty awk lookup)` under `set -e`, lookup precedes the `is_video_ext` guard; (4) Go UI has no Origin/Host guard on state-changing endpoints incl. `POST /api/runs` (arbitrary bash argv); (5) CI never runs `tests/json_events/run_tests.py`; (6) assorted dead code / O(N²) loops / doc drift.
- Wrote the full TDD remediation plan: `docs/superpowers/plans/2026-07-05-twincut-remediation.md` — 3 PR waves, 7 tasks, every fix lands with a red-first regression test; verified-fact appendix included (fixture sizes, ffprobe field order, shellcheck baseline). Deferred items are listed explicitly in the plan to prevent scope creep.
- User decision: execute via superpowers:subagent-driven-development, **but do not start yet** — UI work is mid-flight; remediation waits for the user's go signal. Wave 1/2 are independent; Wave 3 requires Wave 1 merged.
- Also this session: added twincut to bidirectional git-sync (mac-joyce `~/Playground/twincut` ↔ mac-yiqi `~/Tools/twincut`; note the non-default path on mac-yiqi).
- Verification: `make test` green at assessment start (bash 12/12 + Go ok); no product code touched — this handoff adds only the plan + this PROGRESS update.

### 2026-07-05 `[codex]@macmini-yiqi` — reviewer-claude dispatch blocker for PR #15
- Context: attempted to send draft PR #15 to `reviewer-claude` through the required `agent-dispatch reviewer-claude < prompt` wrapper.
- Observed failures: the normal wrapper path ended with `TEAM_RESULT=ERROR unknown`; one path surfaced a Honcho SessionEnd hook permission error opening `~/.honcho/claude-context.md`; escalated retry hit `Hook cancelled`; `HONCHO_ENABLED=false` still returned an empty `TEAM_RESULT=ERROR unknown`; `CLAUDE_CODE_SAFE_MODE=1` and `CLAUDE_CODE_SIMPLE=1` avoided hooks but made `--agent reviewer-claude` unavailable.
- Policy note: raw `claude -p --agent reviewer-claude` diagnostics were not used because they would bypass the agent-team wrapper envelope.
- Agent-team improvement signal: classify Claude wrapper failures as hook failure, missing agent, or unknown with captured stderr/stdout summary; provide a hook-safe Claude mode that still loads user agents; make `agent-team collect <repo>` able to preserve project-local dispatch blocker notes beyond counting Handoff Log entries.

### 2026-07-05 `[codex]@macmini-yiqi` — sync, restamp, docs hygiene
- Preserved pre-sync stamp edits in stash `codex-pre-sync-stamp`, fetched origin, and created branch `codex/sync-restamp-hygiene` from `origin/main`.
- Re-ran `agent-team stamp-handoff /Users/zhabs/Tools/twincut` on the Stage 11 baseline.
- Updated docs to remove stale "tiny/bash-only/no tests" guidance and describe the current Bash CLI + Go UI + Makefile/test-suite shape.
- Removed tracked generated quarantine CSV headers under `installers/_QUARANTINE/`.
- Verification: `make test` passed; `tests/events_contract.sh` PASS=18; `tests/p1_stage11_smoke.sh` PASS=6; `tests/p0_smoke.sh` 25 passed; `tests/p1_thumb_smoke.sh` 28 passed; `tests/p1_thumb_phash_smoke.sh` PASS=17 with pHash-dependent sections skipped because Pillow/imagehash are not installed; `tests/p1_stage9_smoke.sh` skipped because Pillow is not installed; `make build` built `bin/twincut-ui` 8.2M; `git diff --check` passed.
- Sync state: pushed branch `codex/sync-restamp-hygiene`; draft PR #15 opened.

### 2026-07-05 `[codex]@macmini-yiqi` — stamp handoff and assess baseline
- Earlier pre-sync assessment found local `main@d5c5e60` behind `origin/main@1443310` by 1. Local `make test` failed in the old JSON-events restore contract; a temp snapshot of `origin/main@1443310` passed `make test`, `tests/events_contract.sh`, `tests/p1_stage11_smoke.sh`, `tests/p0_smoke.sh`, `tests/p1_thumb_smoke.sh`, and `make build`. `tests/p1_stage9_smoke.sh` and pHash-dependent sections skipped locally without Pillow/imagehash.
