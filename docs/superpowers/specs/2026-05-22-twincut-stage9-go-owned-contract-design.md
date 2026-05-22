# Stage 9 — Go-owned contract for thumbnail_detect

**Date**: 2026-05-22
**Predecessors**: Stage 8 (thumbnail UI, PR #4), Stage 8.5 (P0 hygiene, PR #7), P1 wave 2 (L1 pHash, PR #8 + #9)
**Followup target**: `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md` §7 strategic question
**Scope**: `thumbnail_detect` only (cross-check / self-check / restore unchanged)

## 1. Goal

Replace the current bash↔Go hybrid contract — where Go writes mutable on-disk artifacts that bash re-parses, and bash writes stringly-typed NDJSON that Go re-parses ad-hoc — with a single typed contract anchored in Go structs and validated by round-trip tests. `thumbnail_detect` is the proving ground; cross-check / self-check / restore are out of scope and will follow only if the pattern sticks.

Stage 8.5 fixed the three P0 surface bugs (`L1→NDJSON`, `keeper` column, caller-provided run ID) but the strategic question — "Go-owned or bash-owned?" — was deferred. The project chose **Go-owned**: bash retreats to a leaf primitive that emits typed events; Go orchestrates, holds the run journal, makes decisions, drives the UI.

## 2. Architecture

```
Stage 9 target (thumbnail_detect only):

                 ① scan: typed NDJSON on fd 3
┌──────────────┐ ──────────────────────────► ┌──────────────┐
│   bash       │                              │     Go       │
│ leaf prim    │                              │ orchestrator │
│  - scan      │                              │  + journal   │
│  - apply     │ ◄────────────────────────── │   + UI       │
└──────────────┘  ② apply input: stdin       └──────────────┘
                     JSON-lines commands
                  ③ apply: typed NDJSON action events back
```

Two boundaries change:

- **Scan boundary**: `twincut --thumbnail-detect --source X --json-events` continues to be one bash fork. All L1 / L2 / L3 candidates flow through the typed `thumb_candidate` event (this is where Stage 8.5 P0 #1 landed for L1; Stage 9 finishes the typing).
- **Apply boundary**: `twincut --thumbnail-detect-apply --json-events --json-in` reads JSON-lines from stdin (one planned move per line) and emits typed `action` events to fd 3. The on-disk `<stateDir>/runs/<id>.thumb-confirm.tsv` round-trip is deleted.

Schema authority: `ui/server/events.go` Go structs. Bash side gets one helper function per event type (`emit_thumb_candidate`, `emit_action_move`, etc.) — no more generic `emit_event "type" "k=v" ...` at call sites. A Go round-trip test against bash-emitted fixtures guards drift.

## 3. Components

### New files

| File | Responsibility |
|---|---|
| `lib/events.sh` | Typed bash helpers. One function per event type; signature mirrors the Go struct field-for-field. Source from `bin/twincut.sh`. |
| `tests/events_contract.sh` | Run each helper against canned inputs, write outputs to `tests/fixtures/events/<event_type>__<case>.ndjson`, assert byte-equal to checked-in golden file. |
| `tests/fixtures/events/*.ndjson` | One file per (event_type, case). Checked into git. |
| `ui/server/events_roundtrip_test.go` | Table-driven: iterate `tests/fixtures/events/*.ndjson`, unmarshal each line through `DisallowUnknownFields`, compare to hand-written expected struct via `reflect.DeepEqual`. |
| `tests/p1_stage9_smoke.sh` | End-to-end smoke for the new contract — preview scan + apply via stdin pipe, asserts journal contents + filesystem state. |

### Changed files

| File | Change |
|---|---|
| `bin/twincut.sh` | Source `lib/events.sh` at startup. Add `--json-in` flag to `--thumbnail-detect-apply`: when set, read JSON-lines commands from stdin instead of TSV file. Old TSV path stays through Step 4, deleted in Step 5. |
| `lib/thumb.sh` | Replace every `emit_event "thumb_candidate" "k=v" ...` call with `emit_thumb_candidate <args>`. Same for the other event types touched here. |
| `ui/server/events.go` | Add `ApplyCommand` input struct (typed schema for stdin JSON-lines). Tighten existing event structs if any fields are missing or untyped. |
| `ui/server/apply_list.go` | Add `composeApplyCommands(view *ResultsView) []byte` that returns JSON-lines bytes. Delete `composeThumbnailConfirmTSV` in Step 5. |
| `ui/server/thumbnail.go::handleThumbnailsApply` | Replace "write TSV + pass `--thumb-dir` flag" with "spawn bash + pipe `composeApplyCommands` bytes through stdin". Caller-provided run ID still applies (Stage 8.5 P0 #3). |
| `ui/server/runs.go::Start` | Add `Stdin io.Reader` option to `StartOptions` if absent; orchestrator threads `composeApplyCommands` bytes through. |
| `lib/thumb.sh::thumb_confirm_review` | Deleted in Step 5 (replaced by inline JSON-lines processing inside `--thumbnail-detect-apply --json-in`). |

### Artifacts removed

- `<stateDir>/runs/<id>.thumb-confirm.tsv` — gone. Stdin pipe replaces it. This also closes stage8-followup item #3 (TOCTOU residual) and item #6 (orphan TSV TTL).
- `composeThumbnailConfirmTSV` function + its tests.

### Artifacts kept (bash-private caches, never a contract surface)

- `<source>/.thumb_index.tsv` — L1 scan cache
- `<source>/.thumb_phash_index.tsv` — pHash cache (P1 wave 2)
- `<source>/.video_meta_index.csv` — video metadata cache
- `<backup>/.backup_hashindex.txt`, `<source>/.source_hashindex.txt` — hash caches

### Legacy CLI path unchanged

`bin/twincut.sh --thumbnail-detect` without `--json-events` still writes `<source>/_thumbnails/_review.csv` for the headless-CLI eyeball workflow. Stage 9 only refactors the `--json-events` channel.

## 4. Data flow

### 4.1 Scan

```
Go: handleThumbnailsScan
  ├─ runs.Start(cmd=twincut,
  │             args=[--thumbnail-detect --source X --json-events ...],
  │             stdin=nil,
  │             journalTo=<stateDir>/runs/<runID>/events.ndjson)
  │
  └─ bash fork:
       source lib/events.sh
       emit_run_start mode=thumbnail_detect_preview source=X run_id=<runID>
       ...L1+L2+L3 scan loops...
         emit_thumb_candidate decision=thumb_l2_exif ...
         emit_thumb_candidate decision=thumb_l1_review ... phash_distance=3 ...
       emit_progress phase=scan done=42 total=100 current_path=...
       emit_run_end run_id=<runID> status=succeeded duration_ms=...

Go SSE tails events.ndjson to the browser in real time.
```

Example wire format (typical `thumb_candidate`):

```json
{"type":"thumb_candidate","ts":1747934400,"run_id":"r_abc",
 "decision":"thumb_l1_review","path":"/img/IMG_0001.JPG","keeper":"/img/IMG_0001.HEIC",
 "group_id":"l1ph:abcd1234","phash_distance":3,
 "width":320,"height":240,"size_bytes":18432,"reason":"l1_phash_match"}
```

The schema authority is `ui/server/events.go::ThumbCandidate`. The bash helper `emit_thumb_candidate` has matching argument slots 1:1. `ts` is Unix epoch seconds (`int64`, matching the current `EventEnvelope.TS` field — no format change at the schema level in Stage 9).

### 4.2 Apply

```
Go: handleThumbnailsApply
  preview_run_id validated (mode + status, Stage 8.5 P0 #3)
  ├─ cmds := composeApplyCommands(view)   // []byte, JSON-lines
  │
  └─ runs.Start(cmd=twincut,
                args=[--thumbnail-detect-apply --json-events --json-in --source X],
                stdin=bytes.NewReader(cmds),
                journalTo=<stateDir>/runs/<applyRunID>/events.ndjson)

  bash fork:
    source lib/events.sh
    emit_run_start mode=thumbnail_detect_apply run_id=<applyRunID>
    while IFS= read -r line; do
      type=$(jq -r '.type' <<<"$line")
      case "$type" in
        apply_move)
          src=$(jq -r '.src' <<<"$line")
          dst_dir=$(jq -r '.dst_dir' <<<"$line")
          keeper=$(jq -r '.keeper // ""' <<<"$line")
          decision=$(jq -r '.decision' <<<"$line")
          qmove "$src" "$dst_dir" "$keeper" "" "$decision"
          # qmove itself emits the action event with the computed dst.
          ;;
        apply_skip)
          src=$(jq -r '.src' <<<"$line")
          decision=$(jq -r '.decision' <<<"$line")
          emit_action_skip src="$src" decision="$decision" reason="user_override"
          ;;
      esac
    done
    emit_run_end run_id=<applyRunID> status=succeeded total=N applied=M skipped=K
```

Note: `qmove SRC DEST_DIR MATCHED HASH DECISION` (the current 5-positional signature; computes the actual destination filename inside `DEST_DIR` itself, handles name-collision suffix, hardlink safety, manifest append, and emits its own `action` event). `ApplyCommand` mirrors that shape — Go does NOT pre-compute the final destination filename.

Input wire format (`ApplyCommand`):

```json
{"type":"apply_move","src":"/img/IMG_0001.JPG","dst_dir":"/img/_QUARANTINE/_thumbnails",
 "keeper":"/img/IMG_0001.HEIC","decision":"thumb_l1_review"}
```

```json
{"type":"apply_skip","src":"/img/IMG_0002.JPG","decision":"keep_user_override"}
```

Fields:
- `src` (required): source absolute path
- `dst_dir` (required for `apply_move`): destination directory; bash picks the final filename inside it
- `keeper` (optional, empty string allowed): the matched file for hardlink-safety + manifest `matched` column
- `decision` (required): one of the allowed decision tags (`thumb_l1_review`, `thumb_l2_exif`, `thumb_l3_embed`, `thumb_confirmed`, `keep_user_override`, …). Bash validates against the allowed set; unknown values → emit `error code=apply_failed`, continue.

### 4.3 Error handling

- **Per-line apply failure** (e.g. `qmove` returns non-zero, or `jq` parse error): emit `error code=apply_failed src=... detail=...`, continue with next line. Matches existing tolerance semantics.
- **`run_end.status`**: always `succeeded` if the bash process exited 0, even with partial errors. Go aggregates the `error` event count for the UI status badge.
- **Stdin closed early** (Go process killed mid-pipe): bash sees EOF on the read loop, emits `run_end status=interrupted` with the partial counts.
- **Bash fatal** (syntax error, unbound var): non-zero exit code; Go marks the run failed via its existing exit-code handler.

### 4.4 Replay

`<stateDir>/runs/<applyRunID>/events.ndjson` is the authoritative journal. On server restart, Go rebuilds the apply-run's UI state by re-reading the journal. Re-applying the same preview means re-composing `ApplyCommand` from the (immutable) preview run's events.ndjson — deterministic by construction.

## 5. Testing strategy

Three layers, contract-first.

### 5.1 Helper unit tests (`tests/events_contract.sh`)

For each `emit_*` helper:

```bash
# Example
TWINCUT_TEST_TS="2026-05-22T17:30:00Z" \
TWINCUT_TEST_RUN_ID="r_test" \
emit_thumb_candidate \
  --decision thumb_l1_review \
  --path /img/a.jpg --keeper /img/a.heic \
  --group-id l1ph:abc --phash-distance 3 \
  --width 320 --height 240 --size-bytes 18432 \
  --reason l1_phash_match \
  > /tmp/out.ndjson

diff -u tests/fixtures/events/thumb_candidate__l1_phash.ndjson /tmp/out.ndjson
```

`ts` and `run_id` honor `TWINCUT_TEST_TS` / `TWINCUT_TEST_RUN_ID` env overrides so fixtures stay byte-stable. Initial coverage: ~8 event types × 1-3 cases each.

### 5.2 Go round-trip (`ui/server/events_roundtrip_test.go`)

Table-driven; reads each `tests/fixtures/events/*.ndjson`:

- Each line unmarshals into `EventEnvelope`; routed to the typed payload struct by the `"type"` field.
- `json.Decoder.DisallowUnknownFields()` — any field bash emits that Go doesn't model → red.
- For each Go struct field with `omitempty`, fixtures must contain at least one case where the field is non-empty (forces coverage).
- Final assertion uses `reflect.DeepEqual` against hand-written expected struct literals.

This is the single test that catches drift in either direction.

### 5.3 End-to-end smoke (`tests/p1_stage9_smoke.sh`)

A new smoke that exercises the full pipeline:

1. Fixture image dir (gradient PNGs, 1 keeper + 2 suspects + 1 unrelated).
2. Run `twincut --thumbnail-detect --source ... --json-events --thumb-fd-3-out /tmp/scan.ndjson`.
3. Assert: every line's `type` is in the allowed set; every `thumb_candidate` has a non-empty `path`; pHash distance bounds respected.
4. Compose `ApplyCommand` JSON-lines from the scan output, pipe through `twincut --thumbnail-detect-apply --json-in --json-events`, capture fd-3 output.
5. Assert: `action` events match the input commands; quarantine directory contains the expected files; source no longer contains the moved files; manifest TSV has the expected rows.

`p1_thumb_phash_smoke.sh` (26 sections) does not change — protect it from collateral damage during the migration.

### 5.4 Existing Go server tests

- `apply_list_test.go` — `composeThumbnailConfirmTSV` tests → deleted; new tests for `composeApplyCommands` (byte-exact JSON-lines + round-trip Unmarshal).
- `thumbnail_test.go::TestHandleThumbnailsApply` — argv assertions adjusted; new assertion that the stdin pipe bytes match expected JSON-lines.
- `events_test.go` — add Marshal/Unmarshal tests for `ApplyCommand`.

### 5.5 Convention

Going forward, any new event type or `ApplyCommand` variant must ship with: a Go struct, a `lib/events.sh` helper, ≥1 fixture file, and the Go round-trip test entry. The convention is documented at the top of `lib/events.sh` and as a package doc comment on `ui/server/events.go`.

## 6. Migration sequencing

Six steps, each a self-contained commit or small PR. Tests stay green at every step. Step N rollback leaves Step N-1 fully functional.

### Step 1 — Schema skeleton (additive, zero behavior change)

- Create `lib/events.sh` with all helpers; internally they delegate to the existing `emit_event` wrapper.
- Add `tests/events_contract.sh` + initial fixtures.
- Add `events_roundtrip_test.go`.
- No call sites change.
- **Verification**: existing smokes green + new tests green.

### Step 2 — bash call-site migration

- Replace every `emit_event "thumb_candidate" ...`, `emit_event "action" ...`, etc. in `lib/thumb.sh` and `bin/twincut.sh` with the corresponding `emit_*` helper call.
- Mark the generic `emit_event` deprecated (comment + grep-based guard in a smoke section).
- **Verification**: `p1_thumb_phash_smoke.sh` 26/26 + Go tests + new round-trip green.

### Step 3 — apply `--json-in` channel (new path parallel to old)

- `bin/twincut.sh --thumbnail-detect-apply` gains `--json-in`. With the flag set: read stdin JSON-lines. Without it: legacy TSV path.
- Shared inner loop for the move/skip work; only the input adapter differs.
- New `lib/events.sh::ApplyCommand` parse helper (bash side, jq-backed).
- Add `tests/p1_stage9_smoke.sh` covering the new channel.
- **Verification**: legacy thumbnail UI Go tests still pass; new smoke passes.

### Step 4 — Go side switches to stdin pipe

- `ui/server/apply_list.go::composeApplyCommands` lands (old `composeThumbnailConfirmTSV` still present).
- `ui/server/thumbnail.go::handleThumbnailsApply` switches: no TSV write, no `--thumb-dir` / `--thumb-confirm` flags, stdin pipe carries the commands.
- `ui/server/runs.go::Start` accepts `Stdin io.Reader`.
- **Verification**: Go end-to-end test + smoke green. From this point, all new apply runs use the new channel.

### Step 5 — delete the old code paths

- Remove `composeThumbnailConfirmTSV` and its tests.
- Remove `lib/thumb.sh::thumb_confirm_review`.
- Remove `bin/twincut.sh`'s `--thumb-confirm <file>` handling (keep the flag name for one release with a usage-error emit, or drop directly — see decision below).
- Promote deprecation note on `emit_event` to "unused, remove on next refactor".
- **Verification**: smoke + Go tests green.

### Step 6 — close-out

- `CLAUDE.md` gets a Stage 9 paragraph documenting the new contract.
- `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md` items #1, #2, #3, #6 marked fully closed; §7 strategic question marked resolved.
- Run `reviewer-gemini` on the cumulative diff.
- PR to main as Stage 9.

## 7. Decision log

Decisions locked during 2026-05-22 brainstorm session, in order:

| Axis | Decided | Rejected |
|---|---|---|
| Scope | Only `thumbnail_detect` | (a) add one more workflow; (b) all three workflows |
| Schema source of truth | Go struct + round-trip tests against bash fixtures | (a) external JSON Schema file; (b) Go-generated JSON Schema for bash CI validation |
| Bash granularity | Medium — keep `--thumbnail-detect` and `--thumbnail-detect-apply` as scan/apply entrypoints, but apply input becomes stdin JSON-lines and on-disk `.thumb-confirm.tsv` goes away | (a) Coarse: keep current TSV input, only type the NDJSON output; (b) Fine: full atomization (`--move`, `--hash`, `--phash`, `--probe-video` each as own command) |
| Apply transactional model | One bash fork, stdin JSON-lines, internal loop, error per row → continue | (a) N forks of single-file `--move`; (b) both APIs side by side |
| `ApplyCommand.dst_dir` granularity | Per-command destination *directory* (bash picks filename inside via existing `qmove` collision logic) | (a) Per-run `--thumb-dir` flag like today; (b) Go computes final filename and sends full `dst` path |
| `--thumb-confirm <file>` flag fate | Drop the flag in Step 5 (no deprecation period — twincut is single-user, no external consumers) | Keep with usage-error emit for one release |
| Legacy CLI `_review.csv` | Untouched | Drop / unify with NDJSON channel |
| Schema versioning | None (Go + bash always co-versioned; single-user tool) | Add `schema_version` field to envelope |

## 8. Scope boundaries

**In scope**:
- `thumbnail_detect` scan + apply contract
- Typed `lib/events.sh` helpers
- `ApplyCommand` input schema
- Round-trip + contract + smoke tests
- Removing `.thumb-confirm.tsv` and `composeThumbnailConfirmTSV`

**Out of scope** (defer to follow-up stages or sweeps):
- `cross-check`, `self-check`, `restore` contract migration
- Removing legacy `_review.csv` write
- stage8-followup item #4 (capability token) — fine for single-user
- stage8-followup item #6 P2 nits (`fmt.Sscan`, csv warning, dim parsing) — P2 sweep
- flock for concurrent twincut runs
- Pillow/imagehash pip pinning

## 9. References

- `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md` — Stage 8 architectural backlog, §7 strategic question
- `docs/superpowers/specs/2026-05-21-twincut-p1-wave2-l1-perceptual-hash-design.md` — P1 wave 2 spec (immediate predecessor)
- `docs/superpowers/plans/2026-05-21-twincut-stage8.5-p0-hygiene.md` — Stage 8.5 plan (closed items #1/#2/#3 from stage8-followup)
- `ui/server/events.go` — current event struct definitions
- `bin/twincut.sh::emit_event` — current generic emitter (~line 189)
- `lib/thumb.sh::thumb_confirm_review` — current TSV consumer (to be deleted)
- `ui/server/apply_list.go::composeThumbnailConfirmTSV` — current TSV producer (to be deleted)
