# Stage 9 — Go-owned contract for thumbnail_detect — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bash↔Go hybrid contract for `thumbnail_detect` with a single typed contract: Go structs are the schema authority; bash side gets one helper function per event type; apply input flows via stdin JSON-lines (no more `.thumb-confirm.tsv` round-trip).

**Architecture:** Bash retreats to a leaf primitive that emits typed NDJSON via named helpers (`emit_run_start`, `emit_thumb_candidate`, `emit_action_move`, etc.). Apply mode gains `--json-in`, reading JSON-lines `ApplyCommand`s from stdin via `jq`. Go composes the command stream, pipes through stdin, owns the run journal. A round-trip test in Go decodes bash-emitted fixtures with `DisallowUnknownFields`, catching drift in either direction.

**Tech Stack:** bash 3.2 (macOS system bash, no associative arrays), `jq` (new dep, for input parsing only — output stays hand-rolled `printf`+`json_escape`), Go 1.22+, NDJSON line protocol.

**Spec:** `docs/superpowers/specs/2026-05-22-twincut-stage9-go-owned-contract-design.md`

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `lib/events.sh` | Typed bash helpers (one function per event type). Sourced by `bin/twincut.sh` after defaults block, before `lib/thumb.sh`. Internally uses `json_escape` (already in `bin/twincut.sh`) and writes to fd 3 with stdout fallback. |
| `tests/events_contract.sh` | Per-helper unit tests: feed known args, compare stdout byte-for-byte against `tests/fixtures/events/*.ndjson`. |
| `tests/fixtures/events/<event_type>__<case>.ndjson` | Golden NDJSON output for each (helper, case) pair. Checked into git. |
| `ui/server/events_roundtrip_test.go` | Table-driven Go test that reads every fixture, unmarshals through the typed Go struct with `DisallowUnknownFields()`, and asserts `reflect.DeepEqual` against hand-written expected values. |
| `tests/p1_stage9_smoke.sh` | End-to-end smoke for the new scan→stdin-apply pipeline (separate from `p1_thumb_phash_smoke.sh`). |

**Changed files:**

| Path | Change |
|---|---|
| `bin/twincut.sh` | Source `lib/events.sh` at startup (T1). Add `--json-in` flag (T6). Migrate emit_event call sites (T5). Delete `--thumb-confirm <file>` handling (T9). Delete `emit_event` function (T9). |
| `lib/thumb.sh` | Migrate emit_event call sites (T5). Delete `thumb_confirm_review` (T9). |
| `ui/server/events.go` | Add `ApplyCommand` input struct (T7). Tighten existing event payload structs where round-trip surfaces gaps (T1+). |
| `ui/server/apply_list.go` | Add `composeApplyCommands` (T7). Delete `composeThumbnailConfirmTSV` (T9). |
| `ui/server/apply_list_test.go` | New tests for `composeApplyCommands` (T7); delete TSV-compose tests (T9). |
| `ui/server/thumbnail.go` | `handleThumbnailsApply` switches to stdin pipe (T8). |
| `ui/server/thumbnail_test.go` | Update apply-spawn argv assertions (T8). |
| `ui/server/runs.go` | `StartOptions` gains `Stdin io.Reader` (T8). |
| `ui/server/events_test.go` | Add `ApplyCommand` Marshal/Unmarshal tests (T7). |
| `CLAUDE.md` | Add Stage 9 paragraph, add `jq` to runtime deps (T6, T10). |
| `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md` | Mark items #1/#2/#3/#6 closed; §7 strategic question resolved (T10). |

---

## Conventions

**Helper signature.** Every `emit_*` helper accepts `--key value` long-options. Unknown args are fatal: print `helper: unknown arg <flag>` to stderr and `return 2`. Positional args are not accepted. This forces explicit, self-documenting call sites.

**Test seams.** Every helper consults two env vars for fixture stability:
- `TWINCUT_TEST_TS` — if set, used verbatim as the `ts` value; else `date -u +%s`.
- `RUN_ID` — already-existing env var; helpers use it for `run_id`. Tests set `RUN_ID=r_test`.

**Output sink.** Helpers reuse the existing fd-3-with-stdout-fallback pattern from `emit_event` (a small `_emit_write` shared function in `lib/events.sh`).

**JSON guard.** Every string field passes through `json_escape` (already in `bin/twincut.sh:176`). Every numeric field is validated with a regex (`^-?[0-9]+$`) before interpolation as a raw JSON number; non-matching values default to `0` with a warning to stderr.

**Allowed sets.** `decision` values are validated against an allowed set inside `emit_thumb_candidate` (`thumb_l1_review|thumb_l2_exif|thumb_l3_embed|thumb_confirmed|keep_user_override`). Unknown → stderr warning + emit anyway (we want to surface drift without crashing scans).

---

### Task 1: Schema skeleton + first helper (emit_run_start) + Go round-trip infrastructure

**Files:**
- Create: `lib/events.sh`
- Create: `tests/events_contract.sh`
- Create: `tests/fixtures/events/run_start__basic.ndjson`
- Create: `ui/server/events_roundtrip_test.go`
- Modify: `bin/twincut.sh` (source `lib/events.sh`)

This task establishes the entire pipeline end-to-end with one helper. Subsequent tasks scale out.

- [ ] **Step 1: Create `lib/events.sh` skeleton (helpers stubbed, internals working)**

```bash
#!/usr/bin/env bash
# lib/events.sh — typed NDJSON event emitters for twincut.
#
# Schema authority: ui/server/events.go Go structs. Every helper here has
# matching field slots; a round-trip test in events_roundtrip_test.go
# decodes the fixtures generated by tests/events_contract.sh and fails on
# drift.
#
# Convention: helpers accept --key value long-options only. Unknown args
# are fatal (return 2). Test seams:
#   TWINCUT_TEST_TS  — override the ts field
#   RUN_ID           — provides run_id (existing global)

# Resolve a timestamp; honor TWINCUT_TEST_TS for fixture-stable output.
_emit_now_ts(){
  if [[ -n "${TWINCUT_TEST_TS:-}" ]]; then
    printf '%s' "$TWINCUT_TEST_TS"
  else
    date -u +%s
  fi
}

# Write a single NDJSON line: fd 3 if open, else stdout.
_emit_write(){
  if { true >&3; } 2>/dev/null; then
    printf '%s\n' "$1" >&3
  else
    printf '%s\n' "$1"
  fi
}

# Validate a numeric field; echo it on stdout if valid, else 0 + warning.
_emit_num(){
  local name="$1" v="$2"
  if [[ "$v" =~ ^-?[0-9]+$ ]]; then
    printf '%s' "$v"
  else
    echo "emit: field $name expected int, got '$v' — using 0" >&2
    printf '0'
  fi
}
```

- [ ] **Step 2: Source `lib/events.sh` from `bin/twincut.sh`**

Modify `bin/twincut.sh` near line 88-91 (where `lib/thumb.sh` is sourced):

```bash
# Before:
#   LIB_DIR=...
#   # shellcheck source=../lib/thumb.sh
#   source "$LIB_DIR/thumb.sh"
# After:
LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)"
# shellcheck source=../lib/events.sh
source "$LIB_DIR/events.sh"
# shellcheck source=../lib/thumb.sh
source "$LIB_DIR/thumb.sh"
```

(The actual line where `lib/thumb.sh` is sourced is in `bin/twincut.sh` near line 90. Read the file first to confirm exact context.)

- [ ] **Step 3: Write the failing contract test (run_start)**

Create `tests/events_contract.sh`:

```bash
#!/usr/bin/env bash
# tests/events_contract.sh — per-helper unit tests for lib/events.sh.
#
# Runs each emit_* helper with canned input, compares stdout byte-for-byte
# against tests/fixtures/events/<event_type>__<case>.ndjson.
#
# Fixtures stay stable because helpers honor TWINCUT_TEST_TS and RUN_ID
# env vars.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="$ROOT/tests/fixtures/events"
PASS=0
FAIL=0

# shellcheck source=../bin/twincut.sh
# Source just lib/events.sh + json_escape from bin/twincut.sh.
# We need json_escape but not the whole CLI parser, so extract:
source "$ROOT/lib/events.sh"
# json_escape is defined in bin/twincut.sh; pull it in by sourcing the file
# but stopping before main() runs. The script guards with "if main" semantics —
# for now, just `source` it; CLI defaults set but nothing runs.
# (NOTE: bin/twincut.sh executes parsing at top-level today. For the contract
# test we instead inline json_escape via a small helper or refactor. See
# Task 1 Step 4 below for the chosen path.)

JSON_EVENTS=true    # required: helpers gate on $JSON_EVENTS
RUN_ID="r_test"
export TWINCUT_TEST_TS=1747934400

run_case(){
  local name="$1" fixture="$2"
  shift 2
  local actual
  actual="$("$@")"
  if diff -u "$FIXTURE_DIR/$fixture" <(printf '%s\n' "$actual") >/dev/null; then
    echo "  ok    $name"
    PASS=$((PASS+1))
  else
    echo "  FAIL  $name"
    diff -u "$FIXTURE_DIR/$fixture" <(printf '%s\n' "$actual") >&2 || true
    FAIL=$((FAIL+1))
  fi
}

# === run_start ===
run_case "run_start basic" "run_start__basic.ndjson" \
  emit_run_start --mode thumbnail_detect_preview --source /img

echo
echo "=========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
```

Make executable:
```bash
chmod +x tests/events_contract.sh
```

- [ ] **Step 4: Refactor `json_escape` to be source-able standalone**

The contract test needs `json_escape` without running `bin/twincut.sh`'s top-level parser. Cleanest fix: move `json_escape` into `lib/events.sh` and remove it from `bin/twincut.sh`.

Cut `json_escape()` from `bin/twincut.sh:176-185`. Paste at the top of `lib/events.sh` after the shebang/comments block, before `_emit_now_ts`:

```bash
# JSON string escaper. Handles backslash, quote, control chars, newline, tab,
# carriage return. Output is bare (no surrounding quotes) so callers can
# compose object literals.
json_escape(){
  local s="${1-}"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/\\r}"
  s="${s//$'\t'/\\t}"
  s="${s//$'\b'/\\b}"
  s="${s//$'\f'/\\f}"
  printf '%s' "$s"
}
```

Then update the contract test to drop the bin/twincut.sh source — `lib/events.sh` is now self-contained.

- [ ] **Step 5: Run the contract test, verify it fails**

```bash
bash tests/events_contract.sh
```

Expected: FAIL "run_start basic" because `emit_run_start` is not defined.

- [ ] **Step 6: Implement `emit_run_start` in `lib/events.sh`**

Append to `lib/events.sh`:

```bash
# emit_run_start — start-of-run event.
#   --mode VAL      mode string (e.g. thumbnail_detect_preview, thumbnail_detect_apply)
#   --source VAL    source directory absolute path
#   --run-id VAL    optional explicit run_id (overrides $RUN_ID)
emit_run_start(){
  $JSON_EVENTS || return 0
  local mode="" source="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --mode)    mode="$2";    shift 2 ;;
      --source)  source="$2";  shift 2 ;;
      --run-id)  run_id="$2";  shift 2 ;;
      *) echo "emit_run_start: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"run_start","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"mode":"'"$(json_escape "$mode")"'"'
  out+=',"source":"'"$(json_escape "$source")"'"'
  out+='}'
  _emit_write "$out"
}
```

- [ ] **Step 7: Create the run_start fixture**

Create `tests/fixtures/events/run_start__basic.ndjson` with exactly this content (single line, trailing newline):

```
{"type":"run_start","ts":1747934400,"run_id":"r_test","mode":"thumbnail_detect_preview","source":"/img"}
```

- [ ] **Step 8: Run the contract test, verify it passes**

```bash
bash tests/events_contract.sh
```

Expected output ends with:
```
PASS=1 FAIL=0
```

- [ ] **Step 9: Write the failing Go round-trip test**

Create `ui/server/events_roundtrip_test.go`:

```go
package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureCase pairs a fixture file with its expected typed payload.
// Unknown types or unknown fields fail the test — this is the drift
// catch in either direction (bash adds field Go doesn't have, or vice versa).
type fixtureCase struct {
	file     string
	wantType EventType
	want     interface{} // expected typed payload (RunStart, ThumbCandidate, ...)
}

// fixtures lists every (lib/events.sh helper, case) pair.
// Add an entry whenever a new helper or case is introduced.
func roundtripFixtures() []fixtureCase {
	return []fixtureCase{
		{
			file:     "run_start__basic.ndjson",
			wantType: EventRunStart,
			want: RunStart{
				Mode:   "thumbnail_detect_preview",
				Source: "/img",
			},
		},
	}
}

func TestEventsRoundtrip(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	dir := filepath.Join(root, "tests", "fixtures", "events")

	for _, c := range roundtripFixtures() {
		c := c
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join(dir, c.file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Bytes()
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				env, payload, err := strictDecodeEvent(line, c.want)
				if err != nil {
					t.Fatalf("line %d: decode: %v", lineNum, err)
				}
				if env.Type != c.wantType {
					t.Fatalf("line %d: type=%q want=%q", lineNum, env.Type, c.wantType)
				}
				if !reflect.DeepEqual(payload, c.want) {
					t.Fatalf("line %d: payload mismatch:\n got = %+v\nwant = %+v", lineNum, payload, c.want)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan: %v", err)
			}
		})
	}
}

// repoRoot walks up from the test binary's working directory until it finds
// a go.mod (the ui/server module). Returns the parent of ui/server, i.e.
// /Users/.../twincut.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// ui/server -> twincut
	return filepath.Clean(filepath.Join(dir, "..", "..")), nil
}

// strictDecodeEvent decodes a single NDJSON line and returns the envelope
// plus a typed payload of the same dynamic type as wantPrototype, with
// DisallowUnknownFields enabled (so bash emitting an unmodeled field is fatal).
func strictDecodeEvent(line []byte, wantPrototype interface{}) (EventEnvelope, interface{}, error) {
	var env EventEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return env, nil, err
	}
	// Re-decode into the typed payload type with strict field policy.
	payloadType := reflect.TypeOf(wantPrototype)
	payloadPtr := reflect.New(payloadType).Interface()
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(payloadPtr); err != nil {
		return env, nil, err
	}
	// Drop the envelope-level fields we don't compare (type, ts, run_id) by
	// returning a dereferenced copy of the payload zeroed for those fields,
	// using the comparison strategy: the typed payload structs do NOT include
	// type/ts/run_id (they're only on EventEnvelope), so reflect.DeepEqual on
	// the payload struct already excludes them.
	_ = strings.TrimSpace // (silences linter; keep for future use)
	return env, reflect.ValueOf(payloadPtr).Elem().Interface(), nil
}
```

This test assumes a `RunStart` struct in `events.go` with the shape `{Mode, Source string}`. If `events.go` does not currently model a typed payload for `run_start` (today it might just be the envelope), add one in the next step.

- [ ] **Step 10: Add `RunStart` typed payload to `ui/server/events.go`**

Read `ui/server/events.go` to find where typed payloads (e.g. `ThumbCandidate`) are declared. Add alongside:

```go
// RunStart is the typed payload of a "run_start" event. Twincut emits exactly
// one per run, before any other event.
type RunStart struct {
	Mode   string `json:"mode"`
	Source string `json:"source"`
}
```

Note: this struct deliberately omits `type`, `ts`, `run_id` — those live on `EventEnvelope`. The round-trip test exploits this: strict-decoding a line that ALSO has the envelope fields would fail with `DisallowUnknownFields` unless the payload struct excludes them. Since the test decodes the same bytes both as `EventEnvelope` (which has type/ts/run_id) and as the typed payload (which doesn't), we need the payload's `DisallowUnknownFields` to tolerate the envelope fields too.

Two options:
1. **Embed `EventEnvelope` in each payload** so the same struct has both envelope + type-specific fields.
2. **Skip strict decoding for envelope fields** in the test — decode envelope first (loose), then strip type/ts/run_id from the bytes and strict-decode the rest.

Option 1 keeps payloads self-describing. Adopt option 1: every typed payload struct embeds `EventEnvelope`.

Update `RunStart`:

```go
type RunStart struct {
	EventEnvelope
	Mode   string `json:"mode"`
	Source string `json:"source"`
}
```

Update the round-trip test's `wantPrototype` to include envelope fields it cares about:

```go
want: RunStart{
    EventEnvelope: EventEnvelope{Type: EventRunStart, TS: 1747934400, RunID: "r_test"},
    Mode:   "thumbnail_detect_preview",
    Source: "/img",
},
```

(Adjust `EventEnvelope` field references to match the actual struct field names in `events.go`.)

- [ ] **Step 11: Run the Go test, verify it passes**

```bash
cd ui/server && go test -run TestEventsRoundtrip ./...
```

Expected: PASS.

- [ ] **Step 12: Run the full existing test suite — main stays green**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
```

Expected: all green. `p1_thumb_phash_smoke.sh`: PASS=26 FAIL=0.

- [ ] **Step 13: Commit**

```bash
git add lib/events.sh tests/events_contract.sh tests/fixtures/events/run_start__basic.ndjson \
        ui/server/events_roundtrip_test.go ui/server/events.go bin/twincut.sh
git commit -m "Stage 9 T1: lib/events.sh skeleton + emit_run_start + Go round-trip infra

- New lib/events.sh: shared JSON output helpers (_emit_now_ts / _emit_write
  / _emit_num) + first typed emitter emit_run_start. json_escape moved
  from bin/twincut.sh into here so contract tests can source standalone.
- New tests/events_contract.sh: byte-exact diff against checked-in fixtures.
- New tests/fixtures/events/run_start__basic.ndjson: golden output.
- New ui/server/events_roundtrip_test.go: table-driven Go decode against
  fixtures with json.Decoder.DisallowUnknownFields (drift catcher).
- ui/server/events.go: add RunStart payload struct (embeds EventEnvelope).
- bin/twincut.sh: source lib/events.sh before lib/thumb.sh."
```

---

### Task 2: Envelope helpers — run_end, warn, error, progress

**Files:**
- Modify: `lib/events.sh` (append four emitters)
- Modify: `tests/events_contract.sh` (append four cases)
- Create: `tests/fixtures/events/run_end__succeeded.ndjson`
- Create: `tests/fixtures/events/warn__io_error.ndjson`
- Create: `tests/fixtures/events/error__usage.ndjson`
- Create: `tests/fixtures/events/progress__scan.ndjson`
- Modify: `ui/server/events.go` (add payload structs if missing)
- Modify: `ui/server/events_roundtrip_test.go` (extend `roundtripFixtures`)

These are simple-shape envelope events (low field counts). Same pattern as T1.

- [ ] **Step 1: Write contract test cases for the four new helpers**

Append to `tests/events_contract.sh` before the final `echo`/`exit`:

```bash
# === run_end ===
run_case "run_end succeeded" "run_end__succeeded.ndjson" \
  emit_run_end --status succeeded --duration-ms 1234 --total 42 --applied 30 --skipped 12

# === warn ===
run_case "warn io_error" "warn__io_error.ndjson" \
  emit_warn --code io_error --path /img/IMG.JPG --detail "mv failed"

# === error ===
run_case "error usage" "error__usage.ndjson" \
  emit_error --code usage_error --detail "missing --source"

# === progress ===
run_case "progress scan" "progress__scan.ndjson" \
  emit_progress --phase scan --done 10 --total 100 --current-path /img/IMG.JPG
```

- [ ] **Step 2: Run the test, verify all four new cases fail**

```bash
bash tests/events_contract.sh
```

Expected: existing PASS=1, plus 4 FAILs (helpers undefined).

- [ ] **Step 3: Implement the four helpers in `lib/events.sh`**

Append:

```bash
# emit_run_end — terminal event for a run.
#   --status VAL         one of: succeeded | failed | interrupted
#   --duration-ms INT    optional total run duration
#   --total INT          optional candidate count (apply: total commands seen)
#   --applied INT        optional apply success count
#   --skipped INT        optional apply skip count
emit_run_end(){
  $JSON_EVENTS || return 0
  local status="" duration_ms="" total="" applied="" skipped="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --status)       status="$2"; shift 2 ;;
      --duration-ms)  duration_ms="$2"; shift 2 ;;
      --total)        total="$2"; shift 2 ;;
      --applied)      applied="$2"; shift 2 ;;
      --skipped)      skipped="$2"; shift 2 ;;
      --run-id)       run_id="$2"; shift 2 ;;
      *) echo "emit_run_end: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"run_end","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"status":"'"$(json_escape "$status")"'"'
  [[ -n "$duration_ms" ]] && out+=',"duration_ms":'"$(_emit_num duration_ms "$duration_ms")"
  [[ -n "$total" ]]       && out+=',"total":'"$(_emit_num total "$total")"
  [[ -n "$applied" ]]     && out+=',"applied":'"$(_emit_num applied "$applied")"
  [[ -n "$skipped" ]]     && out+=',"skipped":'"$(_emit_num skipped "$skipped")"
  out+='}'
  _emit_write "$out"
}

# emit_warn — non-fatal warning event.
#   --code VAL    enum (io_error | missing_file | bad_video | appledouble | ...)
#   --path VAL    optional path the warning is about
#   --detail VAL  free-text human-readable explanation
emit_warn(){
  $JSON_EVENTS || return 0
  local code="" path="" detail="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --code)    code="$2"; shift 2 ;;
      --path)    path="$2"; shift 2 ;;
      --detail)  detail="$2"; shift 2 ;;
      --run-id)  run_id="$2"; shift 2 ;;
      *) echo "emit_warn: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"warn","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"code":"'"$(json_escape "$code")"'"'
  [[ -n "$path" ]]   && out+=',"path":"'"$(json_escape "$path")"'"'
  [[ -n "$detail" ]] && out+=',"detail":"'"$(json_escape "$detail")"'"'
  out+='}'
  _emit_write "$out"
}

# emit_error — fatal-ish error event (run may still continue, but the
# specific operation failed).
#   --code VAL    enum (usage_error | runtime_error | apply_failed | mv_failed | ...)
#   --path VAL    optional path the error is about
#   --detail VAL  free-text
emit_error(){
  $JSON_EVENTS || return 0
  local code="" path="" detail="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --code)    code="$2"; shift 2 ;;
      --path)    path="$2"; shift 2 ;;
      --detail)  detail="$2"; shift 2 ;;
      --run-id)  run_id="$2"; shift 2 ;;
      *) echo "emit_error: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"error","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"code":"'"$(json_escape "$code")"'"'
  [[ -n "$path" ]]   && out+=',"path":"'"$(json_escape "$path")"'"'
  [[ -n "$detail" ]] && out+=',"detail":"'"$(json_escape "$detail")"'"'
  out+='}'
  _emit_write "$out"
}

# emit_progress — progress beacon during a long phase.
#   --phase VAL          enum (scan | hash | phash | apply | restore)
#   --done INT           count of items processed so far
#   --total INT          best-known total
#   --current-path VAL   what's currently being processed
emit_progress(){
  $JSON_EVENTS || return 0
  local phase="" done="" total="" current_path="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --phase)         phase="$2"; shift 2 ;;
      --done)          done="$2"; shift 2 ;;
      --total)         total="$2"; shift 2 ;;
      --current-path)  current_path="$2"; shift 2 ;;
      --run-id)        run_id="$2"; shift 2 ;;
      *) echo "emit_progress: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"progress","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"phase":"'"$(json_escape "$phase")"'"'
  [[ -n "$done" ]]         && out+=',"done":'"$(_emit_num done "$done")"
  [[ -n "$total" ]]        && out+=',"total":'"$(_emit_num total "$total")"
  [[ -n "$current_path" ]] && out+=',"current_path":"'"$(json_escape "$current_path")"'"'
  out+='}'
  _emit_write "$out"
}
```

- [ ] **Step 4: Create the four fixture files**

`tests/fixtures/events/run_end__succeeded.ndjson`:
```
{"type":"run_end","ts":1747934400,"run_id":"r_test","status":"succeeded","duration_ms":1234,"total":42,"applied":30,"skipped":12}
```

`tests/fixtures/events/warn__io_error.ndjson`:
```
{"type":"warn","ts":1747934400,"run_id":"r_test","code":"io_error","path":"/img/IMG.JPG","detail":"mv failed"}
```

`tests/fixtures/events/error__usage.ndjson`:
```
{"type":"error","ts":1747934400,"run_id":"r_test","code":"usage_error","detail":"missing --source"}
```

`tests/fixtures/events/progress__scan.ndjson`:
```
{"type":"progress","ts":1747934400,"run_id":"r_test","phase":"scan","done":10,"total":100,"current_path":"/img/IMG.JPG"}
```

- [ ] **Step 5: Run the contract test, verify PASS=5 FAIL=0**

```bash
bash tests/events_contract.sh
```

- [ ] **Step 6: Add Go typed payload structs if missing**

Read `ui/server/events.go`. For each of `RunEnd`, `Warn`, `Error`, `Progress`, ensure a typed struct exists. If `events.go` already has `RunEnd` / `Progress` (it likely has `Progress` from the original emit_event surface), extend with `EventEnvelope` embedding if not already done. If absent, add:

```go
type RunEnd struct {
	EventEnvelope
	Status      string `json:"status"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	Total       int64  `json:"total,omitempty"`
	Applied     int64  `json:"applied,omitempty"`
	Skipped     int64  `json:"skipped,omitempty"`
}

type Warn struct {
	EventEnvelope
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type ErrorEvent struct { // name avoids collision with Go built-in
	EventEnvelope
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type Progress struct {
	EventEnvelope
	Phase       string `json:"phase"`
	Done        int64  `json:"done,omitempty"`
	Total       int64  `json:"total,omitempty"`
	CurrentPath string `json:"current_path,omitempty"`
}
```

Also add the matching `EventType` constants if missing (`EventRunEnd`, `EventWarn`, `EventError`, `EventProgress`).

- [ ] **Step 7: Extend `roundtripFixtures()` in `events_roundtrip_test.go`**

Add four entries to the slice:

```go
{
    file:     "run_end__succeeded.ndjson",
    wantType: EventRunEnd,
    want: RunEnd{
        EventEnvelope: EventEnvelope{Type: EventRunEnd, TS: 1747934400, RunID: "r_test"},
        Status: "succeeded", DurationMs: 1234, Total: 42, Applied: 30, Skipped: 12,
    },
},
{
    file:     "warn__io_error.ndjson",
    wantType: EventWarn,
    want: Warn{
        EventEnvelope: EventEnvelope{Type: EventWarn, TS: 1747934400, RunID: "r_test"},
        Code: "io_error", Path: "/img/IMG.JPG", Detail: "mv failed",
    },
},
{
    file:     "error__usage.ndjson",
    wantType: EventError,
    want: ErrorEvent{
        EventEnvelope: EventEnvelope{Type: EventError, TS: 1747934400, RunID: "r_test"},
        Code: "usage_error", Detail: "missing --source",
    },
},
{
    file:     "progress__scan.ndjson",
    wantType: EventProgress,
    want: Progress{
        EventEnvelope: EventEnvelope{Type: EventProgress, TS: 1747934400, RunID: "r_test"},
        Phase: "scan", Done: 10, Total: 100, CurrentPath: "/img/IMG.JPG",
    },
},
```

- [ ] **Step 8: Run Go tests, verify PASS**

```bash
cd ui/server && go test -run TestEventsRoundtrip ./...
```

- [ ] **Step 9: Run full suite, main stays green**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
```

Expected: all green. Smoke 26/26, contract 5/5.

- [ ] **Step 10: Commit**

```bash
git add lib/events.sh tests/events_contract.sh tests/fixtures/events/run_end__succeeded.ndjson \
        tests/fixtures/events/warn__io_error.ndjson tests/fixtures/events/error__usage.ndjson \
        tests/fixtures/events/progress__scan.ndjson \
        ui/server/events.go ui/server/events_roundtrip_test.go
git commit -m "Stage 9 T2: envelope helpers — run_end, warn, error, progress

Four typed emitters with matching Go payload structs and fixtures.
Contract test 5/5; round-trip test green; smoke unchanged."
```

---

### Task 3: emit_thumb_candidate (the central typed helper)

**Files:**
- Modify: `lib/events.sh` (append `emit_thumb_candidate`)
- Modify: `tests/events_contract.sh` (append three cases: l2_exif, l3_embed, l1_review)
- Create: `tests/fixtures/events/thumb_candidate__l2_exif.ndjson`
- Create: `tests/fixtures/events/thumb_candidate__l3_embed.ndjson`
- Create: `tests/fixtures/events/thumb_candidate__l1_phash.ndjson`
- Modify: `ui/server/events_roundtrip_test.go` (extend with three cases)
- Modify: `ui/server/events.go` if `ThumbCandidate` needs `EventEnvelope` embedding

This is the highest-leverage helper; it carries the full L1/L2/L3 surface.

- [ ] **Step 1: Read the current `ThumbCandidate` Go struct in `ui/server/events.go`**

Confirm field names and JSON tags (currently includes `Path`, `Keeper`, `GroupID`, `Decision`, `Reason`, `Width`, `Height`, `SizeBytes`, `PhashDistance`). Make a note of the exact JSON tag spelling for each — the helper must match.

- [ ] **Step 2: Write the three failing contract test cases**

Append to `tests/events_contract.sh` before the final summary:

```bash
# === thumb_candidate (L2 exif) ===
run_case "thumb_candidate l2_exif" "thumb_candidate__l2_exif.ndjson" \
  emit_thumb_candidate \
    --decision thumb_l2_exif \
    --path /img/IMG_0010.JPG \
    --keeper /img/IMG_0010.HEIC \
    --group-id 2025-04-01T12:00:00_3024x4032 \
    --width 320 --height 240 --size-bytes 18432

# === thumb_candidate (L3 embedded thumb) ===
run_case "thumb_candidate l3_embed" "thumb_candidate__l3_embed.ndjson" \
  emit_thumb_candidate \
    --decision thumb_l3_embed \
    --path /img/IMG_0011.JPG \
    --keeper /img/IMG_0011.HEIC \
    --group-id l3:abc123 \
    --width 160 --height 120 --size-bytes 9216

# === thumb_candidate (L1 pHash matched) ===
run_case "thumb_candidate l1_phash" "thumb_candidate__l1_phash.ndjson" \
  emit_thumb_candidate \
    --decision thumb_l1_review \
    --path /img/IMG_0012.JPG \
    --keeper /img/IMG_0012.HEIC \
    --group-id l1ph:abcd1234deadbeef \
    --phash-distance 3 \
    --reason l1_phash_match \
    --width 320 --height 240 --size-bytes 18432
```

- [ ] **Step 3: Run the test, verify three new FAILs**

```bash
bash tests/events_contract.sh
```

Expected: FAIL on the three new cases (`emit_thumb_candidate` not defined).

- [ ] **Step 4: Implement `emit_thumb_candidate` in `lib/events.sh`**

Append:

```bash
# emit_thumb_candidate — a thumbnail-detect scan result.
#   --decision VAL     thumb_l1_review | thumb_l2_exif | thumb_l3_embed
#   --path VAL         absolute path to the candidate (suspect)
#   --keeper VAL       optional; absolute path to the keeper that justifies this candidate
#   --group-id VAL     optional; opaque group identifier (L2: fingerprint; L3: l3:<sha>; L1: l1ph:<sha>)
#   --width INT        optional; image pixel width
#   --height INT       optional; image pixel height
#   --size-bytes INT   optional; file size in bytes
#   --phash-distance INT  optional; Hamming distance for L1 pHash matched candidates
#   --reason VAL       optional; sub-classification (e.g. l1_phash_match, l1_only_size)
emit_thumb_candidate(){
  $JSON_EVENTS || return 0
  local decision="" path="" keeper="" group_id=""
  local width="" height="" size_bytes="" phash_distance="" reason="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --decision)        decision="$2"; shift 2 ;;
      --path)            path="$2"; shift 2 ;;
      --keeper)          keeper="$2"; shift 2 ;;
      --group-id)        group_id="$2"; shift 2 ;;
      --width)           width="$2"; shift 2 ;;
      --height)          height="$2"; shift 2 ;;
      --size-bytes)      size_bytes="$2"; shift 2 ;;
      --phash-distance)  phash_distance="$2"; shift 2 ;;
      --reason)          reason="$2"; shift 2 ;;
      --run-id)          run_id="$2"; shift 2 ;;
      *) echo "emit_thumb_candidate: unknown arg $1" >&2; return 2 ;;
    esac
  done
  # Warn-only validation of decision against the allowed set; we still emit.
  case "$decision" in
    thumb_l1_review|thumb_l2_exif|thumb_l3_embed|thumb_confirmed|keep_user_override) ;;
    *) echo "emit_thumb_candidate: unknown decision '$decision'" >&2 ;;
  esac
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"thumb_candidate","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"decision":"'"$(json_escape "$decision")"'"'
  out+=',"path":"'"$(json_escape "$path")"'"'
  [[ -n "$keeper" ]]         && out+=',"keeper":"'"$(json_escape "$keeper")"'"'
  [[ -n "$group_id" ]]       && out+=',"group_id":"'"$(json_escape "$group_id")"'"'
  [[ -n "$width" ]]          && out+=',"width":'"$(_emit_num width "$width")"
  [[ -n "$height" ]]         && out+=',"height":'"$(_emit_num height "$height")"
  [[ -n "$size_bytes" ]]     && out+=',"size_bytes":'"$(_emit_num size_bytes "$size_bytes")"
  [[ -n "$phash_distance" ]] && out+=',"phash_distance":'"$(_emit_num phash_distance "$phash_distance")"
  [[ -n "$reason" ]]         && out+=',"reason":"'"$(json_escape "$reason")"'"'
  out+='}'
  _emit_write "$out"
}
```

- [ ] **Step 5: Create the three fixture files**

`tests/fixtures/events/thumb_candidate__l2_exif.ndjson`:
```
{"type":"thumb_candidate","ts":1747934400,"run_id":"r_test","decision":"thumb_l2_exif","path":"/img/IMG_0010.JPG","keeper":"/img/IMG_0010.HEIC","group_id":"2025-04-01T12:00:00_3024x4032","width":320,"height":240,"size_bytes":18432}
```

`tests/fixtures/events/thumb_candidate__l3_embed.ndjson`:
```
{"type":"thumb_candidate","ts":1747934400,"run_id":"r_test","decision":"thumb_l3_embed","path":"/img/IMG_0011.JPG","keeper":"/img/IMG_0011.HEIC","group_id":"l3:abc123","width":160,"height":120,"size_bytes":9216}
```

`tests/fixtures/events/thumb_candidate__l1_phash.ndjson`:
```
{"type":"thumb_candidate","ts":1747934400,"run_id":"r_test","decision":"thumb_l1_review","path":"/img/IMG_0012.JPG","keeper":"/img/IMG_0012.HEIC","group_id":"l1ph:abcd1234deadbeef","width":320,"height":240,"size_bytes":18432,"phash_distance":3,"reason":"l1_phash_match"}
```

Important: the **field order in JSON output must match the helper's output order** exactly for byte-equal diff. The helper writes fields in this order: ts, run_id, decision, path, keeper, group_id, width, height, size_bytes, phash_distance, reason. Fixture lines must match.

- [ ] **Step 6: Run the contract test, verify PASS=8 FAIL=0**

```bash
bash tests/events_contract.sh
```

- [ ] **Step 7: Update `ui/server/events.go::ThumbCandidate` to embed `EventEnvelope` if not already**

Confirm via read; if it doesn't, change:

```go
type ThumbCandidate struct {
	// existing fields...
}
```

to:

```go
type ThumbCandidate struct {
	EventEnvelope
	// existing fields...
}
```

If `ThumbCandidate` previously stored `type`/`ts`/`run_id` as its own fields, remove the duplicates (they're now in the embedded envelope). Update any code that referenced them to use the embedded path.

- [ ] **Step 8: Add three cases to `roundtripFixtures()` in `events_roundtrip_test.go`**

```go
{
    file:     "thumb_candidate__l2_exif.ndjson",
    wantType: EventThumbCandidate,
    want: ThumbCandidate{
        EventEnvelope: EventEnvelope{Type: EventThumbCandidate, TS: 1747934400, RunID: "r_test"},
        Decision: "thumb_l2_exif",
        Path: "/img/IMG_0010.JPG",
        Keeper: "/img/IMG_0010.HEIC",
        GroupID: "2025-04-01T12:00:00_3024x4032",
        Width: 320, Height: 240, SizeBytes: 18432,
    },
},
{
    file:     "thumb_candidate__l3_embed.ndjson",
    wantType: EventThumbCandidate,
    want: ThumbCandidate{
        EventEnvelope: EventEnvelope{Type: EventThumbCandidate, TS: 1747934400, RunID: "r_test"},
        Decision: "thumb_l3_embed",
        Path: "/img/IMG_0011.JPG",
        Keeper: "/img/IMG_0011.HEIC",
        GroupID: "l3:abc123",
        Width: 160, Height: 120, SizeBytes: 9216,
    },
},
{
    file:     "thumb_candidate__l1_phash.ndjson",
    wantType: EventThumbCandidate,
    want: ThumbCandidate{
        EventEnvelope: EventEnvelope{Type: EventThumbCandidate, TS: 1747934400, RunID: "r_test"},
        Decision: "thumb_l1_review",
        Path: "/img/IMG_0012.JPG",
        Keeper: "/img/IMG_0012.HEIC",
        GroupID: "l1ph:abcd1234deadbeef",
        Width: 320, Height: 240, SizeBytes: 18432,
        PhashDistance: 3,
        Reason: "l1_phash_match",
    },
},
```

(Adjust field names to match the actual `ThumbCandidate` struct in `events.go`.)

- [ ] **Step 9: Run all tests**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
```

Expected: all green. Contract 8/8.

- [ ] **Step 10: Commit**

```bash
git add lib/events.sh tests/events_contract.sh tests/fixtures/events/thumb_candidate__*.ndjson \
        ui/server/events.go ui/server/events_roundtrip_test.go
git commit -m "Stage 9 T3: emit_thumb_candidate + three decision-variant fixtures

ThumbCandidate Go struct now embeds EventEnvelope. Helper covers L1
review (with phash_distance + reason), L2 exif, L3 embedded thumb.
All optional fields elided when empty; field order matches Go struct
declaration order for byte-equal diff."
```

---

### Task 4: action helpers + dup_group

**Files:**
- Modify: `lib/events.sh` (append `emit_action_move`, `emit_action_skip`, `emit_action_delete`, `emit_action_restore`, `emit_dup_group`)
- Modify: `tests/events_contract.sh` (append cases)
- Create: `tests/fixtures/events/action_move__dry.ndjson`
- Create: `tests/fixtures/events/action_skip__hardlink.ndjson`
- Create: `tests/fixtures/events/action_delete__wet.ndjson`
- Create: `tests/fixtures/events/action_restore__ok.ndjson`
- Create: `tests/fixtures/events/dup_group__cross_hash.ndjson`
- Modify: `ui/server/events.go` (`Action`, `DupGroup` typed structs)
- Modify: `ui/server/events_roundtrip_test.go`

`action` events share a base shape; the `kind` discriminator drives field availability. We expose one helper per kind for typed call sites.

- [ ] **Step 1: Write contract test cases**

Append to `tests/events_contract.sh`:

```bash
# === action move (dry run, with matched keeper) ===
run_case "action_move dry" "action_move__dry.ndjson" \
  emit_action_move --src /img/a.jpg --dst /img/_Q/a.jpg \
    --matched /img/a.heic --decision thumb_l2_exif --dry-run true

# === action skip (hardlink) ===
run_case "action_skip hardlink" "action_skip__hardlink.ndjson" \
  emit_action_skip --src /img/a.jpg --matched /img/a.heic \
    --reason hardlink --decision thumb_l2_exif

# === action delete (wet run) ===
run_case "action_delete wet" "action_delete__wet.ndjson" \
  emit_action_delete --src /img/b.jpg --matched /img/b.heic \
    --decision thumb_confirmed --dry-run false

# === action restore ===
run_case "action_restore ok" "action_restore__ok.ndjson" \
  emit_action_restore --variant restore --src /q/a.jpg --dst /img/a.jpg --dry-run false

# === dup_group (cross-hash) ===
run_case "dup_group cross_hash" "dup_group__cross_hash.ndjson" \
  emit_dup_group --group-id 7 --match-reason md5 \
    --keep-path /img/a.jpg --remove-path /img/b.jpg
```

- [ ] **Step 2: Implement the helpers in `lib/events.sh`**

Append:

```bash
# action_move event: a (planned or executed) move into the quarantine.
#   --src VAL          source absolute path
#   --dst VAL          destination absolute path
#   --matched VAL      optional; matched keeper that justifies the move
#   --decision VAL     decision tag (thumb_l2_exif, thumb_confirmed, cross_hash, ...)
#   --dry-run BOOL     true|false (no quotes in the JSON output)
emit_action_move(){
  $JSON_EVENTS || return 0
  local src="" dst="" matched="" decision="" dry_run="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --src)       src="$2"; shift 2 ;;
      --dst)       dst="$2"; shift 2 ;;
      --matched)   matched="$2"; shift 2 ;;
      --decision)  decision="$2"; shift 2 ;;
      --dry-run)   dry_run="$2"; shift 2 ;;
      --run-id)    run_id="$2"; shift 2 ;;
      *) echo "emit_action_move: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"action","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"kind":"move"'
  out+=',"src":"'"$(json_escape "$src")"'"'
  out+=',"dst":"'"$(json_escape "$dst")"'"'
  [[ -n "$matched" ]]  && out+=',"matched":"'"$(json_escape "$matched")"'"'
  [[ -n "$decision" ]] && out+=',"decision":"'"$(json_escape "$decision")"'"'
  case "$dry_run" in
    true|false) out+=',"dry_run":'"$dry_run" ;;
    "") ;;
    *) echo "emit_action_move: --dry-run must be true|false, got '$dry_run'" >&2 ;;
  esac
  out+='}'
  _emit_write "$out"
}

# action_skip event: a candidate that was looked at but not moved.
#   --src VAL          source path
#   --matched VAL      optional; the keeper that triggered the skip (for hardlink)
#   --reason VAL       enum: excluded | hardlink | user_override | ...
#   --decision VAL     decision tag the skip belongs to
emit_action_skip(){
  $JSON_EVENTS || return 0
  local src="" matched="" reason="" decision="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --src)       src="$2"; shift 2 ;;
      --matched)   matched="$2"; shift 2 ;;
      --reason)    reason="$2"; shift 2 ;;
      --decision)  decision="$2"; shift 2 ;;
      --run-id)    run_id="$2"; shift 2 ;;
      *) echo "emit_action_skip: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"action","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"kind":"skip"'
  out+=',"src":"'"$(json_escape "$src")"'"'
  [[ -n "$matched" ]]  && out+=',"matched":"'"$(json_escape "$matched")"'"'
  [[ -n "$reason" ]]   && out+=',"reason":"'"$(json_escape "$reason")"'"'
  [[ -n "$decision" ]] && out+=',"decision":"'"$(json_escape "$decision")"'"'
  out+='}'
  _emit_write "$out"
}

# action_delete event: a delete (not a move into quarantine).
#   --src VAL         source path that was deleted
#   --matched VAL     optional; matched keeper
#   --decision VAL    decision tag
#   --dry-run BOOL    true|false
emit_action_delete(){
  $JSON_EVENTS || return 0
  local src="" matched="" decision="" dry_run="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --src)       src="$2"; shift 2 ;;
      --matched)   matched="$2"; shift 2 ;;
      --decision)  decision="$2"; shift 2 ;;
      --dry-run)   dry_run="$2"; shift 2 ;;
      --run-id)    run_id="$2"; shift 2 ;;
      *) echo "emit_action_delete: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"action","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"kind":"delete"'
  out+=',"src":"'"$(json_escape "$src")"'"'
  [[ -n "$matched" ]]  && out+=',"matched":"'"$(json_escape "$matched")"'"'
  [[ -n "$decision" ]] && out+=',"decision":"'"$(json_escape "$decision")"'"'
  case "$dry_run" in
    true|false) out+=',"dry_run":'"$dry_run" ;;
    "") ;;
    *) echo "emit_action_delete: --dry-run must be true|false" >&2 ;;
  esac
  out+='}'
  _emit_write "$out"
}

# action_restore event: a restore operation (or one of its failure variants).
#   --variant VAL     enum: restore | restore_missing | restore_unrecoverable | restore_conflict
#   --src VAL         source path (in quarantine)
#   --dst VAL         destination path (where it goes back to)
#   --dry-run BOOL    true|false
emit_action_restore(){
  $JSON_EVENTS || return 0
  local variant="" src="" dst="" dry_run="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --variant)   variant="$2"; shift 2 ;;
      --src)       src="$2"; shift 2 ;;
      --dst)       dst="$2"; shift 2 ;;
      --dry-run)   dry_run="$2"; shift 2 ;;
      --run-id)    run_id="$2"; shift 2 ;;
      *) echo "emit_action_restore: unknown arg $1" >&2; return 2 ;;
    esac
  done
  case "$variant" in
    restore|restore_missing|restore_unrecoverable|restore_conflict) ;;
    *) echo "emit_action_restore: bad --variant '$variant'" >&2; return 2 ;;
  esac
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"action","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"kind":"'"$variant"'"'
  out+=',"src":"'"$(json_escape "$src")"'"'
  out+=',"dst":"'"$(json_escape "$dst")"'"'
  case "$dry_run" in
    true|false) out+=',"dry_run":'"$dry_run" ;;
    "") ;;
    *) echo "emit_action_restore: --dry-run must be true|false" >&2 ;;
  esac
  out+='}'
  _emit_write "$out"
}

# dup_group event: a cluster of duplicates with one keeper and N removals.
# Stage 9 keeps the existing emit_event behavior (one event per (keep, remove)
# pair, not per group) — the bash similar-video/cross-check loops emit one
# event per remove path. This helper mirrors that one-per-remove semantics.
#   --group-id INT          numeric group counter
#   --match-reason VAL      enum: md5 | cross_hash | video_fast | video_strict | ...
#   --keep-path VAL         path of the keeper
#   --remove-path VAL       path of one of the dupes-to-remove
emit_dup_group(){
  $JSON_EVENTS || return 0
  local group_id="" match_reason="" keep_path="" remove_path="" run_id=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --group-id)      group_id="$2"; shift 2 ;;
      --match-reason)  match_reason="$2"; shift 2 ;;
      --keep-path)     keep_path="$2"; shift 2 ;;
      --remove-path)   remove_path="$2"; shift 2 ;;
      --run-id)        run_id="$2"; shift 2 ;;
      *) echo "emit_dup_group: unknown arg $1" >&2; return 2 ;;
    esac
  done
  [[ -z "$run_id" ]] && run_id="${RUN_ID:-}"
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"dup_group","ts":'"$ts"
  [[ -n "$run_id" ]] && out+=',"run_id":"'"$(json_escape "$run_id")"'"'
  out+=',"group_id":'"$(_emit_num group_id "$group_id")"
  out+=',"match_reason":"'"$(json_escape "$match_reason")"'"'
  out+=',"keep_path":"'"$(json_escape "$keep_path")"'"'
  out+=',"remove":[{"path":"'"$(json_escape "$remove_path")"'"}]'
  out+='}'
  _emit_write "$out"
}
```

Note: `emit_dup_group` reproduces the existing `dup_group` shape including the `remove` array-of-objects field (matching `events_test.go` example payload).

- [ ] **Step 3: Create the five fixture files**

`tests/fixtures/events/action_move__dry.ndjson`:
```
{"type":"action","ts":1747934400,"run_id":"r_test","kind":"move","src":"/img/a.jpg","dst":"/img/_Q/a.jpg","matched":"/img/a.heic","decision":"thumb_l2_exif","dry_run":true}
```

`tests/fixtures/events/action_skip__hardlink.ndjson`:
```
{"type":"action","ts":1747934400,"run_id":"r_test","kind":"skip","src":"/img/a.jpg","matched":"/img/a.heic","reason":"hardlink","decision":"thumb_l2_exif"}
```

`tests/fixtures/events/action_delete__wet.ndjson`:
```
{"type":"action","ts":1747934400,"run_id":"r_test","kind":"delete","src":"/img/b.jpg","matched":"/img/b.heic","decision":"thumb_confirmed","dry_run":false}
```

`tests/fixtures/events/action_restore__ok.ndjson`:
```
{"type":"action","ts":1747934400,"run_id":"r_test","kind":"restore","src":"/q/a.jpg","dst":"/img/a.jpg","dry_run":false}
```

`tests/fixtures/events/dup_group__cross_hash.ndjson`:
```
{"type":"dup_group","ts":1747934400,"run_id":"r_test","group_id":7,"match_reason":"md5","keep_path":"/img/a.jpg","remove":[{"path":"/img/b.jpg"}]}
```

- [ ] **Step 4: Run the contract test, verify PASS=13 FAIL=0**

```bash
bash tests/events_contract.sh
```

- [ ] **Step 5: Update Go event structs**

Add `Action` and ensure `DupGroup` is present in `events.go`. If `Action` doesn't exist:

```go
type Action struct {
	EventEnvelope
	Kind     string `json:"kind"`              // move | skip | delete | restore | restore_missing | restore_unrecoverable | restore_conflict
	Src      string `json:"src"`
	Dst      string `json:"dst,omitempty"`
	Matched  string `json:"matched,omitempty"`
	Reason   string `json:"reason,omitempty"`  // populated for skip kind
	Decision string `json:"decision,omitempty"`
	DryRun   *bool  `json:"dry_run,omitempty"` // pointer so absent != false
}
```

`*bool` avoids the "absent vs false" ambiguity that bit dimensions in stage8-followup item #6.

If `DupGroup` exists, confirm its shape matches the fixture (it likely does; `events_test.go:16` uses it).

Add `EventAction EventType = "action"` constant if not present.

- [ ] **Step 6: Add five entries to `roundtripFixtures()`**

```go
trueBool := true
falseBool := false
// ...
{
    file:     "action_move__dry.ndjson",
    wantType: EventAction,
    want: Action{
        EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
        Kind: "move", Src: "/img/a.jpg", Dst: "/img/_Q/a.jpg",
        Matched: "/img/a.heic", Decision: "thumb_l2_exif",
        DryRun: &trueBool,
    },
},
{
    file:     "action_skip__hardlink.ndjson",
    wantType: EventAction,
    want: Action{
        EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
        Kind: "skip", Src: "/img/a.jpg", Matched: "/img/a.heic",
        Reason: "hardlink", Decision: "thumb_l2_exif",
    },
},
{
    file:     "action_delete__wet.ndjson",
    wantType: EventAction,
    want: Action{
        EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
        Kind: "delete", Src: "/img/b.jpg", Matched: "/img/b.heic",
        Decision: "thumb_confirmed", DryRun: &falseBool,
    },
},
{
    file:     "action_restore__ok.ndjson",
    wantType: EventAction,
    want: Action{
        EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
        Kind: "restore", Src: "/q/a.jpg", Dst: "/img/a.jpg",
        DryRun: &falseBool,
    },
},
{
    file:     "dup_group__cross_hash.ndjson",
    wantType: EventDupGroup,
    want: DupGroup{ /* fields per existing struct in events.go */ },
},
```

- [ ] **Step 7: Run all tests**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
```

Expected: all green. Contract 13/13.

- [ ] **Step 8: Commit**

```bash
git add lib/events.sh tests/events_contract.sh tests/fixtures/events/action_*.ndjson \
        tests/fixtures/events/dup_group__*.ndjson \
        ui/server/events.go ui/server/events_roundtrip_test.go
git commit -m "Stage 9 T4: action_* helpers + dup_group

Four action emitters (move/skip/delete/restore) covering all current
kind discriminator values. dup_group helper mirrors the one-per-remove
semantics used by similar-video and cross-hash loops. Go Action struct
now uses *bool for dry_run to disambiguate absent vs false."
```

---

### Task 5: Migrate all `emit_event` call sites to typed helpers

**Files:**
- Modify: `lib/thumb.sh` (4 thumb_candidate sites)
- Modify: `bin/twincut.sh` (~30 emit_event call sites across cross-check, self-check, restore, appledouble, bad-video)

After this task, every NDJSON event in twincut is produced by a typed helper. The generic `emit_event` is unused (deletion deferred to T9 to keep this task small).

- [ ] **Step 1: List all `emit_event` call sites**

```bash
grep -nE 'emit_event\s+[a-z]' bin/twincut.sh lib/thumb.sh
```

Expected output: ~30 lines. Record them; each will be rewritten.

- [ ] **Step 2: Migrate `lib/thumb.sh` thumb_candidate sites (4 calls)**

Replace in `lib/thumb.sh`:

```bash
# Before (line ~197, L2 exif):
emit_event "thumb_candidate" "decision=thumb_l2_exif" "path=$p" "keeper=$keep" "group_id=$fp" "width=@${_w:-0}" "height=@${_h:-0}" "size_bytes=@${_sz:-0}"

# After:
emit_thumb_candidate \
  --decision thumb_l2_exif \
  --path "$p" --keeper "$keep" --group-id "$fp" \
  --width "${_w:-0}" --height "${_h:-0}" --size-bytes "${_sz:-0}"
```

```bash
# Before (line ~276, L3 embed):
emit_event "thumb_candidate" "decision=thumb_l3_embed" "path=$f" "keeper=$matched" "group_id=l3:$_gid" "width=@${_w:-0}" "height=@${_h:-0}" "size_bytes=@${_sz:-0}"

# After:
emit_thumb_candidate \
  --decision thumb_l3_embed \
  --path "$f" --keeper "$matched" --group-id "l3:$_gid" \
  --width "${_w:-0}" --height "${_h:-0}" --size-bytes "${_sz:-0}"
```

Lines ~582 + ~593 are inside `thumb_write_review` (the L1 phash-matched and L1-unmatched branches). The current code is:

```bash
# L1 phash matched (~ line 578-585):
emit_event "thumb_candidate" \
  "decision=thumb_l1_review" \
  "path=$f" "keeper=$keeper" "group_id=$group_id" \
  "phash_distance=@$distance" \
  "width=@${w:-0}" "height=@${h:-0}" "size_bytes=@${_sz:-0}" \
  "reason=l1_phash_match"

# L1 unmatched (~ line 591-598):
emit_event "thumb_candidate" \
  "decision=thumb_l1_review" \
  "path=$f" \
  "width=@${w:-0}" "height=@${h:-0}" "size_bytes=@${_sz:-0}" \
  "reason=l1_only_size"
```

Rewrite as:

```bash
# L1 phash matched:
emit_thumb_candidate \
  --decision thumb_l1_review \
  --path "$f" --keeper "$keeper" --group-id "$group_id" \
  --phash-distance "$distance" \
  --width "${w:-0}" --height "${h:-0}" --size-bytes "${_sz:-0}" \
  --reason l1_phash_match

# L1 unmatched:
emit_thumb_candidate \
  --decision thumb_l1_review \
  --path "$f" \
  --width "${w:-0}" --height "${h:-0}" --size-bytes "${_sz:-0}" \
  --reason l1_only_size
```

- [ ] **Step 3: Run smoke to confirm thumb migration is byte-clean**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: PASS=26 FAIL=0. If smoke assertions on field presence/absence pass, the helper output matches the prior emit_event output at the call sites.

- [ ] **Step 4: Migrate `bin/twincut.sh` action call sites**

This is the bulk. Each call site:

```bash
# Before:
emit_event action kind=skip src="$src" reason=excluded decision="$dec"
# After:
emit_action_skip --src "$src" --reason excluded --decision "$dec"
```

```bash
# Before:
emit_event action kind=skip src="$src" matched="$matched" reason=hardlink decision="$dec"
# After:
emit_action_skip --src "$src" --matched "$matched" --reason hardlink --decision "$dec"
```

```bash
# Before:
emit_event action kind=move src="$src" dst="$dest" dry_run=@true matched="$matched" decision="$dec"
# After:
emit_action_move --src "$src" --dst "$dest" --matched "$matched" --decision "$dec" --dry-run true
```

```bash
# Before:
emit_event action kind=move src="$src" dst="$dest" dry_run=@false matched="$matched" decision="$dec"
# After:
emit_action_move --src "$src" --dst "$dest" --matched "$matched" --decision "$dec" --dry-run false
```

```bash
# Before:
emit_event action kind=delete src="$src" dry_run=@true matched="$matched" decision="$dec"
# After:
emit_action_delete --src "$src" --matched "$matched" --decision "$dec" --dry-run true
```

```bash
# Before:
emit_event action kind=restore_unrecoverable src="$orig" dst="" dry_run=@"$RESTORE_DRY_RUN"
# After:
emit_action_restore --variant restore_unrecoverable --src "$orig" --dst "" --dry-run "$RESTORE_DRY_RUN"
```

Apply the same pattern for `restore_missing`, `restore_conflict`, `restore` variants.

- [ ] **Step 5: Migrate `bin/twincut.sh` non-action call sites**

```bash
# Before:
emit_event error code=usage_error detail="$*"
# After:
emit_error --code usage_error --detail "$*"

# Before:
emit_event error code=runtime_error detail="$*"
# After:
emit_error --code runtime_error --detail "$*"

# Before:
emit_event warn code=missing_file path="$_move" detail="apply-list source not found"
# After:
emit_warn --code missing_file --path "$_move" --detail "apply-list source not found"

# Before:
emit_event warn code=io_error path="$dir" detail="mkdir failed"
# After:
emit_warn --code io_error --path "$dir" --detail "mkdir failed"

# Before:
emit_event warn code=io_error path="$src" detail="mv failed -> $dest"
# After:
emit_warn --code io_error --path "$src" --detail "mv failed -> $dest"

# Before:
emit_event warn code=io_error path="$src" detail="rm failed"
# After:
emit_warn --code io_error --path "$src" --detail "rm failed"

# Before:
emit_event warn code=bad_video path="$f" detail="ffprobe failed or zero metadata"
# After:
emit_warn --code bad_video --path "$f" --detail "ffprobe failed or zero metadata"

# Before:
emit_event warn code=appledouble path="$f" detail="AppleDouble sidecar"
# After:
emit_warn --code appledouble --path "$f" --detail "AppleDouble sidecar"

# Before:
emit_event error code=mv_failed detail="$quar -> $orig"
# After:
emit_error --code mv_failed --detail "$quar -> $orig"

# Before:
emit_event progress phase=restore done=@"$seen" total=@"$total" current_path="$orig"
# After:
emit_progress --phase restore --done "$seen" --total "$total" --current-path "$orig"

# Before:
emit_event progress phase=scan done=@"$TOTAL" total=@"${TOTAL_SRC:-0}" current_path="$f"
# After:
emit_progress --phase scan --done "$TOTAL" --total "${TOTAL_SRC:-0}" --current-path "$f"
```

- [ ] **Step 6: Migrate run_start / run_end / dup_group call sites**

```bash
# Before:
emit_event run_start mode=restore source="$mf"
# After:
emit_run_start --mode restore --source "$mf"

# Before (in run_end calls, the existing code varies):
emit_event run_end ...
# After:
emit_run_end --status "<status>" --duration-ms "<ms>" ...
```

For each `run_end` call, inspect the current args and map field by field.

For `dup_group` calls (in cross-check and similar-video loops):

```bash
# Before (paraphrased):
emit_event dup_group group_id=@$gid match_reason="$reason" keep_path="$kp" \
  remove=@"[{\"path\":\"$rp\"}]"
# After:
emit_dup_group --group-id "$gid" --match-reason "$reason" \
  --keep-path "$kp" --remove-path "$rp"
```

The helper builds the `remove` array internally; bash callers no longer hand-construct JSON arrays. If a dup_group call site previously emitted multiple removes in one event, split it into multiple `emit_dup_group` calls (one per remove). Confirm via `git grep emit_event.*dup_group` and inspect each.

- [ ] **Step 7: Run smoke + Go tests**

```bash
bash tests/p1_thumb_phash_smoke.sh
cd ui/server && go test ./...
```

Expected: smoke 26/26 + Go tests all green. If anything fails, the helper output deviates from the prior emit_event output for that field set — fix the helper or fix the migration.

- [ ] **Step 8: Confirm `emit_event` has zero remaining callers in production code**

```bash
grep -nE 'emit_event\s+[a-z]' bin/twincut.sh lib/thumb.sh
```

Expected: empty. If anything remains, address it.

(The `emit_event` function itself is left intact for T9 to delete; we just want no production callers.)

- [ ] **Step 9: Commit**

```bash
git add bin/twincut.sh lib/thumb.sh
git commit -m "Stage 9 T5: migrate all emit_event call sites to typed helpers

~30 call sites across cross-check, self-check, thumbnail-detect, restore,
appledouble, bad-video loops moved to emit_run_start / emit_run_end /
emit_action_* / emit_thumb_candidate / emit_warn / emit_error /
emit_progress / emit_dup_group. Generic emit_event function still
present (deletion deferred to T9) but has no production callers.
Smoke 26/26 + Go tests all green."
```

---

### Task 6: bash `--json-in` apply channel + Stage 9 smoke

**Files:**
- Modify: `bin/twincut.sh` (parse `--json-in`; new apply-input adapter)
- Modify: `CLAUDE.md` (jq runtime dep)
- Create: `tests/p1_stage9_smoke.sh`

The new apply channel reads JSON-lines `ApplyCommand`s from stdin via `jq` and dispatches to `qmove`. The legacy TSV path stays available (`--thumb-confirm <file>`) until T9.

- [ ] **Step 1: Confirm `jq` is available locally and document it**

```bash
which jq
jq --version
```

Add `jq` to `CLAUDE.md`'s external runtime deps line:

```
External runtime deps: bash, ffprobe/ffmpeg, standard coreutils, md5/sha1 tooling, jq (for Stage 9 apply --json-in mode). Optional for L1 perceptual-hash pairing: python3 ≥ 3.8, Pillow ≥ 9.0, imagehash ≥ 4.3.
```

- [ ] **Step 2: Add `--json-in` flag parsing in `bin/twincut.sh`**

In the CLI parse loop (where flags are consumed), add a case for `--json-in`:

```bash
--json-in)
  JSON_IN=true
  shift
  ;;
```

Add the default at the top of the defaults block:

```bash
JSON_IN=false
```

- [ ] **Step 3: Validate `--json-in` is only accepted with `--thumbnail-detect-apply` and `--json-events`**

In the post-parse validation section (where flag combinations are checked), add:

```bash
if $JSON_IN; then
  if [[ "$APPLY_MODE" != "thumbnail_detect_apply" ]]; then
    emit_error --code usage_error --detail "--json-in only valid with --thumbnail-detect-apply"
    die "--json-in only valid with --thumbnail-detect-apply"
  fi
  if ! $JSON_EVENTS; then
    emit_error --code usage_error --detail "--json-in requires --json-events"
    die "--json-in requires --json-events"
  fi
fi
```

(Adjust the `APPLY_MODE` variable name and `die` helper name to match existing twincut.sh conventions.)

- [ ] **Step 4: Implement the apply-input adapter inside `process_apply_list` (or a new sibling function)**

Find the existing apply runner (around `bin/twincut.sh:325-365`, `process_apply_list`). It reads a TSV file. Add a branch on `$JSON_IN`:

```bash
process_apply_list(){
  if $JSON_IN; then
    process_apply_list_jsonin
    return $?
  fi
  # existing TSV reader below — unchanged
  ...
}

process_apply_list_jsonin(){
  local total=0 moved=0 skipped=0
  # Parse JSON-lines from stdin via jq; one decoded row per output line as
  # NUL-separated fields: src \t dst_dir \t keeper \t decision \t type
  # (Pre-flight: assert jq is available.)
  if ! command -v jq >/dev/null 2>&1; then
    emit_error --code usage_error --detail "jq required for --json-in mode"
    die "jq required for --json-in mode"
  fi
  while IFS=$'\t' read -r _type src dst_dir keeper decision; do
    total=$((total+1))
    case "$_type" in
      apply_move)
        # Validate decision against the allowed set.
        case "$decision" in
          thumb_l1_review|thumb_l2_exif|thumb_l3_embed|thumb_confirmed|keep_user_override) ;;
          *) emit_error --code apply_failed --path "$src" --detail "unknown decision '$decision'"
             skipped=$((skipped+1)); continue ;;
        esac
        if [[ ! -e "$src" ]]; then
          emit_warn --code missing_file --path "$src" --detail "apply src not on disk"
          skipped=$((skipped+1)); continue
        fi
        if qmove "$src" "$dst_dir" "$keeper" "" "$decision"; then
          moved=$((moved+1))
        else
          skipped=$((skipped+1))
        fi
        ;;
      apply_skip)
        emit_action_skip --src "$src" --decision "$decision" --reason user_override
        skipped=$((skipped+1))
        ;;
      *)
        emit_error --code apply_failed --path "$src" --detail "unknown ApplyCommand type '$_type'"
        skipped=$((skipped+1))
        ;;
    esac
  done < <(jq -rc 'select(.type == "apply_move" or .type == "apply_skip") |
                   [.type, (.src // ""), (.dst_dir // ""), (.keeper // ""), (.decision // "")] | @tsv')
  emit_run_end --status succeeded --total "$total" --applied "$moved" --skipped "$skipped"
  return 0
}
```

The `jq` filter:
- Selects only known command types
- Extracts five fields per command, in fixed order
- `@tsv` produces tab-separated output with proper escaping of embedded tabs/newlines (jq replaces them with `\t` / `\n` literals; bash `read -r` round-trips correctly for normal paths)

Note: this design assumes paths do NOT contain literal `\t` (jq's `@tsv` quoting) or newlines. macOS paths nearly never contain these, but the choice is documented here. A truly path-safe protocol would use `@text` with NUL separators, but the read loop semantics get more complex. Defer NUL separation to a follow-up unless a real-world path breaks it.

- [ ] **Step 5: Hook `process_apply_list_jsonin` into the apply mode dispatch**

The current apply dispatch is around `bin/twincut.sh:920-935`. The dispatch already calls `process_apply_list` (or whatever the existing function is named). After Step 4 the function picks `--json-in` branch when set; no further changes needed at the dispatch.

If the current code does `thumb_confirm_review "$THUMB_CONFIRM_FILE"` directly (skipping `process_apply_list`), refactor so that when `$JSON_IN`, it calls `process_apply_list_jsonin` instead. The exact site depends on how thumb_confirm_review and process_apply_list interact today; read both first.

- [ ] **Step 6: Write `tests/p1_stage9_smoke.sh`**

```bash
#!/usr/bin/env bash
# tests/p1_stage9_smoke.sh — Stage 9 end-to-end smoke.
#
# Sets up a tiny fixture image dir, runs a scan with --json-events,
# composes a synthetic ApplyCommand JSON-lines stream, pipes it to
# --thumbnail-detect-apply --json-in --json-events, and asserts the
# resulting events.ndjson + filesystem state.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TWINCUT="$ROOT/bin/twincut.sh"
PASS=0
FAIL=0

assert(){
  local what="$1" cond="$2"
  if eval "$cond"; then
    echo "  ok   $what"
    PASS=$((PASS+1))
  else
    echo "  FAIL $what (cond: $cond)"
    FAIL=$((FAIL+1))
  fi
}

# === 1. fixture image dir ===
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SRC="$TMP/src"; mkdir -p "$SRC"
# Use Python to build gradient PNGs if Pillow is available; else skip.
if ! python3 -c 'import PIL' 2>/dev/null; then
  echo "[skip] Pillow not installed — Stage 9 smoke needs gradient fixtures"
  exit 0
fi
python3 - <<PY
from PIL import Image
import os
src = "$SRC"
def grad(name, size, axis="x"):
    im = Image.new("RGB", size)
    for x in range(size[0]):
        for y in range(size[1]):
            v = x if axis == "x" else y
            im.putpixel((x, y), (v % 256, (v*2) % 256, (v*3) % 256))
    im.save(os.path.join(src, name))
grad("keeper.jpg", (3024, 4032))
grad("suspect_small.jpg", (320, 240))    # L1 + L2 thumb candidate
grad("unrelated_big.jpg", (2000, 1500))  # NOT a thumb
PY

# === 2. preview scan ===
PREVIEW_NDJSON="$TMP/preview.ndjson"
"$TWINCUT" --thumbnail-detect --source "$SRC" --json-events \
  3>"$PREVIEW_NDJSON" >/dev/null 2>&1 || true

assert "preview emitted at least one run_start" \
  '[[ $(grep -c "\"type\":\"run_start\"" "$PREVIEW_NDJSON") -ge 1 ]]'

assert "preview emitted at least one thumb_candidate" \
  '[[ $(grep -c "\"type\":\"thumb_candidate\"" "$PREVIEW_NDJSON") -ge 1 ]]'

assert "preview ended with run_end" \
  '[[ $(grep -c "\"type\":\"run_end\"" "$PREVIEW_NDJSON") -ge 1 ]]'

# === 3. compose ApplyCommand JSON-lines (one apply_move for suspect_small) ===
APPLY_INPUT="$TMP/apply.ndjson"
QUAR_DIR="$SRC/_QUARANTINE/_thumbs"
cat > "$APPLY_INPUT" <<EOF
{"type":"apply_move","src":"$SRC/suspect_small.jpg","dst_dir":"$QUAR_DIR","keeper":"$SRC/keeper.jpg","decision":"thumb_l2_exif"}
EOF

# === 4. apply via --json-in ===
APPLY_NDJSON="$TMP/apply_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SRC" \
  3>"$APPLY_NDJSON" < "$APPLY_INPUT" >/dev/null 2>&1 || true

assert "apply emitted one action kind=move" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_NDJSON") -eq 1 ]]'

assert "apply quarantine file exists" \
  '[[ -e "$QUAR_DIR/suspect_small.jpg" ]]'

assert "apply source file removed" \
  '[[ ! -e "$SRC/suspect_small.jpg" ]]'

assert "apply emitted run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_NDJSON"'

# === 5. unknown decision triggers error event, no crash ===
APPLY_BAD="$TMP/apply_bad.ndjson"
cat > "$APPLY_BAD" <<EOF
{"type":"apply_move","src":"$SRC/unrelated_big.jpg","dst_dir":"$QUAR_DIR","keeper":"","decision":"NOT_A_VALID_DECISION"}
EOF
APPLY_BAD_NDJSON="$TMP/apply_bad_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SRC" \
  3>"$APPLY_BAD_NDJSON" < "$APPLY_BAD" >/dev/null 2>&1 || true

assert "bad-decision emitted error event" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$APPLY_BAD_NDJSON"'

assert "bad-decision left source file intact" \
  '[[ -e "$SRC/unrelated_big.jpg" ]]'

echo "========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
```

Make executable:
```bash
chmod +x tests/p1_stage9_smoke.sh
```

- [ ] **Step 7: Run the Stage 9 smoke**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: PASS=8 FAIL=0 (or "skip" if Pillow not installed — in which case the test exits 0 with the skip notice).

- [ ] **Step 8: Run the full suite — main stays green**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
bash tests/p1_stage9_smoke.sh
```

- [ ] **Step 9: Commit**

```bash
git add bin/twincut.sh CLAUDE.md tests/p1_stage9_smoke.sh
git commit -m "Stage 9 T6: bash --json-in apply mode + p1_stage9_smoke.sh

--json-in flag: --thumbnail-detect-apply reads ApplyCommand JSON-lines
from stdin via jq, validates decision against allowed set, dispatches
to qmove. Legacy --thumb-confirm <file> path unchanged.

CLAUDE.md: jq added to runtime deps.

tests/p1_stage9_smoke.sh: end-to-end scan + apply with the new channel,
including bad-decision negative case."
```

---

### Task 7: Go `ApplyCommand` struct + `composeApplyCommands`

**Files:**
- Modify: `ui/server/events.go` (add `ApplyCommand`)
- Modify: `ui/server/events_test.go` (Marshal/Unmarshal tests)
- Modify: `ui/server/apply_list.go` (add `composeApplyCommands`)
- Modify: `ui/server/apply_list_test.go` (new tests)

- [ ] **Step 1: Write failing test for `ApplyCommand` Marshal**

In `ui/server/events_test.go`, append:

```go
func TestApplyCommand_MarshalApplyMove(t *testing.T) {
	cmd := ApplyCommand{
		Type:     "apply_move",
		Src:      "/img/IMG.JPG",
		DstDir:   "/img/_Q/_thumbs",
		Keeper:   "/img/IMG.HEIC",
		Decision: "thumb_l2_exif",
	}
	got, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"apply_move","src":"/img/IMG.JPG","dst_dir":"/img/_Q/_thumbs","keeper":"/img/IMG.HEIC","decision":"thumb_l2_exif"}`
	if string(got) != want {
		t.Errorf("got=%s\nwant=%s", got, want)
	}
}

func TestApplyCommand_MarshalApplySkipOmitsKeeper(t *testing.T) {
	cmd := ApplyCommand{
		Type:     "apply_skip",
		Src:      "/img/IMG.JPG",
		Decision: "keep_user_override",
	}
	got, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"apply_skip","src":"/img/IMG.JPG","decision":"keep_user_override"}`
	if string(got) != want {
		t.Errorf("got=%s\nwant=%s", got, want)
	}
}
```

- [ ] **Step 2: Verify the tests fail (struct not defined)**

```bash
cd ui/server && go test -run TestApplyCommand ./...
```

Expected: compile error or fail.

- [ ] **Step 3: Add `ApplyCommand` to `events.go`**

```go
// ApplyCommand is a Go-side authored command sent to bash via the
// --thumbnail-detect-apply --json-in stdin pipe. One per line.
// Stage 9 contract: Go composes these from the preview run's events
// + user UI selections; bash dispatches to qmove (apply_move) or
// emits a skip action (apply_skip).
type ApplyCommand struct {
	Type     string `json:"type"`               // apply_move | apply_skip
	Src      string `json:"src"`                // source absolute path
	DstDir   string `json:"dst_dir,omitempty"`  // required for apply_move; destination directory (bash picks final filename)
	Keeper   string `json:"keeper,omitempty"`   // optional; matched-keeper path for hardlink-safety + manifest
	Decision string `json:"decision"`           // allowed set enforced by bash
}
```

- [ ] **Step 4: Run the tests, verify PASS**

```bash
cd ui/server && go test -run TestApplyCommand ./...
```

- [ ] **Step 5: Write failing test for `composeApplyCommands`**

In `ui/server/apply_list_test.go`, append:

```go
func TestComposeApplyCommands_ThumbnailDetect(t *testing.T) {
	view := &ResultsView{
		Members: []ResultMember{
			{Path: "/img/a.jpg", Decision: "thumb_l2_exif", Keeper: "/img/a.heic", Action: "move"},
			{Path: "/img/b.jpg", Decision: "thumb_l1_review", Keeper: "/img/b.heic", Action: "move", PhashDistance: 3},
			{Path: "/img/c.jpg", Decision: "keep_user_override", Action: "skip"},
		},
	}
	dstDir := "/img/_QUARANTINE/_thumbs"
	got := composeApplyCommands(view, dstDir)
	want := `{"type":"apply_move","src":"/img/a.jpg","dst_dir":"/img/_QUARANTINE/_thumbs","keeper":"/img/a.heic","decision":"thumb_l2_exif"}
{"type":"apply_move","src":"/img/b.jpg","dst_dir":"/img/_QUARANTINE/_thumbs","keeper":"/img/b.heic","decision":"thumb_l1_review"}
{"type":"apply_skip","src":"/img/c.jpg","decision":"keep_user_override"}
`
	if string(got) != want {
		t.Errorf("composeApplyCommands mismatch:\n got=%q\nwant=%q", got, want)
	}
}
```

(Adjust `ResultsView` and `ResultMember` field names to match `apply_list.go`'s actual types.)

- [ ] **Step 6: Verify the test fails**

```bash
cd ui/server && go test -run TestComposeApplyCommands ./...
```

Expected: compile error (`composeApplyCommands` undefined).

- [ ] **Step 7: Implement `composeApplyCommands` in `apply_list.go`**

```go
// composeApplyCommands serializes a confirmed thumbnail review view into a
// JSON-lines byte stream suitable for piping to:
//   twincut --thumbnail-detect-apply --json-events --json-in
//
// One ApplyCommand per ResultMember. Members with Action=="skip" become
// apply_skip; everything else becomes apply_move with DstDir set.
//
// dstDir is the destination directory (e.g. "<source>/_QUARANTINE/_thumbs");
// bash picks the final filename inside via qmove's collision logic.
func composeApplyCommands(view *ResultsView, dstDir string) []byte {
	var buf bytes.Buffer
	for _, m := range view.Members {
		var cmd ApplyCommand
		if m.Action == "skip" {
			cmd = ApplyCommand{
				Type:     "apply_skip",
				Src:      m.Path,
				Decision: m.Decision,
			}
		} else {
			cmd = ApplyCommand{
				Type:     "apply_move",
				Src:      m.Path,
				DstDir:   dstDir,
				Keeper:   m.Keeper,
				Decision: m.Decision,
			}
		}
		line, err := json.Marshal(cmd)
		if err != nil {
			// json.Marshal on a fixed struct cannot fail; defensive only.
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
```

Add the necessary imports (`bytes`, `encoding/json`) if not already present.

- [ ] **Step 8: Run the tests, verify PASS**

```bash
cd ui/server && go test -run "TestApplyCommand|TestComposeApplyCommands" ./...
cd ui/server && go test ./...
```

Expected: all green.

- [ ] **Step 9: Commit**

```bash
git add ui/server/events.go ui/server/events_test.go ui/server/apply_list.go ui/server/apply_list_test.go
git commit -m "Stage 9 T7: Go ApplyCommand struct + composeApplyCommands

New typed input schema for the bash --json-in apply channel. One
function builds the JSON-lines byte stream from a ResultsView.
composeThumbnailConfirmTSV is still present (deletion in T9)."
```

---

### Task 8: Go `handleThumbnailsApply` switches to stdin pipe

**Files:**
- Modify: `ui/server/runs.go` (`StartOptions.Stdin` field)
- Modify: `ui/server/thumbnail.go` (`handleThumbnailsApply`)
- Modify: `ui/server/thumbnail_test.go` (argv + stdin assertions)

- [ ] **Step 1: Read `runs.go` to find the Start function and StartOptions struct**

Confirm whether `Stdin io.Reader` is already a field. If yes, skip Step 2. If no, add it.

- [ ] **Step 2: Add `Stdin io.Reader` to `StartOptions`**

In `runs.go`, in the `StartOptions` struct, add:

```go
// Stdin is an optional reader piped to the spawned process's stdin.
// Used by Stage 9's apply mode to stream ApplyCommand JSON-lines.
Stdin io.Reader
```

Update `Start()` to thread this through `exec.Cmd.Stdin` if non-nil:

```go
// inside Start(opts StartOptions) ...
if opts.Stdin != nil {
    cmd.Stdin = opts.Stdin
}
```

(Look for an existing pattern around how `cmd.Stdout`/`cmd.Stderr` are wired, and follow it.)

- [ ] **Step 3: Write a failing test for `handleThumbnailsApply` with stdin**

In `ui/server/thumbnail_test.go`, append:

```go
func TestHandleThumbnailsApply_Stage9StdinPipe(t *testing.T) {
	// Fixture preview run with two L2 candidates.
	srv, cleanup := newTestServer(t)
	defer cleanup()

	previewID := seedPreviewRun(t, srv, []ResultMember{
		{Path: "/img/a.jpg", Decision: "thumb_l2_exif", Keeper: "/img/a.heic", Action: "move"},
		{Path: "/img/b.jpg", Decision: "keep_user_override", Action: "skip"},
	})

	req := httptest.NewRequest("POST", "/api/thumbnails/apply", strings.NewReader(
		fmt.Sprintf(`{"preview_run_id":"%s","source":"/img"}`, previewID)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.runs.SetSpawnHook(func(opts StartOptions) {
		// Assert argv does NOT contain --thumb-confirm or --thumb-dir
		for _, a := range opts.Args {
			if a == "--thumb-confirm" || strings.HasPrefix(a, "--thumb-dir") {
				t.Errorf("Stage 9 argv must not contain %q; got args=%v", a, opts.Args)
			}
		}
		// Assert argv contains --json-in
		var hasJSONIn bool
		for _, a := range opts.Args {
			if a == "--json-in" {
				hasJSONIn = true
				break
			}
		}
		if !hasJSONIn {
			t.Errorf("Stage 9 argv must contain --json-in; got args=%v", opts.Args)
		}
		// Read all stdin bytes and assert they are exactly the expected JSON-lines.
		got, _ := io.ReadAll(opts.Stdin)
		want := `{"type":"apply_move","src":"/img/a.jpg","dst_dir":"/img/_QUARANTINE/_thumbs","keeper":"/img/a.heic","decision":"thumb_l2_exif"}
{"type":"apply_skip","src":"/img/b.jpg","decision":"keep_user_override"}
`
		if string(got) != want {
			t.Errorf("stdin bytes mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

(Names `newTestServer`, `seedPreviewRun`, `SetSpawnHook` reflect a typical test-fixture pattern; adjust to actual names in `thumbnail_test.go`. If no spawn-hook seam exists, add one in `runs.go` — a `SpawnHook func(StartOptions)` field on the run manager that, if non-nil, is invoked instead of `exec.Cmd.Start`.)

- [ ] **Step 4: Verify the test fails**

```bash
cd ui/server && go test -run TestHandleThumbnailsApply_Stage9StdinPipe ./...
```

Expected: FAIL (current code still writes TSV + uses --thumb-confirm).

- [ ] **Step 5: Rewrite `handleThumbnailsApply` to use the stdin pipe**

In `ui/server/thumbnail.go`, replace the TSV-write + argv flag construction with:

```go
// Old:
//   tsvPath := filepath.Join(s.stateDir, "runs", applyRunID+".thumb-confirm.tsv")
//   if err := writeConfirmTSV(tsvPath, view); err != nil { ... }
//   args := []string{"--thumbnail-detect-apply", "--json-events", "--thumb-confirm", tsvPath, "--thumb-dir", thumbDir, ...}

// New:
dstDir := filepath.Join(req.Source, "_QUARANTINE", "_thumbs")
cmds := composeApplyCommands(view, dstDir)
args := []string{
    "--thumbnail-detect-apply",
    "--json-events",
    "--json-in",
    "--source", req.Source,
}

run, err := s.runs.Start(StartOptions{
    ID:    applyRunID,
    Cmd:   s.twincutBin,
    Args:  args,
    Stdin: bytes.NewReader(cmds),
    Mode:  "thumbnail_detect_apply",
    // ... existing fields (journalTo, etc.)
})
if err != nil {
    ...
}
```

Drop the TSV write step entirely. Drop the `--thumb-dir` and `--thumb-confirm` argv elements.

(Confirm the `applyRunID` is already caller-provided per Stage 8.5 P0 #3; no change needed there.)

- [ ] **Step 6: Run the new test, verify PASS**

```bash
cd ui/server && go test -run TestHandleThumbnailsApply_Stage9StdinPipe ./...
```

- [ ] **Step 7: Run all tests including legacy thumbnail UI suite**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
bash tests/p1_stage9_smoke.sh
```

Expected: all green. Older `TestHandleThumbnailsApply` cases that assert the TSV path or `--thumb-confirm` flag must be **updated** (not removed; we want assertions of the new shape).

- [ ] **Step 8: Commit**

```bash
git add ui/server/runs.go ui/server/thumbnail.go ui/server/thumbnail_test.go
git commit -m "Stage 9 T8: handleThumbnailsApply pipes ApplyCommands via stdin

- runs.go: StartOptions gains Stdin io.Reader; Start() threads it to
  exec.Cmd.Stdin when non-nil.
- thumbnail.go: composeApplyCommands(view, dstDir) builds the JSON-lines
  byte stream; piped through StartOptions.Stdin. No more TSV write,
  no --thumb-dir / --thumb-confirm flags.
- thumbnail_test.go: assert argv shape (--json-in present, --thumb-*
  absent) and stdin bytes match the expected JSON-lines."
```

---

### Task 9: Delete old code paths

**Files:**
- Modify: `ui/server/apply_list.go` (delete `composeThumbnailConfirmTSV`)
- Modify: `ui/server/apply_list_test.go` (delete TSV-compose tests)
- Modify: `lib/thumb.sh` (delete `thumb_confirm_review`)
- Modify: `bin/twincut.sh` (delete `--thumb-confirm <file>` flag handling; delete `emit_event`; delete `THUMB_CONFIRM_FILE` default)
- Modify: `ui/server/results.go` (audit any reads of `_thumbnails/_review.csv` or `.thumb-confirm.tsv` and remove if dead)

- [ ] **Step 1: Delete `composeThumbnailConfirmTSV` from `apply_list.go`**

Remove the function. Remove any unused imports.

- [ ] **Step 2: Delete the TSV-compose tests from `apply_list_test.go`**

Search for tests named like `TestComposeThumbnailConfirmTSV*` and remove them.

- [ ] **Step 3: Verify Go suite still green**

```bash
cd ui/server && go test ./...
```

- [ ] **Step 4: Delete `thumb_confirm_review` from `lib/thumb.sh`**

Remove the entire function (around `lib/thumb.sh:679`).

- [ ] **Step 5: Delete `--thumb-confirm` flag handling from `bin/twincut.sh`**

- Remove the flag-parse case (search `--thumb-confirm`).
- Remove the call site (search `thumb_confirm_review`).
- Remove the `THUMB_CONFIRM_FILE=""` default at the top.

- [ ] **Step 6: Delete the now-unused `emit_event` function from `bin/twincut.sh`**

Remove lines 188-216 (the `emit_event` function definition). Confirm zero callers first:

```bash
grep -nE 'emit_event\b' bin/twincut.sh lib/thumb.sh tests/
```

If anything remains in tests, decide per-case whether to update or remove.

- [ ] **Step 7: Run full suite**

```bash
cd ui/server && go test ./...
bash tests/p1_thumb_phash_smoke.sh
bash tests/events_contract.sh
bash tests/p1_stage9_smoke.sh
```

Expected: all green.

- [ ] **Step 8: Commit**

```bash
git add ui/server/apply_list.go ui/server/apply_list_test.go lib/thumb.sh bin/twincut.sh
git commit -m "Stage 9 T9: delete obsolete code paths

- ui/server/apply_list.go: composeThumbnailConfirmTSV deleted.
- ui/server/apply_list_test.go: TSV-compose tests deleted.
- lib/thumb.sh: thumb_confirm_review deleted (replaced by --json-in
  inline processing in T6).
- bin/twincut.sh: --thumb-confirm flag handling, THUMB_CONFIRM_FILE
  default, and the now-unused generic emit_event function deleted.

Closes stage8-followup items #1, #2, #3 (TSV round-trip), and
#6 (orphan TSV TTL — no more TSV to leave orphan)."
```

---

### Task 10: Close-out — docs + final third-party review

**Files:**
- Modify: `CLAUDE.md` (Stage 9 paragraph, contract documentation)
- Modify: `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md` (mark items closed)
- Dispatch: `reviewer-gemini`

- [ ] **Step 1: Add Stage 9 paragraph to `CLAUDE.md`**

Append to the Architecture Notes section:

```markdown
- **Stage 9 (`thumbnail_detect` only): Go-owned contract.** The web-UI
  `--json-events` channel uses a single typed schema rooted in Go structs
  (`ui/server/events.go`). bash emits via per-type helpers in
  `lib/events.sh` (e.g. `emit_thumb_candidate`, `emit_action_move`); the
  generic `emit_event` is gone. Apply input flows from Go to bash as
  stdin JSON-lines (`ApplyCommand` records via `--json-in`); no more
  `.thumb-confirm.tsv` round-trip. Drift between Go and bash is caught
  by `ui/server/events_roundtrip_test.go`, which decodes every fixture
  in `tests/fixtures/events/` with `json.Decoder.DisallowUnknownFields`.
  Cross-check, self-check, and restore still use the typed helpers (T5
  migrated all call sites) but their workflow contracts remain hybrid
  pending future stages.
```

- [ ] **Step 2: Mark stage8-followup items closed**

In `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md`:

- §1 ("Mutable, source-scoped `_review.csv` ..."): append a note "**Resolved in Stage 9** (PR <#>): web-UI path no longer writes any review/confirm artifact."
- §2 (Confirm TSV drops keeper): append "**Resolved in Stage 8.5 PR #7 + Stage 9 contract migration**."
- §3 (TOCTOU): append "**Resolved in Stage 8.5 (caller-provided ID) + Stage 9 (no TSV write at all)**."
- §6 (orphan TSV TTL): append "**Resolved in Stage 9** — `.thumb-confirm.tsv` no longer written."
- §7 (Go-owned vs bash-owned): append "**Resolved in Stage 9**: chose Go-owned per stage8-followup option A. `thumbnail_detect` migrated; other workflows pending."

- [ ] **Step 3: Commit the docs**

```bash
git add CLAUDE.md docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md
git commit -m "Stage 9 T10 docs: CLAUDE.md Stage 9 paragraph + stage8-followup closed"
```

- [ ] **Step 4: Dispatch reviewer-gemini for the cumulative Stage 9 diff**

Run the third-party reviewer against the full branch diff vs `main`:

```bash
# From the working directory:
git fetch origin main
# Then dispatch reviewer-gemini via the Agent tool (or interactively):
#   "Adversarial third-party review of the Stage 9 cumulative diff.
#    Branch: feature/stage-9-go-owned-contract. Diff: git diff origin/main..HEAD.
#    Spec: docs/superpowers/specs/2026-05-22-twincut-stage9-go-owned-contract-design.md.
#    Focus areas: (1) drift between lib/events.sh helpers and events.go structs;
#    (2) jq @tsv robustness for paths containing tabs/newlines/quotes;
#    (3) error/partial-failure semantics in process_apply_list_jsonin;
#    (4) Go composeApplyCommands edge cases (empty members, skip-only views);
#    (5) deletion of emit_event — any silent loss of error reporting?"
```

Address any P0/P1 findings inline. P2 nits can be captured as TODOs in a follow-up issue.

- [ ] **Step 5: Open PR**

```bash
git push -u origin feature/stage-9-go-owned-contract
gh pr create --title "Stage 9: Go-owned contract for thumbnail_detect" --body "$(cat <<'EOF'
## Summary

Implements Stage 9 per docs/superpowers/specs/2026-05-22-twincut-stage9-go-owned-contract-design.md.

- `lib/events.sh`: typed bash emitters, one per event type
- `ui/server/events_roundtrip_test.go`: drift catcher (DisallowUnknownFields against bash fixtures)
- `--json-in` apply channel: Go pipes ApplyCommand JSON-lines via stdin; no more `.thumb-confirm.tsv` round-trip
- Closes stage8-followup items #1, #2, #3, #6, and resolves §7 strategic question (Go-owned path chosen)
- Scope is `thumbnail_detect` only; cross-check / self-check / restore migrate later

## Test plan
- [x] `bash tests/events_contract.sh` (13/13)
- [x] `bash tests/p1_thumb_phash_smoke.sh` (26/26 — unchanged, regression guard)
- [x] `bash tests/p1_stage9_smoke.sh` (new end-to-end, ≥8 assertions)
- [x] `cd ui/server && go test ./...`
- [x] reviewer-gemini cumulative review

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review notes

After writing the plan, this section captures the spec→plan coverage map and any open ambiguities the implementer should know about.

**Spec coverage:**
- §1 Goal → addressed across T1-T10 (each task makes incremental progress toward the typed contract).
- §2 Architecture diagram → T6 (bash `--json-in`) + T8 (Go stdin pipe) realize both boundaries.
- §3 Components → every file in the spec's table has a task that touches it.
- §4.1 Scan data flow → T3 (`emit_thumb_candidate`) + T5 (call-site migration). The current scan already emits NDJSON; T3 just tightens the schema.
- §4.2 Apply data flow → T6 (bash side) + T7 (Go side) + T8 (wire-up).
- §4.3 Error handling → T6 (per-row continue, allowed-set validation, run_end status).
- §4.4 Replay → no new code; relies on existing journal in `<stateDir>/runs/<id>/events.ndjson`. Documented in T10 close-out.
- §5 Testing strategy → T1 establishes layers 1+2 infrastructure; T2-T4 extend layer 1; T6 adds layer 3 (smoke).
- §6 Migration sequencing → tasks map 1:1 (spec step 1 = T1+T2+T3+T4; step 2 = T5; step 3 = T6; step 4 = T7+T8; step 5 = T9; step 6 = T10).
- §7 Decision log → reflected in helper API choices (long-options, allowed-set validation, dst_dir per-command).
- §8 Scope boundaries → only `--thumbnail-detect`-related Go handlers touched in T7+T8; cross-check/self-check/restore call sites benefit from typed helpers (T5) without workflow changes.

**Known open ambiguities** (engineer should resolve via the spec or by asking):
- Exact spawn-hook seam in `runs.go` — varies by current code structure; T8 Step 3 acknowledges this and offers a fallback (add the seam if absent).
- Whether to keep `--thumb-confirm` flag with a usage-error stub or delete outright — spec §7 decision: delete outright (T9 Step 5).
- jq `@tsv` robustness on paths with literal tabs/newlines — explicitly accepted limitation in T6 Step 4 (defer NUL separation).

**Type consistency check:**
- `EventEnvelope` embedding: T1 introduces it; T2/T3/T4 extend it; all `want` struct literals in `roundtripFixtures()` use the same envelope-init pattern.
- `ApplyCommand` fields: T7 defines `{Type, Src, DstDir, Keeper, Decision}`; T6 bash extracts the same five fields via jq; T8 stdin pipe carries them; consistent.
- Helper names: every `emit_*` in T1-T4 is referenced consistently in T5 (call-site migration) and T6 (apply-input adapter).
