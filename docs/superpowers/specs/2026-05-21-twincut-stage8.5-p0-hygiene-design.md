# Stage 8.5: P0 Hygiene Fixes for Thumbnail-Detect Web UI — Design

**Status**: approved
**Date**: 2026-05-21
**Predecessor**: `2026-05-21-twincut-stage8-followup.md`
**Branch target**: `feature/stage-8.5-p0-hygiene` (new, off `feature/stage-8-thumbnail-ui`)

## Goal

Make the thumbnail-detect Web UI's preview→apply chain replayable, manifest-complete, and race-free. Three locked fixes. **No architectural changes** — Go/bash boundary redesign is deferred to Stage 9.

## Scope

### In scope

1. **Fix 1** — Under `--json-events`, L1 candidates flow through NDJSON events instead of the source-scoped, mutable `_review.csv`. Replay safety.
2. **Fix 2** — Apply TSV gains a 7th `keeper` column. L2/L3 hydrate from events; L1 stays empty (no paired keeper exists). Manifest completeness.
3. **Fix 3** — `RunManager.Start` accepts a caller-provided run ID with regex + existence validation. The post-Start rename in `handleThumbnailsApply` is removed. TOCTOU elimination.

### Out of scope (explicitly)

- Signed-token capability for apply requests — single-user-local threat model is adequate today.
- L1 grouping by perceptual hash — belongs to P1 wave 2.
- Go-owned workflow vs bash-owned-with-thin-viewer architectural decision — Stage 9.
- P2 nits #2/#3/#4 from the followup doc (orphan TSV cleanup, `fmt.Sscan`→`strconv.Atoi`, zero-dim writes) — defer to Stage 9 or convenience commits.

Out-of-scope items intentionally left untouched so this spec stays a single-PR worth of work.

## Fix 1 — L1 → NDJSON events (under `--json-events`)

### Current state

`thumb_detect_run` (in `lib/thumb.sh`) unconditionally calls `thumb_write_review`, which writes a 5-column TSV to `<source>/_thumbnails/_review.csv`. The file is source-scoped (lives next to the user's images) and mutable. `BuildResults` in `ui/server/results.go` reads this file whenever the run mode starts with `thumbnail_detect`, in order to materialize an `l1-suspects` group in the UI view.

Asymmetry: L2/L3 candidates already flow through run-scoped NDJSON events (`thumb_candidate`, decisions `thumb_l2_exif` / `thumb_l3_embed`). Only L1 still uses on-disk source state. Between preview and apply, that file can be overwritten by a second preview, by a CLI run with different thresholds, or by hand-editing — apply then composes the confirm TSV from drift, not from the journaled preview.

### Target state

- **bash** (`lib/thumb.sh`): `thumb_write_review` checks whether NDJSON event emission is active by reading the same flag `emit_event` itself consults — the implementation plan must grep `lib/thumb.sh` (and helpers it sources) to identify the actual variable name (`JSON_EVENTS`, `EMIT_JSON`, or similar) and reuse it; do not introduce a parallel flag. In events mode:
  - Emit one `thumb_candidate` event per L1 suspect with `decision=thumb_l1_review`.
  - Skip the `_review.csv` write entirely.
  In non-events mode (legacy CLI without `--json-events`):
  - Write `_review.csv` as today. Unchanged.
- **Go** (`ui/server/results.go`): the block under `if strings.HasPrefix(snap.Mode, "thumbnail_detect")` that opens `<source>/_thumbnails/_review.csv` is removed. The event-loop `case EventThumbCandidate` branch is extended to dispatch by `decision`:
  - `thumb_l1_review` → append into a synthetic `l1-suspects` group (preserving the existing UI structure)
  - `thumb_l2_exif` / `thumb_l3_embed` → existing per-group logic

### Event schema (L1)

```
{"type":"thumb_candidate","ts":<unix-ns>,"run_id":"<id>",
 "decision":"thumb_l1_review",
 "path":"<absolute path>",
 "reason":"l1_only_suspect" | "l1_only_maybe",
 "width":<int>,"height":<int>,"size_bytes":<int>}
```

No `keeper` field. L1 has no algorithmically determined keeper — this is intentional and the apply TSV will reflect it.

`group_id` is not set for L1 events. They aggregate into a single synthetic `l1-suspects` group on the Go side. This matches today's UI structure and avoids introducing L1 grouping (P1 wave 2 territory).

### Backward compatibility

- Legacy CLI users invoking `twincut --thumbnail-detect` without `--json-events` see no behavior change. The disk file continues to be written.
- Legacy CLI users invoking `twincut --thumb-confirm <file>` on a hand-edited disk file continue to work — that path doesn't go through `BuildResults`.
- Preview runs created **before** Stage 8.5 (journals with no `thumb_l1_review` events) will materialize an empty L1 group when re-rendered. Document this in the changelog: only Stage 8.5+ previews are replayable; older previews are non-replayable as designed and should be re-run.

## Fix 2 — Keeper column in apply TSV

### Current state

`composeThumbnailConfirmTSV` (in `ui/server/apply_list.go`) emits 6 columns: `path`, `reason`, `width`, `height`, `note`, `decision`. `thumb_confirm_review` (in `lib/thumb.sh`) parses with `awk -F'\t'`, extracts `$1` (path) and `$6` (decision), and calls `qmove "$p" "$THUMB_DIR" "" "" "$dec"` — the 3rd argument (`matched`, the kept original) is always empty. Manifests lose the relationship that justified the move.

### Target state — 7-column TSV

```
path  reason  width  height  note  decision  keeper
```

- L2 / L3 rows: `keeper` is populated from `ResultMember.Keeper`, which `BuildResults` fills from the `keeper` field of each `thumb_candidate` event. `UnmarshalThumbCandidate` already parses `Keeper`. `ResultMember` gains a `Keeper string` field.
- L1 rows: `keeper` is empty. There is no paired keeper for an L1 suspect; this is the correct semantic value.
- The field-guard loop in `composeThumbnailConfirmTSV` extends to `keeper` (tab/newline rejection).

### bash parser update

`thumb_confirm_review` reads a 7th column:

```bash
keeper="$(awk -F'\t' '{print $7}' <<< "$_raw_line")"
# ... existing decision validation ...
qmove "$p" "$THUMB_DIR" "$keeper" "" "$dec"
```

When the 7th column is absent (5- or 6-column legacy input), `awk` returns the empty string and `qmove` receives `matched=""` — identical to today's behavior. Backward compat preserved.

### Test impact

`tests/p1_thumb_smoke.sh` sections 9 and 9c (current 6-column format) gain a trailing `\t<keeper>` column. Section 9b (5-column legacy direct-confirm) stays unchanged — it never had keeper and never will.

## Fix 3 — Caller-provided run ID

### Current state

`RunManager.Start` always calls `newRunID()` internally. `handleThumbnailsApply` pre-generates `applyRunID` for the TSV filename, writes the TSV at `<stateDir>/runs/<applyRunID>.thumb-confirm.tsv`, calls `Start` (which generates a *different* ID), kicks off bash, and then attempts to rename the TSV to match `run.ID`. The bash child is reading the file under the original `applyRunID` path. If the rename completes before bash opens the file, bash dies with "review csv not found".

### Target state

`StartOptions` gains an optional `ID` field:

```go
type StartOptions struct {
    ID   string  // optional; empty → newRunID()
    Mode string
    Args []string
    Env  []string
}
```

`Start` validates a caller-provided ID:

```go
var runIDRegex = regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9]+$`)

id := opts.ID
if id == "" {
    id = newRunID()
} else {
    if !runIDRegex.MatchString(id) {
        return nil, fmt.Errorf("invalid caller-provided run ID: %q", id)
    }
    if _, err := os.Stat(filepath.Join(m.stateDir, "runs", id+".ndjson")); err == nil {
        return nil, fmt.Errorf("run journal already exists for ID: %q", id)
    }
}
// ... existing logic from this point ...
```

`handleThumbnailsApply`:

```go
applyRunID := newRunID()
tsvPath := filepath.Join(runsDir, applyRunID+".thumb-confirm.tsv")
if err := os.WriteFile(tsvPath, tsvData, 0o644); err != nil { /* ... */ }

run, err := s.runs.Start(StartOptions{
    ID:   applyRunID,
    Mode: "thumbnail_detect_apply",
    Args: args,
})
// post-Start rename block REMOVED
```

No rename → no race.

### Boundaries

- No existing caller of `Start` passes an ID, so default behavior is unchanged.
- The regex matches the exact shape `newRunID()` produces: 8-digit date, `T`, 6-digit time, `Z`, dash, lowercase alphanumeric suffix. This prevents path-traversal (no `/`, no `..`) and accidental cross-format inputs.
- Existence check rejects ID reuse, even though `newRunID()` collision is astronomically unlikely.

## Testing

### Bash smoke (`tests/p1_thumb_smoke.sh`)

**Modified**:
- Section 9 (apply TSV happy path): TSV fixture grows to 7 columns. L2/L3 rows carry `keeper`. L1 rows leave keeper empty.
- Section 9c (decision validation): same 7-column shape; the decision-allowlist assertions are unchanged.
- Section 9b (legacy 5-column direct-confirm): unchanged.

**New section** (slot after current section 11):
- `thumb_detect_run` with `--json-events` and `--dry-run`:
  - Assert `<source>/_thumbnails/_review.csv` is **not created**.
  - Assert journal NDJSON contains exactly N `thumb_candidate` events with `decision=thumb_l1_review`, where N = number of L1 suspects in the fixture.
- `thumb_detect_run` without `--json-events`:
  - Assert `_review.csv` is created with expected content. Regression guard for legacy CLI users.

### Go unit

`composeThumbnailConfirmTSV` table-driven cases:
- Group with only L2 members → keeper column populated from each member's `Keeper`.
- Group with only L3 members → same.
- L1-only synthetic group → keeper column empty.
- Mixed (real-world scenario): per-row keeper drawn from the member.
- Field guard: keeper containing tab → error; keeper containing newline → error.

`BuildResults`:
- New journal fixture containing `thumb_l1_review` events → assert `l1-suspects` group materialized with the right members.
- Assert `BuildResults` does NOT read `<source>/_thumbnails/_review.csv` from disk under any mode (delete or invert the existing disk fixture in older tests).

`RunManager.Start`:
- `opts.ID == ""` → generated ID, current behavior.
- `opts.ID` malformed (contains slash, wrong shape) → error.
- `opts.ID` valid but `<stateDir>/runs/<id>.ndjson` exists → error.
- `opts.ID` valid + journal absent → uses ID as-is.

`handleThumbnailsApply`:
- No rename: TSV at `<runsDir>/<applyRunID>.thumb-confirm.tsv` is still there after `Start` returns; same path is in the args slice passed to bash.

### Manual smoke (`tests/manual/stage8_smoke.md`)

New case appended after existing cases:

> **Replay regression**: Run preview twice on the same source dir with different thumbnail size thresholds. The two runs produce different L1 suspect sets. Confirm using `preview_run_id` from preview 1. Verify that quarantined files match preview 1's L1 set, not preview 2's. This is the core BLOCKER #1 regression test.

Also: after a successful L2/L3 apply, inspect the manifest TSV — confirm `matched=<keeper-path>` matches the keeper recorded in the original `thumb_candidate` event for that file. This validates Fix 2 end-to-end.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| Pre-8.5 preview runs (journals with no L1 events) re-render with empty L1 group | Document in changelog. UI is functionally fine — just no L1 suspects shown. Users re-run preview. |
| Legacy CLI hand-edits `_review.csv` then runs `--thumb-confirm` on it | Untouched. Disk file still written when `--json-events` is absent; bash parser handles `$7=""`. |
| Caller-provided ID with path traversal injection | regex validation rejects. Existence check is the secondary guard. |
| `_review.csv` left lying around in source dirs from past Stage 8 sessions | Cosmetic only. Not read by post-8.5 Go code. Users can delete manually. |

## References

- Followup doc: `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md`
- Stage 8 design: `docs/superpowers/specs/2026-05-20-twincut-stage8-thumbnail-ui-design.md`
- Stage 8 plan: `docs/superpowers/plans/2026-05-21-twincut-stage8-thumbnail-ui.md`
- Stage 8 branch: `feature/stage-8-thumbnail-ui` (24 commits ahead of `main`, beta status)

## Decision log

- 2026-05-21 — Three-segment roadmap accepted: Stage 8.5 (P0 hygiene, this doc) → P1 wave 2 (perceptual hash) → Stage 9 (Go-owned contract redesign). i18n and multi-pass sweep deferred.
- 2026-05-21 — Q1: L1 disk file under `--json-events` = **skip write entirely**. No double-writing.
- 2026-05-21 — Q2: L1 keeper column = **empty** (honest semantics; no paired keeper to invent). The BLOCKER applies only to L2/L3.
- 2026-05-21 — Q3: Caller-provided ID = **validate with regex + existence check** (cheap insurance).
- 2026-05-21 — Q4: P2 nits not folded into 8.5 (followup #6 items 2/3/4 deferred). Only nit #1 is implicitly fixed by Fix 1.
