# Stage 8 follow-up — architectural backlog

After Stage 8 landed (`feature/stage-8-thumbnail-ui`, 19 commits), two reviewers — `reviewer-gemini` (Google) and `reviewer-codex` (OpenAI) — went over the full diff. This document captures what they flagged, what was fixed in the same session, and what remains. The remaining items are not blockers for using Stage 8 as a feature gate, but Codex's verdict was needs-attention/no-ship for production, and the unfixed items here are why.

## What got fixed this session

Four follow-up commits land on the stage-8 branch immediately after the original 15 tasks:

- `f0e9270` + `54508af` — **fix P0 #1 (gemini)**: confirm-CSV → TSV. `composeThumbnailConfirmCSV` renamed to `composeThumbnailConfirmTSV`, uses `strings.Join(row, "\t")` matching `writeApplyList`. Bash `thumb_confirm_review` reads via `awk -F'\t'` to avoid IFS tab-collapse on empty fields. `thumb_write_review` and `BuildResults` reader both switched to TSV in lockstep. Tab/newline in path fields rejected at compose time. File extension `.thumb-confirm.csv` → `.thumb-confirm.tsv`.
- `83b9315` — **fix P0 #2 (gemini) + MAJOR flag-drift (codex)**: apply args gain `--thumb-dir <source>/_thumbnails` and `--source <source>`. Preview args renamed `--max-edge` → `--thumb-max-edge` (and the two siblings). Tests assert argv shape.
- `0e5be12` — **fix MAJOR capability check (codex)**: `handleThumbnailsApply` gates on `prevSnap.Mode == "thumbnail_detect_preview"` AND `Status != Running` AND `Status == Succeeded`. Returns 422 / 409 / 422 for the three rejection paths.

## What remains open

### 1. Mutable, source-scoped `_review.csv` makes preview→apply non-replayable

**Source**: codex BLOCKER #1.
**Code**: `lib/thumb.sh::thumb_write_review` writes `<source>/_thumbnails/_review.csv`. `ui/server/results.go::BuildResults` re-reads this file every time the apply handler rebuilds the result view from the preview run.

The file is source-scoped (lives next to the user's images) and mutable. A second preview, a CLI run with different thresholds, or a hand-edit between preview-1 and apply-1 changes what the user's saved `preview_run_id` "shows". Apply composes the TSV from the current view → can quarantine paths that were never in the preview the user confirmed.

The Stage 8 design assumed L1 → review.csv was fine because it predated L2/L3 NDJSON. The asymmetry is the real problem: L2 and L3 are run-scoped (journal events), L1 is source-scoped (on-disk file). Codex called this "the architectural asymmetry behind both gemini P0s" — which it is, even though we patched the surface symptoms.

**Fix options:**
- **A (small)**: change `thumb_write_review` to skip the file write during `--dry-run` and emit one `thumb_candidate` NDJSON event per L1 suspect (decision `thumb_l1_review`). `BuildResults` reads L1 from events, not disk. The on-disk file remains only for legacy CLI flows (`twincut --thumbnail-detect` without `--json-events`).
- **B (medium)**: move the on-disk review file under the run state dir, keyed by run ID (`<stateDir>/runs/<id>.review.tsv`). Bash writes it there when `--json-events` is set; the in-source `_review.csv` is reserved for headless-CLI users only.
- **C (big)**: drop the L1 disk artifact entirely; everything flows through NDJSON.

Recommended: **A**. Smallest blast radius, fixes the replay invariant.

### 2. Confirm TSV still drops `keeper`

**Source**: codex BLOCKER #2 (corroborates gemini P1).
**Code**: `composeThumbnailConfirmTSV` in `ui/server/apply_list.go`, 6 columns `path,reason,width,height,note,decision`. `thumb_confirm_review` in `lib/thumb.sh` calls `qmove "$p" "$THUMB_DIR" "" "" "$dec"` — empty `matched` arg.

`thumb_candidate` events carry `keeper`. The direct-CLI path passes `keeper` to `qmove` for hardlink-safety skips, action-event evidence, and manifest entries. The web-UI apply path flattens to a 6-column TSV that has no keeper field → `qmove` gets an empty `matched` arg → manifest loses the relationship that justified the move.

**Fix**: add a 7th column `keeper` to the TSV. `composeThumbnailConfirmTSV` reads `m.Keeper` (or looks up via `g.Members[0]` when role==keeper). `thumb_confirm_review` passes it to `qmove` as `matched`. Smoke test sections 9/9b/9c need an extra `\t<keeper>` column; 9b (legacy 5-column) is unaffected since it never had keeper.

This pairs naturally with fix #1 — once L1 flows through NDJSON, the apply path can build the TSV directly from journaled events with keeper attached, instead of round-tripping through `_review.csv`.

### 3. TOCTOU race between Go's CSV write+rename and bash child opening the file

**Source**: gemini P1.
**Code**: `ui/server/thumbnail.go::handleThumbnailsApply` writes the TSV to `<stateDir>/runs/<applyRunID>.thumb-confirm.tsv`, then calls `s.runs.Start(...)`. `Start` returns once `cmd.Start` returns (fork done; bash may not have opened the file yet). Then we rename `applyRunID` to `run.ID` (which is *always* different — `applyRunID` is generated locally, `Start` calls `newRunID()` internally, no shared ID).

If the rename completes before bash opens the file under its original path, bash dies with "review csv not found". The window is small but real on a loaded system.

**Fix options:**
- **A (preferred)**: extend `StartOptions` to accept a caller-provided ID, so `applyRunID` becomes `run.ID` and no rename is needed. The PreviewID-from-form is already validated upstream; injecting a pre-generated ID is safe.
- **B**: compute the TSV path *after* `Start` returns and write to `run.ID` directly. Means the args slice has to be patched post-Start, or the TSV write moves into the run-launch goroutine.
- **C (band-aid)**: file-lock the TSV during write, retry rename. Doesn't fix the root issue and adds complexity.

Recommended: **A**.

### 4. Trust boundary uses preview_run_id as a capability without a token

**Source**: codex MAJOR (partially addressed in `0e5be12`).
**Code**: `handleThumbnailsApply` checks mode + status of `preview_run_id`. That closes the "stale/wrong-mode" attack. It does NOT close: the run could be mutated between mode-check and CSV compose (concurrent restart of the server reading the journal back differently), or the form could carry a `preview_run_id` from a different user session if multi-user is ever added.

Right now twincut is single-user local. The current fix is adequate for that threat model. If we ever add auth or multi-tenant, the apply capability needs a server-issued token tied to the immutable preview artifact (signed run-ID + revision counter). Note for future stages.

### 5. `thumb_write_review` runs in dry-run, side-effecting source folder

**Source**: gemini P1 (subset of #1 above).
**Code**: `thumb_detect_run` in `lib/thumb.sh` invokes `thumb_write_review` unconditionally — even when `DRY_RUN=true`. The preview writes `<source>/_thumbnails/_review.csv` (now TSV) into the user's image folder.

Subsumed by fix option 1.A — if L1 flows through NDJSON in dry-run, the file isn't written.

### 6. Small data-quality issues (P2 from gemini)

- `csv.NewReader` warning swallow in `BuildResults` L1 read loop — on non-EOF errors, the loop just `break`s. Should push a `ResultWarn{Code: "l1_csv_truncated"}`.
- Orphaned `.thumb-confirm.tsv` files under `<stateDir>/runs/` when apply runs fail or are cancelled — needs a TTL sweep or on-end cleanup.
- `fmt.Sscan` for width/height parse → `strconv.Atoi` would be cheaper and surface errors. Currently the `Sscan` errors are silently swallowed (zero values on parse fail are acceptable).
- `composeThumbnailConfirmTSV` writes `strconv.Itoa(0)` when member dims are zero — fine for bash (ignored) but the manifest never sees actual dimensions.

These are real but not blockers. Fix when convenient.

## Strategic question: Go-owned or bash-owned?

Codex's deepest observation was that the current architecture has Go reading mutable bash-side state while bash reads structured Go-written files. "Worst of both."

Two ways out, neither is in scope for this branch:

- **Go-owned workflow, bash as leaf primitive.** Bash exposes only well-typed operations (`twincut thumbnail detect --json --source X` emits NDJSON; `twincut move <src> <dst> --reason <r> --keeper <k>` performs one quarantine move). Go orchestrates, holds the run journal, makes the decisions. Templates render off Go state. The `_review.csv` round-trip disappears because there is no on-disk intermediate.

- **Bash-owned workflow, Go as thin viewer.** Bash holds all state (manifest TSVs, run NDJSON, review files) and Go is a read-only renderer over them. Apply is just `twincut --thumb-confirm <file>`; the Go side never writes anything but UI state.

The current hybrid will keep producing this class of bug (CSV-parser drift, flag-name drift, file-format drift) every time a new workflow is added. P1 wave 2 (perceptual hash) and Stage 9 (multi-pass sweeps) will both want new event kinds + new apply formats. Without a typed shared schema, the drift keeps compounding.

**Recommended next step (separate plan)**: write a Stage 8.5 design spec that picks one side, defines a typed contract for the apply operation (single record per move with path, keeper, decision, evidence, dims, run_id), and migrates `thumbnail_detect` to it as the proving ground. If it sticks, retrofit self-check and cross-check. If it doesn't, we learn cheaply.

## Decision

Stage 8 is shippable as a feature flag / opt-in mode for testing, with the four follow-up commits above. It is not yet a "blessed" workflow on par with self-check and cross-check. Items 1-3 (L1→NDJSON, keeper column, TOCTOU fix) should be the first three tasks of a Stage 8.5 plan before the Thumbnails tab is removed from any "beta" labeling.

Items 4-6 can be addressed in parallel or deferred. Item 7 (Go-owned vs bash-owned) is a planning question for after Stage 8.5 — needs the typed contract to be concrete before deciding.

## References

- Gemini review: full log at `~/.claude/logs/gemini-usage.log` (2026-05-21 `review:stage8-thumbnail-ui-full`)
- Codex review: full log at `~/.claude/logs/codex-usage.log` (2026-05-21 `adversarial-review:stage-8 thumbnail-ui Go/bash contract`)
- Stage 8 design spec: `docs/superpowers/specs/2026-05-20-twincut-stage8-thumbnail-ui-design.md`
- Stage 8 implementation plan: `docs/superpowers/plans/2026-05-21-twincut-stage8-thumbnail-ui.md`
- Follow-up commits on branch `feature/stage-8-thumbnail-ui`: `f0e9270`, `54508af`, `83b9315`, `0e5be12`
