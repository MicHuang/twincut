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
| R-W1 | Remediation Wave 1: matching-engine correctness (vid_eq rewrite + read-crash class) | claude@mac-joyce | done | PR #16 merged (`9cf6928`, 2026-07-09). All DoD met: both new smokes + `make test` green; Tier-1 gemini OK on main diff + grok-4.5 OK on incremental fix (gemini wrapper was down mid-review — model-ID rot, fixed in agent-team PR #47; user authorized grok substitution). The final-review bad-video-fallback race was FIXED pre-merge (not deferred). |
| R-W2 | Remediation Wave 2: Go UI security/robustness (origin guard, panic, apply validation) | claude@mac-joyce | done | PR #17 merged (`a780610`, 2026-07-10). originGuard mux-wide + wiring pin; apply preview validation; nil-deref → 404; \r-aware stderr drain; dead code out. Tier-1 gemini OK. | Plan Tasks 3-4. DoD: `cd ui && go test ./...` green incl. new origin/history/apply tests, Tier-1 review pass. Independent of W1. |
| R-W3 | Remediation Wave 3: CI drift + bash hygiene + shellcheck gate | claude@mac-joyce | in-review | PR #18 open, CI all-green (go/shell/shellcheck/macOS-thumb). Wiring run_tests.py into CI surfaced 2 pre-existing GNU/BSD bugs (hash backslash-escape, find-order keep tie-break) — fixed `01b7c9a`; grok Medium (backup-path same tie-break) fixed `f5c9115`. All DoD met. **Awaiting user merge.** Plan Tasks 5-7 in order. DoD: CI runs run_tests.py + all smokes; `shellcheck --severity=warning` clean; Tier-1 review pass. Note for Task 6: Wave-1 final review left two candidates to fold in — vid_eq strict re-verify is redundant (fast/full same checks ⇒ 2 wasted ffprobe calls/pair) and `--size-pct` as last CLI arg trips `set -u`. |
| T1 | Sync to Stage 11 baseline | codex@macmini-yiqi | done | Preserved earlier stamp in stash `codex-pre-sync-stamp`, fetched origin, and created branch `codex/sync-restamp-hygiene` from `origin/main`. |
| T2 | Re-apply agent-team stamp | codex@macmini-yiqi | done | Re-ran `agent-team stamp-handoff /Users/zhabs/Tools/twincut` on Stage 11; generated `PROGRESS.md`, `AGENTS.md`, and updated `CLAUDE.md`. |
| T3 | Refresh stale project docs | codex@macmini-yiqi | done | Updated `CLAUDE.md`, `README.md`, and `AGENTS.md` so they reflect the Go UI, Makefile/test suite, and Stage 11 typed event contract. |
| T4 | Hygiene cleanup | codex@macmini-yiqi | done | Removed tracked generated quarantine CSV headers under `installers/_QUARANTINE/`. |

### Task DoD convention
- Each pending/in-progress task must name verification commands, manual checks, service/credential dependencies, and what counts as blocked.
- Blocked entries must include the blocking reason, verified facts, next suggested command, and whether user input is required.

### Next up
- **Remediation Wave 3 (final wave)** per `docs/superpowers/plans/2026-07-05-twincut-remediation.md`: Tasks 5-7 **in order** (CI runs full suite + make test-smoke → bash hygiene → shellcheck gate). Waves 1-2 merged (PRs #16/#17). Task 6 also folds in the Wave-1 leftovers noted in the R-W3 row; a future hygiene candidate beyond the plan: `gofmt -l` flags 8 pre-existing files in ui/server (observed during W2, not in plan scope).
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

### 2026-07-10 `[claude]@mac-joyce` — R-W3 executed, PR #18 open (in-review, awaiting user merge)
- Wave 3 (plan Tasks 5-7, sequential) subagent-driven on `claude/remediation-wave3`. Commits: `b70b38c` CI full suite + `make test-smoke` + seam-test adoption; `39e5362` hygiene (dead code, TMP_CACHE trap, vmeta helper dedup, thumb.sh shared stat helpers, EXTS↔VIDEO_EXTS alignment, TSV-path guard at qmove/qdelete, vid_eq missing-arg guard, seam echo reword); `fce57f1` shellcheck 17→0 + CI gate (nested THUMB case for scoped disable); `87e8b5a` final-review fix (TSV-guard skip no longer aborts run via sidecar dispatchers; p0 25→28); `015c8fd` Tier-1 fix (nested case fails closed on future drift).
- Reviews: per-task Approved ×3; final whole-branch (fable) "with fixes" → fixed + re-review merge-ready; Tier-1 (user requested gemini+grok parallel): gemini BLOCKED network (no substitution), grok-4.5 approve-with-fixes — full triage in PR #18 body (2 MAJORs accepted-as-designed with rationale, 1 minor fixed, rest deferred).
- **CI-surfaced portability fixes (in this PR):** wiring `tests/json_events/run_tests.py` into CI ran it on ubuntu for the first time and exposed 2 pre-existing GNU/BSD bugs the macOS-only local runs never hit — (1) GNU `md5sum`/`sha1sum`/`shasum` prefix output with `\` when the filename contains a backslash, corrupting `awk '{print $1}'` hash extraction (fixed: strip leading `\` in `hash_file`, `01b7c9a`); (2) equal-mtime keep tie-break used find(1) traversal order, filesystem-dependent → nondeterministic keep across ext4/APFS (fixed: `(mtime, path)` `LC_ALL=C` sort on source `01b7c9a` and backup `f5c9115` self-check paths). Tier-1 grok verified both.
- **Follow-ups recorded (not this PR):** similar-video equal-mtime keep still scan-order (grok Low, twincut.sh ~1322); source-self/backup sort `-k2,2` truncates a path at its first embedded tab (grok Low); TSV guard extension (manifest `matched` col, hash-index writes, `\r`); newline-in-path unsafe end-to-end in line-oriented SMAP/sort (pre-existing, acceptable residual); vid_eq strict re-verify redundancy (needs design decision); vid_eq usage-string 3× dup; stage9 D1/D1b run_end assertion gap (pre-existing); `gofmt -w` 8 ui/server files (next Go PR); plan's own §deferred list unchanged.
- Note for merge: EXTS widening ⇒ existing hash caches report `# meta:` drift once → auto-rebuild/`--assume-yes` or prompt (by design, in PR body).
- After merge this closes the entire 2026-07-05 remediation plan (Waves 1/2/3 = PRs #16/#17/#18).

### 2026-07-10 `[claude]@mac-joyce` — R-W2 closed: PR #17 merged (`a780610`)
- User merged; board updated (R-W2 → done), local branch pruned, synced to mac-yiqi via git-sync. Remaining: R-W3 only (Tasks 5-7 sequential; leftovers listed in its row). Details of the wave in the previous entry.

### 2026-07-09 `[claude]@mac-joyce` — R-W2 executed, PR #17 open (in-review, awaiting user merge)
- Executed Wave 2 (plan Tasks 3-4) subagent-driven on `claude/remediation-wave2`. Commits: `f69fd0b`+`9e79da2` originGuard middleware (mux-wide Host/Origin guard, IPv6 bracket fix found by task review); `6616b34`+`f790774` robustness bundle (history-preview nil-deref → 404, apply preview validation 422/409/422 on self+cross-check, healthz writeJSON, `\r`-aware stderr drain, dead code, duplicated `--json-events` dropped in BOTH thumbnail handlers — apply-path instance was found beyond the brief and sanctioned in-loop); `7d8098d` final-review gate (Handler()-wiring pin test, mutation-verified + drainStderr scanner.Err logging).
- Reviews: per-task Approved (Task 3 after one fix loop); final whole-branch (fable) "with fixes" → fixed; Tier-1 `reviewer-gemini` OK — no BLOCKER/MAJOR, accepted-as-is: trailing-dot Host 403 (fails closed), CRLF empty-token (filtered). Note `/healthz` now 403s for non-loopback Host — intentional, documented in PR body.
- Verification: TDD red→green per behavior fix; `cd ui && go test ./... -count=1` + `go vet` green; `make test` green on branch; CI green on `7d8098d`.
- Merge blocked for agent (auto-mode classifier requires human merge, same as PR #16) — user to merge, then: PROGRESS → done, git-sync, R-W3 remains (Tasks 5-7, sequential, folds in Wave-1 leftovers noted in its row).
- Pre-existing observation for a future hygiene pass (NOT this wave): `gofmt -l` flags 8 unformatted files in ui/server.

### 2026-07-09 `[claude]@mac-joyce` — R-W1 closed: PR #16 merged (`9cf6928`)
- Supersedes the open decision in the previous entry: user chose **fix-before-merge**, so the bad-video-fallback race was fixed on-branch (`d0e0a4b`: load/append meta row before the bad-video verdict, fallback uses loaded fields, mirroring the source path) + smoke comment de-overstated per review (`3b2b9d6`). Re-review (sonnet) Approved; incremental Tier-1 via **grok-4.5** `TEAM_RESULT=OK` ("ship the engine change").
- Mid-review infra detour: both Tier-1 wrappers hit upstream model-ID rot (`gemini-2.5-flash` 404, `grok-build` removed). Fixed in agent-team PR #47 (gemini default → `gemini-flash-latest`, grok reviewer → `grok-4.5`, executor stays `grok-composer-2.5-fast`); user authorized the grok substitution for the pending review per TEAM.md fail-closed policy.
- CI green on merge head (go / shell / macOS thumbnail). Board: R-W1 → done; R-W3 unblocked (plus two Wave-1 review leftovers noted in its row for Task 6); R-W2 unclaimed and independent.
- Wave-1 review-finding triage records live in `.superpowers/sdd/progress.md` (gitignored scratch, this machine only).

### 2026-07-09 `[claude]@mac-joyce` — R-W1 executed, PR #16 open (in-review)
- Executed remediation Wave 1 (plan Tasks 1-2) via superpowers:subagent-driven-development on branch `claude/remediation-wave1`: fresh implementer subagent per task, per-task spec+quality review, final whole-branch review, then Tier-1 `reviewer-gemini` on the PR diff.
- Commits: `21bbf57` vid_eq.sh rewrite (ffprobe field swap, WxH compare, EQUAL mode, export SIZE_PCT/DUR_SEC) + `tests/vid_eq_smoke.sh`; `50e8373` crash-class fix (12× `read < <()` `|| true`, is_video_ext guard reorder, seen-pair break→continue) + `tests/backup_selfcheck_smoke.sh`; `f5047d6` Tier-1 findings (probe helpers now emit `:no` instead of aborting when ffprobe fails on an existing file; header comment de-confused). Both smokes wired into CI.
- Verification: TDD red→green per task; `make test` bash 12/12 + Go ok; p0 25/25; stage9 full-run (Pillow present); stage11 6/6; both new smokes green; vid_eq verified under real bash 3.2.57.
- Tier-1 `reviewer-gemini`: TEAM_RESULT=OK, no BLOCKER/MAJOR; its MINOR+NIT fixed in `f5047d6`.
- **Open decision for user before merge:** final whole-branch review flagged one Important plan-level finding, deferred (reviewer judged merge-safe, strictly better than the crash it replaces): `bin/twincut.sh` backup loop's newly-reachable bad-video fallback can move a good video to `_bad_video` when its meta row is missing (mid-run file-add race); self-heal append runs after the check. Fix idea: self-heal before condemning. Options: fix on this branch pre-merge, or merge and fold into Wave 3. Minor Wave-3 candidates also recorded in `.superpowers/sdd/progress.md` (redundant strict re-verify = 2 extra ffprobe calls/pair; `--size-pct` as last arg trips `set -u`).
- R-W2 remains independent/unclaimed; R-W3 waits for this merge.

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
