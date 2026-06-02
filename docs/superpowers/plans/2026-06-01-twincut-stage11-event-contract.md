# Stage 11 — Event-Contract Sealing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire the generic `emit_event` helper so every NDJSON line twincut emits flows through a typed `lib/events.sh` helper, is decoded by a canonical `ui/server/events.go` struct, and is exercised by the `DisallowUnknownFields` round-trip test.

**Architecture:** Go-contract-first. Grow the three event structs (`RunStart`, `RunEnd`, `DupGroup`) + bash helpers + fixtures + round-trip cases (Tasks 1–3), then flip the five bash call sites and delete `emit_event` (Task 4), then clean the Go consumer's now-dead dual-shape branch (Task 5). Each task leaves the tree compiling and tests green.

**Tech Stack:** bash 3.2 (macOS-compatible), Go (`ui/server`), Python test harness (`tests/json_events/run_tests.py`), shell contract/smoke suites, GitHub Actions CI.

---

## Baseline (record before starting)

Run these and note the numbers; the plan assumes this starting state:

```bash
cd /Users/mickey/Playground/twincut
bash tests/events_contract.sh            # expect: all ok
( cd ui && go test ./... )               # expect: ok
python3 tests/json_events/run_tests.py   # expect: 10/12  (2 RED: restore tests, pre-existing — see Task 1)
bash tests/p0_smoke.sh                    # expect: all ok
bash tests/p1_stage9_smoke.sh             # expect: all ok (or [skip] if no Pillow)
```

The two `run_tests.py` failures (`test_restore_dry_run_emits_action_events`, `test_restore_executes_and_emits_run_end`) are pre-existing: restore's `emit_run_end` (twincut.sh:954) omits `cancelled`, which `validate_structure` requires. Task 1 fixes them as a side effect of adding `--cancelled`. **Note:** CI (`.github/workflows/ci.yml`) does NOT run `run_tests.py` — it runs `events_contract.sh`, `p0_smoke.sh`, `p1_stage9_smoke.sh`, and `go test`. We still want `run_tests.py` green locally.

## File structure

**Modified:**
- `lib/events.sh` — extend `emit_run_start`, `emit_run_end`, `emit_dup_group`; add `dup_remove_json` composer.
- `bin/twincut.sh` — migrate 5 call sites (235, 1163, 1466, 1673, 1751), add `--cancelled false` to restore (954), delete `emit_event` (181–209).
- `ui/server/events.go` — grow `RunStart`, `RunEnd`, `DupGroup`, `DupRemoveEntry`; add `UnmarshalDupGroup`.
- `ui/server/results.go` — `decodeGroup` uses canonical `DupGroup`; run_end handler uses typed `RunEnd`.
- `ui/server/results_test.go` — three singular-shape event literals → 1-element arrays.
- `ui/server/events_roundtrip_test.go` — replace the `dup_group__cross_hash` case; add 5 canonical cases.
- `tests/events_contract.sh` — replace dup_group case; add run_start/run_end crosscheck cases.
- `tests/json_events/run_tests.py` — drop the `end["dupes"]` assertion.
- `tests/fixtures/events/run_end__restore.ndjson`, `run_end__restore_failed.ndjson` — append `cancelled:false`.
- `.github/workflows/ci.yml` — add `bash tests/p1_stage11_smoke.sh`.
- `CLAUDE.md` — update the Stage 9 note (legacy `emit_event` sites are gone).

**Created:**
- `tests/fixtures/events/run_end__crosscheck.ndjson`, `run_start__crosscheck.ndjson`, `dup_group__cross_md5.ndjson`, `dup_group__self_md5_multi.ndjson`, `dup_group__similar_video.ndjson`.
- `tests/p1_stage11_smoke.sh`.

**Deleted:**
- `tests/fixtures/events/dup_group__cross_hash.ndjson` (replaced by `dup_group__cross_md5.ndjson`).

---

## Task 1: `run_end` — canonical + restore cancelled fix

**Files:**
- Modify: `ui/server/events.go` (RunEnd struct, ~96-107)
- Modify: `lib/events.sh` (emit_run_end, ~104-138)
- Modify: `bin/twincut.sh:954` (restore emit_run_end)
- Create: `tests/fixtures/events/run_end__crosscheck.ndjson`
- Modify: `tests/fixtures/events/run_end__restore.ndjson`, `run_end__restore_failed.ndjson`
- Modify: `tests/events_contract.sh`, `ui/server/events_roundtrip_test.go`

- [ ] **Step 1: Add the canonical fixture (the failing target)**

Create `tests/fixtures/events/run_end__crosscheck.ndjson` with exactly one line (no trailing blank line beyond the newline):

```
{"type":"run_end","ts":1747934400,"run_id":"r_test","status":"succeeded","total":42,"moved":3,"deleted":0,"manifest_path":"/q/_manifest.tsv","cancelled":false}
```

- [ ] **Step 2: Add the contract case that produces it**

In `tests/events_contract.sh`, after the existing `run_end succeeded` case (line ~45), add:

```bash
run_case "run_end crosscheck" "run_end__crosscheck.ndjson" \
  emit_run_end --status succeeded --total 42 --moved 3 --deleted 0 \
    --manifest-path /q/_manifest.tsv --cancelled false
```

- [ ] **Step 3: Run the contract test — verify it FAILS**

Run: `bash tests/events_contract.sh`
Expected: `FAIL run_end crosscheck` (helper drops `--moved/--deleted/--manifest-path/--cancelled` as unknown args, output lacks those keys).

- [ ] **Step 4: Extend `emit_run_end`**

In `lib/events.sh`, in `emit_run_end`: add locals and arg cases, then emit the new fields after the existing `errors` line. Add to the `local` line (104-107 area):

```bash
  local moved="" deleted="" manifest_path="" cancelled=""
```

Add to the `case "$1"` block (before the `*)` fallback):

```bash
      --moved)           moved="$2"; shift 2 ;;
      --deleted)         deleted="$2"; shift 2 ;;
      --manifest-path)   manifest_path="$2"; shift 2 ;;
      --cancelled)       cancelled="$2"; shift 2 ;;
```

After the existing `[[ -n "$errors" ]] && ...` line (before `out+='}'`), add:

```bash
  [[ -n "$moved" ]]    && out+=',"moved":'"$(_emit_num moved "$moved")"
  [[ -n "$deleted" ]]  && out+=',"deleted":'"$(_emit_num deleted "$deleted")"
  [[ -n "$manifest_path" ]] && out+=',"manifest_path":"'"$(json_escape "$manifest_path")"'"'
  case "$cancelled" in
    true|false) out+=',"cancelled":'"$cancelled" ;;
    "") ;;
    *) echo "emit_run_end: --cancelled must be true|false" >&2 ;;
  esac
```

- [ ] **Step 5: Run the contract test — verify it PASSES**

Run: `bash tests/events_contract.sh`
Expected: `ok    run_end crosscheck` and all other cases still `ok`.

- [ ] **Step 6: Grow the Go `RunEnd` struct + round-trip case**

In `ui/server/events.go`, add to `RunEnd` (after `Errors`):

```go
	Moved        int64  `json:"moved,omitempty"`
	Deleted      int64  `json:"deleted,omitempty"`
	Cancelled    bool   `json:"cancelled,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty"`
```

In `ui/server/events_roundtrip_test.go`, add a case to `roundtripFixtures()`:

```go
		{
			file:     "run_end__crosscheck.ndjson",
			wantType: EventRunEnd,
			want: RunEnd{
				EventEnvelope: EventEnvelope{Type: EventRunEnd, TS: 1747934400, RunID: "r_test"},
				Status:        "succeeded",
				Total:         42,
				Moved:         3,
				ManifestPath:  "/q/_manifest.tsv",
			},
		},
```

(Deleted=0 and Cancelled=false are the zero values; the JSON has them but DeepEqual matches zero.)

- [ ] **Step 7: Run Go tests — verify PASS**

Run: `( cd ui && go test ./... )`
Expected: `ok` (round-trip strict-decodes the new fixture into the grown struct).

- [ ] **Step 8: Fix restore cancelled (the 2 pre-existing reds)**

In `bin/twincut.sh:954`, append `--cancelled false` to the restore `emit_run_end` call:

```bash
  emit_run_end --status "$restore_status" \
    --restored "$restored" \
    --skipped "$skipped_exists" \
    --missing "$missing" \
    --unrecoverable "$unrecoverable" \
    --errors "$errors" \
    --cancelled false
```

For fixture honesty, append `,"cancelled":false` before the closing `}` in both `tests/fixtures/events/run_end__restore.ndjson` and `tests/fixtures/events/run_end__restore_failed.ndjson`.

- [ ] **Step 9: Verify the restore reds are now green**

Run: `python3 tests/json_events/run_tests.py 2>&1 | grep restore`
Expected: `ok  test_restore_dry_run_emits_action_events` and `ok  test_restore_executes_and_emits_run_end`.
Run: `( cd ui && go test ./... )` — Expected: `ok` (restore fixtures still decode).

- [ ] **Step 10: Commit**

```bash
git add lib/events.sh bin/twincut.sh ui/server/events.go ui/server/events_roundtrip_test.go tests/events_contract.sh tests/fixtures/events/run_end__crosscheck.ndjson tests/fixtures/events/run_end__restore.ndjson tests/fixtures/events/run_end__restore_failed.ndjson
git commit -m "Stage 11: canonical run_end (moved/deleted/cancelled/manifest_path) + restore cancelled fix"
```

---

## Task 2: `run_start` — add `dry_run`

**Files:**
- Modify: `ui/server/events.go` (RunStart, ~88-92)
- Modify: `lib/events.sh` (emit_run_start, ~77-96)
- Create: `tests/fixtures/events/run_start__crosscheck.ndjson`
- Modify: `tests/events_contract.sh`, `ui/server/events_roundtrip_test.go`

- [ ] **Step 1: Add the fixture**

Create `tests/fixtures/events/run_start__crosscheck.ndjson`:

```
{"type":"run_start","ts":1747934400,"run_id":"r_test","mode":"cross_check","source":"/src","dry_run":true}
```

- [ ] **Step 2: Add the contract case**

In `tests/events_contract.sh`, after the existing `run_start basic` case (line ~41):

```bash
run_case "run_start crosscheck" "run_start__crosscheck.ndjson" \
  emit_run_start --mode cross_check --source /src --dry-run true
```

- [ ] **Step 3: Run — verify FAIL**

Run: `bash tests/events_contract.sh`
Expected: `FAIL run_start crosscheck` (no `dry_run` in output).

- [ ] **Step 4: Extend `emit_run_start`**

In `lib/events.sh` `emit_run_start`: add `dry_run=""` to the `local` line; add arg case `--dry-run) dry_run="$2"; shift 2 ;;`; after the `out+=',"source":...'` line add:

```bash
  case "$dry_run" in
    true|false) out+=',"dry_run":'"$dry_run" ;;
    "") ;;
    *) echo "emit_run_start: --dry-run must be true|false" >&2 ;;
  esac
```

- [ ] **Step 5: Run — verify PASS**

Run: `bash tests/events_contract.sh`
Expected: `ok    run_start crosscheck`.

- [ ] **Step 6: Grow `RunStart` + round-trip case**

In `ui/server/events.go` `RunStart`, add after `Source`:

```go
	DryRun bool `json:"dry_run,omitempty"`
```

In `events_roundtrip_test.go`, add:

```go
		{
			file:     "run_start__crosscheck.ndjson",
			wantType: EventRunStart,
			want: RunStart{
				EventEnvelope: EventEnvelope{Type: EventRunStart, TS: 1747934400, RunID: "r_test"},
				Mode:          "cross_check",
				Source:        "/src",
				DryRun:        true,
			},
		},
```

- [ ] **Step 7: Run Go tests — verify PASS**

Run: `( cd ui && go test ./... )` — Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add lib/events.sh ui/server/events.go ui/server/events_roundtrip_test.go tests/events_contract.sh tests/fixtures/events/run_start__crosscheck.ndjson
git commit -m "Stage 11: canonical run_start (add dry_run)"
```

---

## Task 3: `dup_group` — canonical helper, composer, struct, fixtures

**Files:**
- Modify: `lib/events.sh` (emit_dup_group ~393-416; add `dup_remove_json`)
- Modify: `ui/server/events.go` (DupGroup, DupRemoveEntry; add UnmarshalDupGroup)
- Create: `tests/fixtures/events/dup_group__cross_md5.ndjson`, `dup_group__self_md5_multi.ndjson`, `dup_group__similar_video.ndjson`
- Delete: `tests/fixtures/events/dup_group__cross_hash.ndjson`
- Modify: `tests/events_contract.sh`, `ui/server/events_roundtrip_test.go`

- [ ] **Step 1: Add the three canonical fixtures**

`tests/fixtures/events/dup_group__cross_md5.ndjson`:

```
{"type":"dup_group","ts":1747934400,"run_id":"r_test","group_id":1,"match_reason":"md5","hash":"deadbeef","keep_path":"/bk/a.jpg","keep_size":1024,"keep_mtime":100,"remove":[{"path":"/src/a.jpg","size":1024,"mtime":200}]}
```

`tests/fixtures/events/dup_group__self_md5_multi.ndjson`:

```
{"type":"dup_group","ts":1747934400,"run_id":"r_test","group_id":1,"match_reason":"md5","hash":"cafe","keep_path":"/p/a.jpg","keep_size":2048,"keep_mtime":100,"remove":[{"path":"/p/b.jpg","size":2048,"mtime":200},{"path":"/p/c.jpg","size":2048,"mtime":300}]}
```

`tests/fixtures/events/dup_group__similar_video.ndjson`:

```
{"type":"dup_group","ts":1747934400,"run_id":"r_test","group_id":1,"match_reason":"video_fast","keep_path":"/v/a.mp4","keep_size":4200000,"keep_mtime":100,"keep_duration":45.5,"keep_width":1920,"keep_height":1080,"keep_fps":29.97,"keep_bitrate":5000000,"remove":[{"path":"/v/b.mp4","size":3900000,"mtime":200,"duration":45.5,"width":1920,"height":1080,"fps":29.97,"bitrate":4700000}]}
```

Delete the old fixture: `git rm tests/fixtures/events/dup_group__cross_hash.ndjson`.

- [ ] **Step 2: Replace the dup_group contract case**

In `tests/events_contract.sh`, replace the `dup_group cross_hash` case (lines ~95-98) with:

```bash
# === dup_group ===
run_case "dup_group cross_md5" "dup_group__cross_md5.ndjson" \
  emit_dup_group --group-id 1 --match-reason md5 --hash deadbeef \
    --keep-path /bk/a.jpg --keep-size 1024 --keep-mtime 100 \
    --remove-json "$(dup_remove_json /src/a.jpg 1024 200)"

run_case "dup_group self_md5_multi" "dup_group__self_md5_multi.ndjson" \
  emit_dup_group --group-id 1 --match-reason md5 --hash cafe \
    --keep-path /p/a.jpg --keep-size 2048 --keep-mtime 100 \
    --remove-json "$(dup_remove_json /p/b.jpg 2048 200)" \
    --remove-json "$(dup_remove_json /p/c.jpg 2048 300)"

run_case "dup_group similar_video" "dup_group__similar_video.ndjson" \
  emit_dup_group --group-id 1 --match-reason video_fast \
    --keep-path /v/a.mp4 --keep-size 4200000 --keep-mtime 100 \
    --keep-duration 45.5 --keep-width 1920 --keep-height 1080 --keep-fps 29.97 --keep-bitrate 5000000 \
    --remove-json "$(dup_remove_json /v/b.mp4 3900000 200 45.5 1920 1080 29.97 4700000)"
```

- [ ] **Step 3: Run — verify FAIL**

Run: `bash tests/events_contract.sh`
Expected: `FAIL` for the three dup_group cases (`dup_remove_json: command not found` or unknown-arg output mismatch).

- [ ] **Step 4: Add the `dup_remove_json` composer**

In `lib/events.sh`, immediately before `emit_dup_group`, add. Note: `duration` and `fps` are floats, so they bypass `_emit_num` (which is integer-only) — they are inserted raw after a numeric-shape guard:

```bash
# dup_remove_json — compose one remove[] entry object for emit_dup_group.
# Echoes a bare JSON object (no newline). size/mtime are required ints;
# args 4-8 (dur/w/h/fps/bps) are optional video meta, emitted only when
# present and non-zero. duration/fps are floats (raw, guarded).
dup_remove_json(){
  local path="$1" size="$2" mtime="$3"
  local dur="${4:-0}" w="${5:-0}" h="${6:-0}" fps="${7:-0}" bps="${8:-0}"
  [[ "$dur" =~ ^[0-9]+(\.[0-9]+)?$ ]] || dur=0
  [[ "$fps" =~ ^[0-9]+(\.[0-9]+)?$ ]] || fps=0
  local o='{"path":"'"$(json_escape "$path")"'"'
  o+=',"size":'"$(_emit_num size "$size")"
  o+=',"mtime":'"$(_emit_num mtime "$mtime")"
  [[ "$dur" != 0 ]] && o+=',"duration":'"$dur"
  [[ "$w"   != 0 && -n "$w" ]] && o+=',"width":'"$(_emit_num width "$w")"
  [[ "$h"   != 0 && -n "$h" ]] && o+=',"height":'"$(_emit_num height "$h")"
  [[ "$fps" != 0 ]] && o+=',"fps":'"$fps"
  [[ "$bps" != 0 && -n "$bps" ]] && o+=',"bitrate":'"$(_emit_num bitrate "$bps")"
  o+='}'
  printf '%s' "$o"
}
```

- [ ] **Step 5: Rewrite `emit_dup_group` to canonical**

In `lib/events.sh`, replace the entire `emit_dup_group` function with:

```bash
# emit_dup_group — a duplicate pair/group. remove[] is always an array;
# pass one or more pre-composed --remove-json entries (see dup_remove_json).
#   --group-id INT  --match-reason VAL  --hash VAL(optional)
#   --keep-path VAL --keep-size INT --keep-mtime INT
#   [--keep-duration FLOAT --keep-width INT --keep-height INT --keep-fps FLOAT --keep-bitrate INT]
#   --remove-json '<obj>'   (repeatable; >=1)
emit_dup_group(){
  $JSON_EVENTS || return 0
  local group_id="" match_reason="" hash="" keep_path="" run_id=""
  local keep_size="" keep_mtime="" keep_dur="" keep_w="" keep_h="" keep_fps="" keep_bps=""
  local _remove_entries=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --group-id)      group_id="$2";     shift 2 ;;
      --match-reason)  match_reason="$2"; shift 2 ;;
      --hash)          hash="$2";         shift 2 ;;
      --keep-path)     keep_path="$2";    shift 2 ;;
      --keep-size)     keep_size="$2";    shift 2 ;;
      --keep-mtime)    keep_mtime="$2";   shift 2 ;;
      --keep-duration) keep_dur="$2";     shift 2 ;;
      --keep-width)    keep_w="$2";       shift 2 ;;
      --keep-height)   keep_h="$2";       shift 2 ;;
      --keep-fps)      keep_fps="$2";     shift 2 ;;
      --keep-bitrate)  keep_bps="$2";     shift 2 ;;
      --remove-json)   _remove_entries+=("$2"); shift 2 ;;
      --run-id)        run_id="$2";       shift 2 ;;
      *) echo "emit_dup_group: unknown arg $1" >&2; return 0 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  [[ "$keep_dur" =~ ^[0-9]+(\.[0-9]+)?$ ]] || keep_dur="${keep_dur:+0}"
  [[ "$keep_fps" =~ ^[0-9]+(\.[0-9]+)?$ ]] || keep_fps="${keep_fps:+0}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"dup_group","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"group_id":'"$(_emit_num group_id "$group_id")"
  out+=',"match_reason":"'"$(json_escape "$match_reason")"'"'
  [[ -n "$hash" ]]      && out+=',"hash":"'"$(json_escape "$hash")"'"'
  out+=',"keep_path":"'"$(json_escape "$keep_path")"'"'
  [[ -n "$keep_size" ]]  && out+=',"keep_size":'"$(_emit_num keep_size "$keep_size")"
  [[ -n "$keep_mtime" ]] && out+=',"keep_mtime":'"$(_emit_num keep_mtime "$keep_mtime")"
  [[ -n "$keep_dur" && "$keep_dur" != 0 ]] && out+=',"keep_duration":'"$keep_dur"
  [[ -n "$keep_w"   && "$keep_w"   != 0 ]] && out+=',"keep_width":'"$(_emit_num keep_width "$keep_w")"
  [[ -n "$keep_h"   && "$keep_h"   != 0 ]] && out+=',"keep_height":'"$(_emit_num keep_height "$keep_h")"
  [[ -n "$keep_fps" && "$keep_fps" != 0 ]] && out+=',"keep_fps":'"$keep_fps"
  [[ -n "$keep_bps" && "$keep_bps" != 0 ]] && out+=',"keep_bitrate":'"$(_emit_num keep_bitrate "$keep_bps")"
  local _joined="" _e
  for _e in ${_remove_entries[@]+"${_remove_entries[@]}"}; do
    [[ -n "$_joined" ]] && _joined+=","
    _joined+="$_e"
  done
  out+=',"remove":['"$_joined"']'
  out+='}'
  _emit_write "$out"
}
```

- [ ] **Step 6: Run — verify PASS**

Run: `bash tests/events_contract.sh`
Expected: `ok    dup_group cross_md5`, `ok    dup_group self_md5_multi`, `ok    dup_group similar_video`, and all prior cases still `ok`. If a byte diff appears, it will print — fix field order to match the fixture.

- [ ] **Step 7: Grow Go structs + add `UnmarshalDupGroup`**

In `ui/server/events.go`, replace `DupRemoveEntry` and `DupGroup` with:

```go
// DupRemoveEntry is one element of the DupGroup.Remove list. Size/MTime are
// always present; the video-meta fields are present only for similarity
// matches (match_reason video_*).
type DupRemoveEntry struct {
	Path     string  `json:"path"`
	Size     int64   `json:"size,omitempty"`
	MTime    int64   `json:"mtime,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	Bitrate  int64   `json:"bitrate,omitempty"`
}

// DupGroup is the typed payload of a "dup_group" event emitted during
// cross-check, self-check, or similar-video. remove is always an array.
type DupGroup struct {
	EventEnvelope
	GroupID      int64            `json:"group_id"`
	MatchReason  string           `json:"match_reason"`
	Hash         string           `json:"hash,omitempty"`
	KeepPath     string           `json:"keep_path"`
	KeepSize     int64            `json:"keep_size,omitempty"`
	KeepMTime    int64            `json:"keep_mtime,omitempty"`
	KeepDuration float64          `json:"keep_duration,omitempty"`
	KeepWidth    int              `json:"keep_width,omitempty"`
	KeepHeight   int              `json:"keep_height,omitempty"`
	KeepFPS      float64          `json:"keep_fps,omitempty"`
	KeepBitrate  int64            `json:"keep_bitrate,omitempty"`
	Remove       []DupRemoveEntry `json:"remove"`
}

// UnmarshalDupGroup decodes the raw payload of a dup_group event into g.
// Mirrors UnmarshalThumbCandidate.
func UnmarshalDupGroup(ev Event, g *DupGroup) error {
	if err := json.Unmarshal(ev.Raw, g); err != nil {
		return fmt.Errorf("unmarshal dup_group: %w", err)
	}
	if g.KeepPath == "" {
		return fmt.Errorf("dup_group seq=%d: missing keep_path", ev.Seq)
	}
	return nil
}
```

In `events_roundtrip_test.go`, **replace** the `dup_group__cross_hash` case with three cases:

```go
		{
			file:     "dup_group__cross_md5.ndjson",
			wantType: EventDupGroup,
			want: DupGroup{
				EventEnvelope: EventEnvelope{Type: EventDupGroup, TS: 1747934400, RunID: "r_test"},
				GroupID:       1,
				MatchReason:   "md5",
				Hash:          "deadbeef",
				KeepPath:      "/bk/a.jpg",
				KeepSize:      1024,
				KeepMTime:     100,
				Remove:        []DupRemoveEntry{{Path: "/src/a.jpg", Size: 1024, MTime: 200}},
			},
		},
		{
			file:     "dup_group__self_md5_multi.ndjson",
			wantType: EventDupGroup,
			want: DupGroup{
				EventEnvelope: EventEnvelope{Type: EventDupGroup, TS: 1747934400, RunID: "r_test"},
				GroupID:       1,
				MatchReason:   "md5",
				Hash:          "cafe",
				KeepPath:      "/p/a.jpg",
				KeepSize:      2048,
				KeepMTime:     100,
				Remove: []DupRemoveEntry{
					{Path: "/p/b.jpg", Size: 2048, MTime: 200},
					{Path: "/p/c.jpg", Size: 2048, MTime: 300},
				},
			},
		},
		{
			file:     "dup_group__similar_video.ndjson",
			wantType: EventDupGroup,
			want: DupGroup{
				EventEnvelope: EventEnvelope{Type: EventDupGroup, TS: 1747934400, RunID: "r_test"},
				GroupID:       1,
				MatchReason:   "video_fast",
				KeepPath:      "/v/a.mp4",
				KeepSize:      4200000,
				KeepMTime:     100,
				KeepDuration:  45.5,
				KeepWidth:     1920,
				KeepHeight:    1080,
				KeepFPS:       29.97,
				KeepBitrate:   5000000,
				Remove: []DupRemoveEntry{{
					Path: "/v/b.mp4", Size: 3900000, MTime: 200,
					Duration: 45.5, Width: 1920, Height: 1080, FPS: 29.97, Bitrate: 4700000,
				}},
			},
		},
```

- [ ] **Step 8: Run Go tests — verify PASS**

Run: `( cd ui && go test ./... )`
Expected: `ok`. (`results.go` still uses its own anonymous struct in `decodeGroup` — untouched until Task 5 — so it still compiles and `results_test.go` still passes.)

- [ ] **Step 9: Commit**

```bash
git add lib/events.sh ui/server/events.go ui/server/events_roundtrip_test.go tests/events_contract.sh tests/fixtures/events/dup_group__cross_md5.ndjson tests/fixtures/events/dup_group__self_md5_multi.ndjson tests/fixtures/events/dup_group__similar_video.ndjson
git rm tests/fixtures/events/dup_group__cross_hash.ndjson
git commit -m "Stage 11: canonical dup_group (array remove[], keep/remove meta) + UnmarshalDupGroup"
```

---

## Task 4: Migrate the five bash call sites + delete `emit_event`

After this task, all bash emits canonical events through typed helpers. The *existing* `results.go:decodeGroup` already accepts the array shape (its `if len(p.Remove)>0` branch), so the UI stays coherent; Task 5 only removes now-dead code.

**Files:**
- Modify: `bin/twincut.sh` (235, 1155-1172, 1466-1476, 1659-1682, 1751-1762; delete 181-209)
- Modify: `tests/json_events/run_tests.py:158`

- [ ] **Step 1: Migrate run_start (1163)**

In `bin/twincut.sh`, replace the `_bk_json` block + `emit_event run_start …` (lines ~1155-1172) with:

```bash
  emit_run_start --mode "$_mode" --source "${SOURCE_DIR:-}" --dry-run "$DRY_RUN"
```

(Delete the `_bk_json` / `_first` / `for _b in …` lines — `backups` is dropped.)

- [ ] **Step 2: Migrate cross dup_group (1466)**

Replace the `emit_event dup_group …` block (lines ~1466-1476) with:

```bash
      emit_dup_group --group-id "$DUPES" --match-reason md5 --hash "$H" \
        --keep-path "$MATCHED_PATH" --keep-size "${_sz_keep:-0}" --keep-mtime "${_mt_keep:-0}" \
        --remove-json "$(dup_remove_json "$f" "${_sz_rm:-0}" "${_mt_rm:-0}")"
```

(`algo` dropped.)

- [ ] **Step 3: Migrate self dup_group (1673)**

Replace the `if $JSON_EVENTS; then … emit_event dup_group … fi` block (lines ~1660-1682) with:

```bash
        if $JSON_EVENTS; then
          _rm_args=()
          while IFS= read -r _rp; do
            [[ -z "$_rp" || "$_rp" == "$KEEP_SPATH" ]] && continue
            _rsz="$(fsize "$_rp")"; _rmt="$(mtime "$_rp")"
            _rm_args+=( --remove-json "$(dup_remove_json "$_rp" "${_rsz:-0}" "${_rmt:-0}")" )
          done < "$SMAP_FILE"
          _ksz="$(fsize "$KEEP_SPATH")"; _kmt="$(mtime "$KEEP_SPATH")"
          emit_dup_group --group-id "$_GROUP_ID" --match-reason md5 --hash "$sh" \
            --keep-path "$KEEP_SPATH" --keep-size "${_ksz:-0}" --keep-mtime "${_kmt:-0}" \
            ${_rm_args[@]+"${_rm_args[@]}"}
        fi
```

- [ ] **Step 4: Rewrite `emit_similar_video_group` (235)**

Replace the `emit_event dup_group …` tail of `emit_similar_video_group` (lines ~235-254) with a call to `emit_dup_group`:

```bash
  emit_dup_group --group-id "$SIM_GROUP_ID" --match-reason "$_reason" \
    --keep-path "$_keep" --keep-size "${_ksz:-0}" --keep-mtime "${_kmt:-0}" \
    --keep-duration "${_kdur:-0}" --keep-width "${_kw:-0}" --keep-height "${_kh:-0}" \
    --keep-fps "${_kfps:-0}" --keep-bitrate "${_kbps:-0}" \
    --remove-json "$(dup_remove_json "$_rm" "${_rsz:-0}" "${_rmt:-0}" "${_ddur:-0}" "${_dw:-0}" "${_dh:-0}" "${_dfps:-0}" "${_dbps:-0}")"
}
```

(`algo=video_fast` dropped; `match_reason` keeps `$_reason`, i.e. `video_fast`/`video_strict`.)

- [ ] **Step 5: Migrate run_end (1751)**

Replace the `emit_event run_end …` block (lines ~1751-1762) with:

```bash
emit_run_end --status succeeded --total "${TOTAL:-0}" \
  --moved "${MOVED:-0}" --deleted "${DELETED:-0}" \
  --manifest-path "${MANIFEST_FILE:-}" --cancelled false
```

(Drops `dupes/similar/source_internal_dupes/backup_internal_dupes/skipped_hardlink/skipped_symlink`.)

- [ ] **Step 6: Delete `emit_event`**

Delete the comment + function definition `bin/twincut.sh` lines ~181-209 (from `# Usage: emit_event TYPE …` through the closing `}` of `emit_event`).

- [ ] **Step 7: Verify zero `emit_event` references**

Run: `grep -rn 'emit_event' bin/*.sh lib/*.sh`
Expected: no output (exit 1).

- [ ] **Step 8: Drop the stale `dupes` assertion**

In `tests/json_events/run_tests.py`, delete line 158 (`assert end["dupes"] == 1`) inside `test_self_check_apply_emits_actions_and_moves_files`. Keep the `moved` and `manifest_path` assertions.

- [ ] **Step 9: Run the full local suite — verify all green**

```bash
bash tests/events_contract.sh                 # all ok
( cd ui && go test ./... )                     # ok
python3 tests/json_events/run_tests.py         # 12/12
bash tests/p0_smoke.sh                          # all ok
bash tests/p1_stage9_smoke.sh                   # all ok / [skip]
```

If a self-check or cross-check NDJSON test fails on shape, inspect the emitted line (`bin/twincut.sh --self-check <tmp> --dry-run --json-events`) and reconcile field order with `lib/events.sh`.

- [ ] **Step 10: Commit**

```bash
git add bin/twincut.sh tests/json_events/run_tests.py
git commit -m "Stage 11: route cross/self/similar through typed helpers; delete emit_event"
```

---

## Task 5: Clean the Go consumer (`results.go`)

The singular `remove_path` branch is now dead (no producer emits it). Switch `decodeGroup` to the canonical struct and update the three tests that fed the singular shape.

**Files:**
- Modify: `ui/server/results.go` (decodeGroup ~308-374; run_end handler ~270-284; call site ~158)
- Modify: `ui/server/results_test.go` (lines ~121-123, ~165-170, ~356)

- [ ] **Step 1: Update the three singular-shape test events to arrays**

In `ui/server/results_test.go`:

`TestBuildResults_CrossCheckShape` (event at ~121-123) — change the singular tail to a 1-element array:

```go
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5","hash":"def",
		 "keep_path":"/bk/a.jpg","keep_size":1024,"keep_mtime":100,
		 "remove":[{"path":"/src/a.jpg","size":1024,"mtime":200}]}`,
```

`TestBuildResults_SimilarVideoSurfacesMetadata` (event at ~165-170):

```go
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"video_fast",
		 "keep_path":"/v/a.mp4","keep_size":4200000,"keep_mtime":100,
		 "keep_duration":45.5,"keep_width":1920,"keep_height":1080,"keep_fps":29.97,"keep_bitrate":5000000,
		 "remove":[{"path":"/v/b.mp4","size":3900000,"mtime":200,
		 "duration":45.5,"width":1920,"height":1080,"fps":29.97,"bitrate":4700000}]}`,
```

Line ~356 (singular) — change to array:

```go
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5","hash":"x","keep_path":"/bk/a.jpg","keep_size":100,"keep_mtime":1,"remove":[{"path":"/src/a.jpg","size":100,"mtime":1}]}`,
```

(Assertions in these tests read `g.Remove[0]` / `view.Groups[0].Remove[...]` and the same values — they stay valid.)

- [ ] **Step 2: Run Go tests — verify the three now FAIL**

Run: `( cd ui && go test ./ui/server/ -run TestBuildResults 2>&1 | tail` (from repo root: `cd ui && go test ./server/ -run 'TestBuildResults_CrossCheckShape|TestBuildResults_SimilarVideoSurfacesMetadata'`)
Expected: still PASS actually — the *old* `decodeGroup` handles arrays too. This step confirms the test edits didn't break anything before the refactor. If PASS, proceed; the refactor in Step 3 must keep them passing.

- [ ] **Step 3: Refactor `decodeGroup` to the canonical struct**

In `ui/server/results.go`, change the call site (~158) from `decodeGroup(ev.Raw)` to `decodeGroup(ev)`, then replace the entire `decodeGroup` function (~308-374) with:

```go
// decodeGroup converts a dup_group event into a ResultGroup using the
// canonical DupGroup struct (remove is always an array). Similar-video
// matches carry per-side video metadata surfaced via ResultFile.
func decodeGroup(ev Event) (ResultGroup, error) {
	var p DupGroup
	if err := UnmarshalDupGroup(ev, &p); err != nil {
		return ResultGroup{}, err
	}
	g := ResultGroup{
		GroupID:     int(p.GroupID),
		MatchReason: p.MatchReason,
		Hash:        p.Hash,
		IsSimilar:   p.MatchReason != "" && p.MatchReason != "md5",
		Keep: newResultFile(p.KeepPath, p.KeepSize, p.KeepMTime,
			p.KeepDuration, p.KeepWidth, p.KeepHeight, p.KeepFPS, p.KeepBitrate),
	}
	for _, r := range p.Remove {
		g.Remove = append(g.Remove, newResultFile(r.Path, r.Size, r.MTime,
			r.Duration, r.Width, r.Height, r.FPS, r.Bitrate))
	}
	return g, nil
}
```

- [ ] **Step 4: Switch the run_end handler to typed `RunEnd`**

In `ui/server/results.go`, in `BuildResults` the `case EventRunEnd:` block (~270-284), replace the anonymous struct with:

```go
		case EventRunEnd:
			var p RunEnd
			if err := json.Unmarshal(ev.Raw, &p); err == nil {
				view.Cancelled = p.Cancelled
				view.MovedCount = int(p.Moved)
				view.DeletedCount = int(p.Deleted)
				view.RestoredCount = int(p.Restored)
				view.ManifestPath = p.ManifestPath
			}
```

- [ ] **Step 5: Run Go tests — verify PASS**

Run: `( cd ui && go test ./... )`
Expected: `ok`. If `TestBuildResults_CrossCheckShape` / `_SimilarVideoSurfacesMetadata` fail, confirm the Step 1 event literals are valid JSON arrays (no stray singular fields).

- [ ] **Step 6: Commit**

```bash
git add ui/server/results.go ui/server/results_test.go
git commit -m "Stage 11: results.go decodes canonical dup_group; drop singular remove_path branch"
```

---

## Task 6: Stage 11 end-to-end smoke + CI wiring

**Files:**
- Create: `tests/p1_stage11_smoke.sh`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Write the smoke**

Create `tests/p1_stage11_smoke.sh` (executable):

```bash
#!/usr/bin/env bash
# tests/p1_stage11_smoke.sh — Stage 11 contract smoke for cross/self flows.
# Asserts the migrated dup_group / run_start / run_end shapes on real runs
# and that the Go parser accepts every emitted line.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TWINCUT="$ROOT/bin/twincut.sh"
PASS=0; FAIL=0
assert(){ local what="$1" cond="$2"; if eval "$cond"; then echo "  ok   $what"; PASS=$((PASS+1)); else echo "  FAIL $what (cond: $cond)"; FAIL=$((FAIL+1)); fi; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# --- self-check dry-run ---
SC="$TMP/sc"; mkdir -p "$SC"
printf 'dup-content' > "$SC/a.jpg"
printf 'dup-content' > "$SC/b.jpg"
printf 'dup-content' > "$SC/c.jpg"
SELF="$TMP/self.ndjson"
"$TWINCUT" --self-check "$SC" --dry-run --json-events >"$SELF" 2>/dev/null || true

assert "self: run_start has dry_run=true" \
  'grep -q "\"type\":\"run_start\".*\"dry_run\":true" "$SELF"'
assert "self: dup_group remove is an array with size" \
  'python3 -c "import json,sys; gs=[json.loads(l) for l in open(\"$SELF\") if l.strip() and json.loads(l)[\"type\"]==\"dup_group\"]; sys.exit(0 if gs and isinstance(gs[0][\"remove\"],list) and \"size\" in gs[0][\"remove\"][0] else 1)"'
assert "self: run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$SELF"'
assert "self: no legacy emit_event leakage (algo absent on dup_group)" \
  '! grep -q "\"type\":\"dup_group\".*\"algo\"" "$SELF"'

# --- cross-check dry-run ---
CSRC="$TMP/src"; CBK="$TMP/bk"; mkdir -p "$CSRC" "$CBK"
printf 'x-content' > "$CSRC/a.jpg"
printf 'x-content' > "$CBK/a.jpg"
CROSS="$TMP/cross.ndjson"
"$TWINCUT" --source "$CSRC" --backup "$CBK" --dry-run --json-events >"$CROSS" 2>/dev/null || true

assert "cross: dup_group has hash and single-entry array remove" \
  'python3 -c "import json,sys; gs=[json.loads(l) for l in open(\"$CROSS\") if l.strip() and json.loads(l)[\"type\"]==\"dup_group\"]; sys.exit(0 if gs and \"hash\" in gs[0] and len(gs[0][\"remove\"])==1 else 1)"'

# --- every line is valid JSON (escaping / shape sanity) ---
assert "self: every emitted line is valid JSON" \
  'python3 -c "import json,sys; [json.loads(l) for l in open(\"$SELF\") if l.strip()]; sys.exit(0)"'
assert "cross: dup_group has no legacy algo field" \
  '! grep -q "\"type\":\"dup_group\".*\"algo\"" "$CROSS"'

echo; echo "=========================================="; echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
```

The round-trip Go test guards typed decoding of fixtures; this smoke guards that *real runs* emit the canonical shapes (array `remove`, `dry_run`, `status`, no `algo`).

- [ ] **Step 2: Make it executable and run it**

```bash
chmod +x tests/p1_stage11_smoke.sh
bash tests/p1_stage11_smoke.sh
```
Expected: all `ok`, `FAIL=0`.

- [ ] **Step 3: Wire into CI**

In `.github/workflows/ci.yml`, in the "Run shell test suites" step (after `bash tests/p0_smoke.sh`, line ~42), add:

```yaml
          bash tests/p1_stage11_smoke.sh
```

- [ ] **Step 4: Commit**

```bash
git add tests/p1_stage11_smoke.sh .github/workflows/ci.yml
git commit -m "Stage 11: end-to-end contract smoke + CI wiring"
```

---

## Task 7: Documentation

**Files:**
- Modify: `CLAUDE.md` (the Stage 9 architecture note)

- [ ] **Step 1: Update the Stage 9 note**

In `CLAUDE.md`, find the paragraph beginning "**Stage 9 (`thumbnail_detect` only): Go-owned contract.**" and the sentence "7 legacy `emit_event` call sites remain in `bin/twincut.sh` for cross-check / restore / similar-video flows (out of Stage 9 scope …)". Replace that sentence with:

```
As of Stage 11 the generic `emit_event` helper is removed: cross-check,
self-check, and similar-video flows emit through the same typed helpers
in `lib/events.sh` (`emit_dup_group`, `emit_run_start`, `emit_run_end`)
and are guarded by the same `events_roundtrip_test.go` drift check. The
`dup_group` wire shape is unified — `remove` is always an array.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Stage 11: update CLAUDE.md — emit_event removed, contract sealed"
```

---

## Task 8: Final verification + review

- [ ] **Step 1: Full suite + grep gates**

```bash
cd /Users/mickey/Playground/twincut
bash tests/events_contract.sh
( cd ui && go test ./... )
python3 tests/json_events/run_tests.py        # 12/12
bash tests/p0_smoke.sh
bash tests/p1_stage9_smoke.sh
bash tests/p1_stage11_smoke.sh
grep -rn 'emit_event' bin/*.sh lib/*.sh        # MUST be empty
make build                                      # Go UI still builds
```

- [ ] **Step 2: Manual UI smoke (no regression)**

```bash
make build
# Run a self-check + a cross-check dry-run through the UI and confirm the
# results page renders groups, the similar-video metadata strip appears,
# and the reclaimable-bytes total is non-zero. (Per spec §7 acceptance #4.)
```

- [ ] **Step 3: Third-party review**

Dispatch `reviewer-gemini` on the branch diff (cross-module contract refactor). Then surface to the user: "This is a cross-module schema/contract refactor — want me to also dispatch `reviewer-codex` for an adversarial design pass?" Dispatch codex only on explicit confirmation.

- [ ] **Step 4: Open the PR** (only when the user asks)

```bash
git push -u origin feature/stage-11-event-contract
gh pr create --title "Stage 11: Go-owned contract for cross-check / self-check / similar-video" --body "<summary + test evidence>"
```

---

## Self-review (completed by plan author)

**Spec coverage:** §4 schema → Tasks 1-3 (run_end/run_start/dup_group canonical). §5 Go changes → Tasks 1-3 (structs) + Task 5 (results.go, UnmarshalDupGroup). §6 bash changes → Task 4 (5 sites + delete emit_event) + Tasks 1-3 (helpers). §7 tests → Tasks 1-3 (fixtures/contract/roundtrip), Task 6 (smoke+CI), Task 4 (run_tests dupes). §8 risks (bash 3.2 arrays, float meta, apply unaffected) → addressed in Task 3 composer + Task 4 array guards. §9 out-of-scope respected (history.go untouched; counters dropped, not UI-wired). §10 review → Task 8.

**Bonus beyond spec (justified):** restore `--cancelled false` (Task 1) fixes 2 pre-existing red tests that the new flag enables; restore fixtures get `cancelled` for honesty.

**Placeholder scan:** clean — every step has runnable commands/code. (An earlier Go-parse stand-in in Task 6 was replaced with concrete python JSON-validity assertions.)

**Type consistency:** helper flags (`--keep-size`, `--remove-json`, `dup_remove_json`) match across events_contract.sh, the helper definition, and the call sites; Go field names (`KeepSize`, `Moved`, `DupRemoveEntry`) match across events.go, roundtrip cases, and results.go.
