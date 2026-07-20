# PROGRESS - twincut

<!-- agent-team:progress-schema=2026-06-28-u1 -->

> Peer-handoff file. Start by reading Status Board; finish by updating Status Board and appending Handoff Log.
> Protocol: agent-team/docs/peer-handoff.md. Owner handles: claude / codex.
> Leader instance = `<handle>@<machine>` (which tool on which machine); use it in Handoff Log titles.

---

## Status Board  _(overwrite this section to reflect current reality)_

**Current milestone:** 🚧 IN PROGRESS — F-H7 pre-use apply-path contract cleanup. Make `--json-in` exit propagation explicit and prevent skipped apply moves from creating empty destination directories before formal daily use.

### Task table

| # | 任务 | owner | status | 备注 |
|---|------|-------|--------|------|
| F-H7 | pre-use apply-path contract cleanup | codex@macmini-yiqi | in-progress | Claim branch `codex/f-h7-preuse-apply-cleanup`. Scope: explicit `process_apply_list_jsonin` status propagation at the top-level short-circuit; remove the redundant pre-`qmove` `mkdir -p` so excluded/hardlink-skipped records do not leave empty destination directories. DoD: red-first or mutation-backed contract tests, focused Stage 9 + full suite, syntax/diff checks, Tier-1 Grok 4.5, CI green, user-approved merge. |
| F-H6 | backup similar-video pair deduplication | codex@macmini-yiqi | done | PR #24 squash-merged as `74aef6f` (2026-07-19, user-approved). Exact Bash 3.2 indexed-array pair keys suppress reverse traversal without path-pattern collisions; innermost `continue 1` preserves later candidates. K3 pins exactly one event/report for a pair; K3b mutation-check pins all three unique pairs. Full local checks and all four GitHub CI jobs green. Tier-1 Grok 4.5 initial Ship with findings; all addressed; incremental re-review Ship with no required residuals. |
| F-H5 | stage9 apply exit-code assertions + keep-policy test polish | claude@mac-joyce | done | PR #23 squash-merged as `df13634` (2026-07-12, user approval); local/remote claim branches pruned. Test-only: all 14 `|| true` masks in `tests/p1_stage9_smoke.sh` → captured-rc assertions (contract characterized first: per-record failures exit 0 via event channel; only apply-flow malformed-JSON pre-flight exits 1), header documents it, per-record-failure sections also assert `run_end succeeded` (48→66 asserts); K3 gains `dup_group.keep_path` JSON assert; keep_policy header notes ext4-vs-APFS discriminating power. Local: both smokes + `make test` + exact shellcheck CI gate green. Tier-1 grok-4.5 `TEAM_RESULT=OK` / Ship; nits taken in-branch (`d2919ac`). |
| F-H4 | Go apply-endpoint 422 mode-echo redaction | codex@macmini-yiqi | done | PR #22 squash-merged as `cf7adbe` (2026-07-12 user approval); local/remote claim branches pruned. Self-check, cross-check, and thumbnail apply handlers return stable wrong-mode 422 text without raw `prevSnap.Mode`; red-first sentinel tests cover all three. Local/CI checks green; Tier-1 grok-4.5 `TEAM_RESULT=OK ok` / Ship. |
| F-H3 | vid_eq strict-mode single-pass validation + usage dedup | codex@macmini-yiqi | done | PR #21 squash-merged as `981d513` (2026-07-11 user approval); local/remote claim branches pruned. Removed redundant strict metadata/ffprobe re-checks at both production call sites, preserved strict semantics, and centralized usage output. Local/CI checks green; Tier-1 grok-4.5 Ship with nits, Important follow-ups fixed before merge. |
| F-H2 | vmeta-index path guard + refresh crash fix | claude@mac-joyce | done | PR #20 merged (`e3d6098`, 2026-07-11, user-approved squash). Scope grew beyond the recorded note — actual consequence was a full run crash, not \"bounded\": (1) `tsv_path_safe` guard at the `append_video_meta` choke point (covers all 3 writers, more complete than the walk-only shape suggested by F-H1 review); (2) retention loops if-form so a dead last row doesn't leak exit 1 → `set -e` killed the run at end-of-run refresh, reproducible with NO evil filename (fix-mode quarantining an indexed video sufficed; line-1258 call only survived because command substitution drops errexit); (3) Tier-1 Important fixed: TSV-unsafe path no longer falls through empty-meta fallback to a false `bad_video` label. All 3 red-first in `tests/backup_selfcheck_smoke.sh`. `make test` + all smokes + shellcheck CI gate green. Tier-1 grok-4.5 Approve-with-nits; Important fixed in-branch (`f257ea0`). |
| F-H1 | Follow-up hygiene wave: KEEP determinism remainder + TSV-guard extension + stage9 run_end assertions + gofmt ui/server | claude@mac-joyce | done | PR #19 merged (`98c30aa`, 2026-07-11). All DoD met: `make test` + all smokes green; TDD red-first where reachable (K2/K3 fs-order caveat recorded); `gofmt -l` empty; shellcheck clean; CI green both platforms. Per-task reviews Approved ×4; final whole-branch (fable) "Yes"; Tier-1 gemini OK (pick_keep comment MINOR fixed in-branch; rest recorded in "Next up"). |
| R-W1 | Remediation Wave 1: matching-engine correctness (vid_eq rewrite + read-crash class) | claude@mac-joyce | done | PR #16 merged (`9cf6928`, 2026-07-09). All DoD met: both new smokes + `make test` green; Tier-1 gemini OK on main diff + grok-4.5 OK on incremental fix (gemini wrapper was down mid-review — model-ID rot, fixed in agent-team PR #47; user authorized grok substitution). The final-review bad-video-fallback race was FIXED pre-merge (not deferred). |
| R-W2 | Remediation Wave 2: Go UI security/robustness (origin guard, panic, apply validation) | claude@mac-joyce | done | PR #17 merged (`a780610`, 2026-07-10). originGuard mux-wide + wiring pin; apply preview validation; nil-deref → 404; \r-aware stderr drain; dead code out. Tier-1 gemini OK. | Plan Tasks 3-4. DoD: `cd ui && go test ./...` green incl. new origin/history/apply tests, Tier-1 review pass. Independent of W1. |
| R-W3 | Remediation Wave 3: CI drift + bash hygiene + shellcheck gate | claude@mac-joyce | done | PR #18 merged (`aa010ff`, 2026-07-10). CI now runs run_tests.py + all smokes + shellcheck job; `make test-smoke`; bash hygiene + TSV guard; shellcheck warning-clean. Wiring run_tests.py into CI surfaced & fixed 2 pre-existing GNU/BSD bugs (hash backslash-escape, find-order keep tie-break, both source+backup paths); Wave-1 vid_eq arg-guard leftover folded in. Tier-1 grok-4.5 (gemini BLOCKED network, not substituted). |
| T1 | Sync to Stage 11 baseline | codex@macmini-yiqi | done | Preserved earlier stamp in stash `codex-pre-sync-stamp`, fetched origin, and created branch `codex/sync-restamp-hygiene` from `origin/main`. |
| T2 | Re-apply agent-team stamp | codex@macmini-yiqi | done | Re-ran `agent-team stamp-handoff /Users/zhabs/Tools/twincut` on Stage 11; generated `PROGRESS.md`, `AGENTS.md`, and updated `CLAUDE.md`. |
| T3 | Refresh stale project docs | codex@macmini-yiqi | done | Updated `CLAUDE.md`, `README.md`, and `AGENTS.md` so they reflect the Go UI, Makefile/test suite, and Stage 11 typed event contract. |
| T4 | Hygiene cleanup | codex@macmini-yiqi | done | Removed tracked generated quarantine CSV headers under `installers/_QUARANTINE/`. |

### Task DoD convention
- Each pending/in-progress task must name verification commands, manual checks, service/credential dependencies, and what counts as blocked.
- Blocked entries must include the blocking reason, verified facts, next suggested command, and whether user input is required.

### Next up
- **F-H1 (in-review) cleared the first four former follow-ups** (KEEP determinism remainder, TSV-guard extension incl. `\r`, stage9 D1/D1b `run_end` asserts, gofmt ui). Remaining recorded follow-ups, none blocking:
  - ~~vmeta-index path guard~~ → claimed as F-H2 (in-progress, see task table).
  - ~~**vid_eq** strict re-verify + usage dedup~~ → F-H3 done via PR #21.
  - ~~**Go** apply-endpoint 422 mode echo~~ → F-H4 done via PR #22.
  - **Optional test/comment polish**: ~~K3 `keep_path` JSON assert; keep_policy ext4 note; stage9 `|| true` exit-code masks~~ → F-H5 done. ~~Apply-loop `mkdir -p` runs before qmove's guard~~ → claimed as F-H7.
  - **New optional product follow-ups from F-H5 Tier-1 (grok-4.5, non-blocking)**: ~~make the `--json-in` short-circuit's exit propagation explicit instead of relying on `set -e`~~ → claimed as F-H7. ~~Backup similar-video duplicate `dup_group` emission~~ → F-H6 fixed and merged via PR #24.
  - **Accepted residuals**: newline-in-path remains unsafe end-to-end in the line-oriented SMAP/sort; `emit_warn --path` blames the action target even when matched/dir is the offending field (convention).
  - The remediation plan's own §"Explicitly deferred" list (O(N²) membership loops, double dir walk, thumb memoization, cache pruning, Go run eviction, etc.) — unchanged, trigger-gated.
- Agent-team follow-up: improve `reviewer-claude` dispatch diagnostics for hook/missing-agent failures observed while reviewing PR #15; wrappers could map upstream "model no longer available" 404s to `BLOCKED not-configured` (seen when gemini/grok model IDs rotted mid-session, fixed in agent-team PR #47).
- Optional follow-up: install Pillow/imagehash locally if full pHash smoke coverage is needed outside CI (CI installs them).

### How to claim
- Run `agent-team handoff-check <task-slug>` first (sync state + existing-claim check), then branch `<handle>/<task>` off synced main, set owner + in-progress, commit `chore(progress): claim <task>`, and push the branch before working. See agent-team/docs/peer-handoff.md §3.

### Blocked / waiting on
- None currently.
- Local pHash-dependent smoke sections may skip unless Pillow/imagehash are installed; CI installs them.

## Archive Index

Closed milestone history lives in docs/progress/archive/. Keep this root PROGRESS.md focused on hot state: current milestone, active/pending/blocked work, immediate next steps, and recent handoff entries.

| Milestone | File | Status | Notes |
|---|---|---|---|
| Initial peer-handoff setup | docs/progress/archive/initial-peer-handoff.md | not-started | Create this archive only after the first milestone closes. |

---

## Handoff Log  _(append only, newest on top)_

### 2026-07-19 `[codex]@macmini-yiqi` — F-H7 claimed: pre-use apply cleanup
- User requested both remaining apply-path product follow-ups before formal use: explicit `--json-in` exit propagation and elimination of empty destination directories on guarded/skipped `qmove` records.
- Online claim preflight found no existing `f-h7-preuse-apply-cleanup` branch. Work is isolated on `codex/f-h7-preuse-apply-cleanup`; next is contract-first tests, minimal implementation, full verification, Tier-1, and draft PR.

### 2026-07-19 `[codex]@macmini-yiqi` — F-H6 closed: PR #24 merged (`74aef6f`)
- User approved; marked draft PR #24 ready and squash-merged it. GitHub CI was green before merge: go-tests, shell-tests (including K3/K3b with CI dependencies), shellcheck, and thumbnail-tests-macos.
- Fast-forwarded local `main` to the merge commit. F-H6 is complete; remaining optional work stays in "Next up" (`--json-in` explicit exit propagation, apply-loop cosmetic directory creation, accepted residuals, and trigger-gated deferrals).

### 2026-07-19 `[codex]@macmini-yiqi` — F-H6 draft PR #24 open
- Rechecked the account concern before publishing: local Git author remains `Yiqi Huang <yitsi.huang@gmail.com>`, while `gh api user`, SSH authentication, and the only account in `hosts.yml` all resolve to GitHub login `MicHuang`; the earlier token-invalid result no longer reproduces.
- Opened draft PR #24 (`codex/f-h6-backup-dup-group-dedup` → `main`) through the GitHub connector with the root cause, behavior impact, red/mutation evidence, full verification matrix, Tier-1 results, and local Pillow/shellcheck caveats. Next: wait for CI, then user decides ready/merge.

### 2026-07-19 `[codex]@macmini-yiqi` — F-H6 Tier-1 Ship; draft PR blocked on `gh` re-auth
- User explicitly approved sending the minimal private diff to Grok 4.5 after the tenant disclosure warning. Initial Tier-1 returned Ship and requested multi-candidate coverage plus exact path-key handling; `cea32d1` addresses both with a Bash 3.2 indexed array, explicit inner-loop `continue 1`, post-dedup `pick_keep`, and K3b's three-pair graph.
- Mutation evidence: temporarily changing `continue 1` to `break` made K3b fail with `expected all three unique pairs, got 2`; restored code passed. Full `make test` plus focused Bash 3.2/smoke/syntax/diff checks are green. Incremental Grok re-review returned `TEAM_RESULT=OK ok` / Ship with no required residuals.
- Branch `codex/f-h6-backup-dup-group-dedup` is pushed through `cea32d1`. Draft PR was not created because the required `github:yeet` preflight found `gh auth status` invalid for active account `MicHuang`; next command is `gh auth refresh -h github.com`, then resume draft PR creation. No auth workaround or connector bypass attempted.

### 2026-07-19 `[codex]@macmini-yiqi` — F-H6 implemented; Tier-1 blocked on explicit external-diff approval
- Reproduced backup similar-video pair duplication: report-only and fix+dry-run emitted the same pair twice, while real fix emitted once only because the first qmove removed the reverse-pass input. Source-self already had canonical seen-pair suppression; cross mode is not symmetric across one directory.
- TDD on `tests/keep_policy_smoke.sh` K3: new exactly-one event assertion failed first with `got 2`; `6cca107` mirrors source-self's canonical pair key and uses `continue` on seen pairs so other candidates remain reachable. K3 now also pins one `BACKUP-SIMILAR` line.
- Verification green: keep-policy, backup-self, vid_eq, events contract 18/18, Stage 11 6/6, P0 28/28, full `make test`, `bash -n`, and `git diff --check`. Stage 9 skipped because Pillow is absent; shellcheck is not installed locally and remains a CI gate.
- Required Tier-1 Grok 4.5 did not execute: tenant policy rejected sending the minimal 13-line private-repo product/test diff to the external reviewer despite agent-team standing approval. No workaround or reviewer substitution attempted. Next: obtain explicit informed user approval, rerun `grok-review`, address findings, then publish the draft PR.

### 2026-07-12 `[claude]@mac-joyce` — F-H5 closed: PR #23 merged (`df13634`)
- User approved; marked draft ready, squash-merged, fast-forwarded local `main`, pruned local/remote claim branches, synced peers.
- CI green pre-merge on both workflow runs (go-tests, shell-tests, shellcheck, thumbnail-tests-macos). Implementation details in the previous entry.
- Remaining follow-ups live in "Next up" (`--json-in` `exit $?` clarity, duplicate `dup_group` emission observation, apply-loop `mkdir -p` cosmetic, accepted residuals, trigger-gated deferrals). No active twincut work remains.

### 2026-07-12 `[claude]@mac-joyce` — F-H5 executed, draft PR #23 open (in-review, awaiting user merge)
- Test-only hygiene wave on `claude/stage9-apply-exitcode-polish`, bundling three "Next up" polish items. Commits: `71d7033` core change (14 `|| true` → captured-rc asserts in stage9 + header contract doc; K3 `dup_group.keep_path` JSON assert; keep_policy ext4/APFS note); `d2919ac` Tier-1 nits (run_end-succeeded asserts on bad-decision/traversal/D5/D2, header scoping).
- Method: characterized exit codes empirically BEFORE pinning (scratchpad probe replacing `|| true` with rc echo). Result: all invocations exit 0 except D3 malformed-JSON (exit 1) — matches `process_apply_list_jsonin` by design (per-record failures → event channel + skipped + `run_end succeeded`; only pre-flight returns 1). rc assert conditions are double-quoted so the value expands at call time (informative FAIL output; avoids SC2034 under assert()'s eval).
- Verification: stage9 66/66, keep_policy all ok, `make test` green, exact CI shellcheck warning gate clean, `git diff --check` clean. Tier-1 per §2 (>50 lines; small packet → direct `grok-review` from main thread): grok-4.5 `TEAM_RESULT=OK` / Ship, zero required changes.
- Follow-ups recorded in "Next up" (NOT taken — product code, out of test-only scope): `--json-in` short-circuit `exit 0` → `exit $?` clarity; duplicate `dup_group` emission observation (same pair emitted as group_id 1 AND 2 in one backup similar-video run — pre-existing, seen while probing K3, needs investigation before calling it a bug).
- Next for whoever picks up: user decides merge of draft PR #23; after merge set F-H5 → done, prune claim branch, sync peers.

### 2026-07-12 `[codex]@macmini-yiqi` — F-H4 closed: PR #22 merged (`cf7adbe`)
- User approved wrap-up. Marked draft PR #22 ready, squash-merged it as `cf7adbe`, fast-forwarded local `main`, and deleted local/remote `codex/f-h4-go-422-mode-redaction` branches.
- F-H4 is complete: all three apply handlers redact raw mode from wrong-mode 422 responses; Tier-1 Grok verdict Ship; final GitHub Go, shell, shellcheck, and macOS thumbnail jobs green. No active twincut work remains.

### 2026-07-11 `[codex]@macmini-yiqi` — F-H4 Tier-1 passed; draft PR #22 open
- User explicitly authorized continuing before the reported approval reset; the required `grok-review` then ran successfully and returned `TEAM_RESULT=OK ok` / Ship for the self-check and cross-check redaction in `600fd65`.
- Reviewer found the same raw-mode echo in thumbnail apply. Folded that same-class residual into F-H4 in `e45a73c`: added a sentinel/exact-body red-first regression, removed the raw mode from the 422 response, and reran focused tests, full `go test ./... -count=1`, and repo `make test` green.
- Draft PR #22 opened from `codex/f-h4-go-422-mode-redaction`; next is GitHub CI, then user merge. Accepted reviewer notes left out of scope: server-owned `status=` strings and request `preview_run_id` echo on some 404 paths.

### 2026-07-11 `[codex]@macmini-yiqi` — F-H4 implemented; review/publish gate blocked
- TDD fix in `600fd65`: self-check and cross-check apply wrong-mode 422 responses no longer concatenate raw `prevSnap.Mode`; endpoint-specific stable messages and status 422 remain. New tests set `internal_sensitive_mode`, assert it is absent, and pin the exact public response body. RED first showed both leaks; GREEN after the two-line production fix.
- Verification: focused wrong-mode tests, `go test ./... -count=1`, sandbox-safe repo `make test`, gofmt, and `git diff --check` all passed.
- Required Tier-1 `grok-review` did not execute because the approval layer hit its usage limit and prohibited workaround/substitution. GitHub draft-PR creation was rejected by the same gate. Code and claim commits are pushed on `codex/f-h4-go-422-mode-redaction`; next after the reported reset is reviewer → findings fix/retest → draft PR/CI.

### 2026-07-11 `[codex]@macmini-yiqi` — F-H3 merged/cleaned; F-H4 claimed
- User approved F-H3 merge. Marked draft PR #21 ready, squash-merged it as `981d513`, fast-forwarded local `main`, and deleted local/remote `codex/f-h3-vid-eq-single-pass` branches.
- Claimed F-H4 from fresh `origin/main`: `agent-team handoff-check f-h4-go-422-mode-redaction` passed online with no existing claim, on branch `codex/f-h4-go-422-mode-redaction`.
- Scope: self-check and cross-check apply wrong-mode 422 responses must no longer concatenate raw `prevSnap.Mode`; retain 422 and stable endpoint-specific guidance. Verification plan: sentinel-mode red-first tests on both handlers, focused and full Go tests, repo `make test`, then Tier-1 review.

### 2026-07-11 `[codex]@macmini-yiqi` — F-H3 implemented, draft PR #21 green
- Removed strict mode's redundant bare/full `vid_eq` pass at both backup-self and cross/source-self production call sites. Strict size/duration thresholds, vmeta fps/bitrate filtering, `video_strict` event labels, strict decision labels, and the direct helper full/`EQUAL` CLI contract remain intact. Centralized `vid_eq.sh` usage output and updated `CLAUDE.md` to the single-pass contract.
- TDD evidence: new cross-check invocation-count regression failed first with `strict candidate should call vid_eq once, got 2`, then passed after the production change. Tier-1 grok-4.5 returned `TEAM_RESULT=OK ok` / Ship with nits; fixed both Important follow-ups by adding a backup-self call-site regression and refreshing `CLAUDE.md`.
- Verification: sandbox-safe `make test` green (script 12/12, Go, event contract 18/18, P0 28/28, Stage 11 6/6, vid_eq/backup-selfcheck/keep-policy smokes); `bash -n` and `git diff --check` green. Local shellcheck was unavailable, but draft PR #21's GitHub shellcheck gate passed along with Go, shell, and macOS thumbnail jobs.
- Commits: `06d3291` implementation, `cfefb16` reviewer follow-up. Next: user review/merge of draft PR #21; after merge mark F-H3 done, prune branch, and sync peers.

### 2026-07-11 `[codex]@macmini-yiqi` — F-H3 claimed: vid_eq strict-mode single pass
- Checked the live board plus open GitHub issues/PRs; no active work or collision existed. `agent-team handoff-check f-h3-vid-eq-single-pass` passed online from fresh branch `codex/f-h3-vid-eq-single-pass` at `origin/main`.
- Scope: remove the two redundant strict-mode bare `vid_eq` re-verification calls (fast/full currently execute identical metadata checks under the same exported strict thresholds), keep the existing strict fps/bitrate vmeta filter and `video_strict` event/decision labels, and deduplicate `bin/vid_eq.sh` usage output.
- Verification plan: add a red-first invocation-count regression, run `bash tests/vid_eq_smoke.sh`, `make test`, relevant smokes, the CI shellcheck warning gate, then Tier-1 review because matching behavior is business logic.

### 2026-07-11 `[claude]@mac-joyce` — F-H2 closed: PR #20 merged (`e3d6098`)
- User approved wrap-up; squash-merged, board updated (F-H2 → done, milestone complete), local + remote claim branches pruned, synced to mac-yiqi.
- Implementation details in the previous entry. Remaining follow-ups in "Next up" (vid_eq strict re-verify design decision + usage dedup, Go 422 mode-echo, optional polish, accepted residuals). No active twincut work remains.

### 2026-07-11 `[claude]@mac-joyce` — F-H2 implemented, PR #20 open, awaiting user merge
- vmeta-index guard follow-up turned out to be a crash-class bug, not "bounded" as recorded: a corrupt/dead row in the LAST retained position made the retention pipeline exit 1 and `set -e` killed the run at the end-of-run refresh (line ~1704), after quarantine moves but before `run_end`/SUMMARY. Reproducible with no evil filename — fix-mode quarantining an indexed video sufficed. The startup call site (line ~1258) only survived because `$( )` command substitution drops errexit by default.
- Three fixes, all red-first in `tests/backup_selfcheck_smoke.sh` (scenarios: tab-named video, deleted-video pruning, fix-mode quarantine+refresh; keeper-is-jpg trick makes the dead-row layout find-order-independent): (1) `tsv_path_safe` guard at `append_video_meta` choke point — deviates deliberately from the review note's walk-only suggestion because the two similar-video self-heals are also writers; (2) retention loops `&&`→`if` so no status leak; (3) `tsv_path_safe` early-out at both similar-video loop entries so unsafe paths don't get falsely labeled `bad_video` (grok Tier-1 Important, fixed in-branch `f257ea0`).
- Verification: `make test`, p0 (28), stage9 (48), stage11 (6), events contract, backup_selfcheck/vid_eq/keep_policy smokes, `go test ./...`, exact CI shellcheck warning gate — all green locally; CI green on both platforms. Tier-1 grok-4.5: Approve with nits.
- **Next for whoever picks up: user decides merge of PR #20** (auto-merge denied by permission policy — agent-authored PR with model-only review). After merge: close F-H2 on the board, prune claim branch, sync peers. Reviewer nits NOT taken (recorded here as accepted residuals): newline/CR smoke variant (tab is representative), single-warn-per-path dedup, mid-dead-row documentation assert.

### 2026-07-11 `[claude]@mac-joyce` — F-H1 closed: PR #19 merged (`98c30aa`)
- User merged (squash); board updated (F-H1 → done, milestone marked complete), local + remote claim branches pruned, synced to mac-yiqi.
- Wave summary in the previous entry. Post-branch additions folded before merge: Tier-1 gemini OK with pick_keep comment MINOR fixed (`24591dd`), triage on the PR; CI green on both platforms including the new keep_policy_smoke on ext4.
- Remaining recorded follow-ups live in "Next up": vmeta-index unsafe-path guard (new, same class as F-H1 Task 2), vid_eq strict re-verify design decision + usage-string dedup, Go 422 mode-echo, optional test/comment polish, accepted residuals, trigger-gated perf deferrals. No active twincut work remains.

### 2026-07-11 `[claude]@mac-joyce` — F-H1 executed, PR open (in-review, awaiting Tier-1 + user merge)
- Follow-up hygiene wave (former "Next up" items 1-4) subagent-driven on `claude/followup-hygiene`. Commits: `1aecff3` pick_keep equal-mtime tie-break (LC_ALL=C path byte order at both similar-video sites) + `-k2,2`→`-k2` sort-key hygiene + new `tests/keep_policy_smoke.sh` (K1 pins the previously-untested Wave-3 hash-dupe tie-break; K2/K3 pin similar-video) wired into Makefile+CI; `6c302e4` tsv_path_safe guard extension (qmove/qdelete now cover `matched`; hash-index write loops skip unsafe paths; `\r` added everywhere) + stage9 D1c/D1d + backup-smoke hash-index test + one pre-authorized p0 stderr-text update; `72f337e` stage9 D1/D1b run_end asserts (characterized `succeeded` first); `65d2d5d` gofmt 9 ui files (verified whitespace-only).
- Reviews: per-task Approved ×4 (zero Critical/Important across all); final whole-branch (fable) "Yes — ready to merge" — reviewer independently re-ran all 9 shell suites + shellcheck + go checks green on a branch snapshot and behaviorally probed the source-loop guard; confirmed the Wave-3 guard-abort class is closed at all 13 qmove/qdelete dispatch sites.
- Notable: K2/K3 passed pre-fix on local APFS (find name-order coincides with byte order) — recorded per plan contingency; CI ubuntu/ext4 is where the pin discriminates. K1 asserts stderr only because the backup hash-dupe flow emits no dup_group event at all (product-forced, not test laziness).
- New follow-up recorded in "Next up": vmeta-index unsafe-path guard gap (found by final review, same class as Task 2, bounded consequences).
- Next: Tier-1 per TEAM.md §2 (eligible {gemini, grok}, default gemini), then user merge; after merge set F-H1 → done and git-sync.

### 2026-07-10 `[claude]@mac-joyce` — R-W3 closed: PR #18 merged (`aa010ff`) — remediation milestone COMPLETE
- User merged Wave 3; board updated (R-W3 → done, milestone marked complete), local branch pruned, synced to mac-yiqi.
- **All three remediation waves now merged**: W1 #16 (`9cf6928`, engine correctness), W2 #17 (`a780610`, Go UI security), W3 #18 (`aa010ff`, CI/hygiene/shellcheck). The 2026-07-05 full-repo assessment is fully remediated.
- Wave-3 highlight beyond plan: wiring `tests/json_events/run_tests.py` into CI ran it on Linux for the first time and caught 2 genuine pre-existing GNU/BSD portability bugs (GNU hasher `\`-prefix on backslash filenames corrupting hash extraction; find-order equal-mtime keep tie-break) — both fixed in-PR on source and backup paths, Tier-1 verified.
- Process note: agent-team gemini/grok review wrappers hit upstream model-ID rot during this milestone (gemini-2.5-flash 404, grok-build removed); fixed in agent-team PR #47 (→ gemini-flash-latest, grok-4.5). gemini later had transient network BLOCKs; grok-4.5 carried Tier-1 where gemini was down, per fail-closed policy (no silent substitution).
- Follow-ups (none blocking) live in the Status Board "Next up". No active twincut work remains.

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
