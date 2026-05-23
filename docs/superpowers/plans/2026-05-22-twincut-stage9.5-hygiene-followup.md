# Stage 9.5 Hygiene Follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the two P1 hygiene findings deferred from Stage 9 (legacy timestamp seam + `jq @tsv` path safety), achieve apply-path decision-allowlist parity between `apply_move` and `apply_skip`, and patch five smoke-test gaps. Pure bash + bash-test work, no Go changes.

**Architecture:** Five small TDD commits on `bin/twincut.sh` and `tests/p1_stage9_smoke.sh` plus one new micro-test file. No schema changes, no fixtures added, no Go-side struct changes. Each task touches independent code regions and can ship as its own commit.

**Tech Stack:** bash 3.2+ (macOS portability), `jq`, `python3` for path resolution, existing helpers in `lib/events.sh`.

**Spec:** `docs/superpowers/specs/2026-05-22-twincut-stage9.5-hygiene-followup-design.md` (commit `3e94028`).

**Branch:** `feature/stage-9.5-hygiene-followup` off `main @ f6973ac`.

---

## File touch map

| Path | Action | Tasks |
|---|---|---|
| `bin/twincut.sh` | edit | T1 (line 188), T2 (lines 395-451), T3 (top of `process_apply_list_jsonin`), T4 (lines 415-421, 441) |
| `tests/p1_stage9_smoke.sh` | edit | T2 (D1), T3 (D2, D3), T4 (D5), T5 (D4) |
| `tests/legacy_event_ts_seam.sh` | create | T1 |

No other files are touched. `lib/events.sh`, `lib/thumb.sh`, `ui/server/*.go`, `tests/events_contract.sh`, `tests/fixtures/events/*.ndjson`, `bin/phash.py`, `bin/vid_eq.sh`, and `CLAUDE.md` are out of scope.

---

## Task 1: Legacy `emit_event` honors `TWINCUT_TEST_TS`

Wires the 7 retained legacy `emit_event` callers (cross-check + restore + similar-video) through the same `_emit_now_ts` seam that the typed helpers in `lib/events.sh` already use. Unlocks fixture-stable testing for legacy events.

**Files:**
- Modify: `bin/twincut.sh` (the `emit_event` function body, ~line 185-208)
- Create: `tests/legacy_event_ts_seam.sh`

- [ ] **Step 1: Write the failing test**

Create `tests/legacy_event_ts_seam.sh`:

```bash
#!/usr/bin/env bash
# tests/legacy_event_ts_seam.sh — verify that the legacy emit_event helper
# in bin/twincut.sh honors the TWINCUT_TEST_TS seam (P1 #4 from Stage 9
# reviewer-gemini). Runs a cross-check against two empty tempdirs; the
# cross-check entry path fires a legacy emit_event run_start before any I/O.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TWINCUT="$ROOT/bin/twincut.sh"

src=$(mktemp -d)
bk=$(mktemp -d)
out=$(mktemp)
trap 'rm -rf "$src" "$bk" "$out"' EXIT

TWINCUT_TEST_TS=1747934400 RUN_ID=r_legacy_ts \
  "$TWINCUT" --source "$src" --backup "$bk" --dry-run --json-events \
  >"$out" 2>/dev/null || true

first_line=$(head -1 "$out")

if ! grep -q '"type":"run_start"' <<<"$first_line"; then
  echo "FAIL: first emitted line is not run_start"
  echo "got: $first_line"
  exit 1
fi

if ! grep -q '"ts":1747934400' <<<"$first_line"; then
  echo "FAIL: ts seam not honored by legacy emit_event"
  echo "got: $first_line"
  exit 1
fi

echo "ok: legacy emit_event honors TWINCUT_TEST_TS"
```

- [ ] **Step 2: Make the test executable and run to verify it fails**

```bash
chmod +x tests/legacy_event_ts_seam.sh
bash tests/legacy_event_ts_seam.sh
```

Expected: `FAIL: ts seam not honored by legacy emit_event`, with `"ts":<some-other-unix-int>` in the actual output (the wall-clock from `date -u +%s`).

- [ ] **Step 3: Patch `bin/twincut.sh:188`**

Use Edit with this exact replacement (the surrounding function context disambiguates):

Find:
```bash
emit_event(){
  $JSON_EVENTS || return 0
  local type="$1"; shift
  local out='{"type":"'"$type"'","ts":'"$(date -u +%s)"
```

Replace with:
```bash
emit_event(){
  $JSON_EVENTS || return 0
  local type="$1"; shift
  local ts; ts=$(_emit_now_ts)
  local out='{"type":"'"$type"'","ts":'"$ts"
```

`lib/events.sh` is sourced at `bin/twincut.sh:89`, well before `emit_event` is defined, so `_emit_now_ts` is in scope.

- [ ] **Step 4: Run the new test to verify it passes**

```bash
bash tests/legacy_event_ts_seam.sh
```

Expected: `ok: legacy emit_event honors TWINCUT_TEST_TS`

- [ ] **Step 5: Run the existing smoke tests to verify no regression**

```bash
bash tests/p1_stage9_smoke.sh
bash tests/events_contract.sh
```

Expected: both still report `FAIL=0` (12/12 and 14/14 respectively).

- [ ] **Step 6: Commit**

```bash
git add bin/twincut.sh tests/legacy_event_ts_seam.sh
git commit -m "Stage 9.5 T1: legacy emit_event honors TWINCUT_TEST_TS seam"
```

---

## Task 2: NUL-delim apply parser (Chunk B-1)

Replaces the `jq … @tsv` + tab-IFS pipeline in `process_apply_list_jsonin` with a NUL-delimited stream. Eliminates field-mangling for paths containing literal tab or newline. No new behavior on the happy path; D1 smoke proves the tab case.

**Files:**
- Modify: `bin/twincut.sh` (the `process_apply_list_jsonin` function body, ~lines 395-452)
- Modify: `tests/p1_stage9_smoke.sh` (add D1 assertion block)

- [ ] **Step 1: Write the failing smoke assertion (D1 — tab in path)**

Append to `tests/p1_stage9_smoke.sh` *before* the `echo "==="` summary line at the end:

```bash
# === D1. tab-in-path — NUL-delim parser must preserve literal \t ===
TAB_NAME="$(printf 'tab%bafter' '\t')"   # tab character embedded in name
TAB_SRC="$TMP/srcD1"; mkdir -p "$TAB_SRC"
cp "$SRC/keeper.jpg" "$TAB_SRC/keeper.jpg"
cp "$SRC/unrelated_big.jpg" "$TAB_SRC/$TAB_NAME.jpg"
TAB_QUAR="$TAB_SRC/_QUARANTINE/_thumbs"

APPLY_TAB="$TMP/apply_tab.ndjson"
# jq pre-composes the JSON so the tab byte is correctly escaped as \t in the JSON literal
jq -cn --arg src "$TAB_SRC/$TAB_NAME.jpg" \
      --arg dst "$TAB_QUAR" \
      --arg keep "$TAB_SRC/keeper.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  > "$APPLY_TAB"

APPLY_TAB_NDJSON="$TMP/apply_tab_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$TAB_SRC" \
  >"$APPLY_TAB_NDJSON" 2>/dev/null < "$APPLY_TAB" || true

assert "D1: tab-in-path apply emits action kind=move" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_TAB_NDJSON") -eq 1 ]]'

assert "D1: tab-in-path: quarantine file with tab in name exists" \
  '[[ -e "$TAB_QUAR/$TAB_NAME.jpg" ]]'

assert "D1: tab-in-path: original source file removed" \
  '[[ ! -e "$TAB_SRC/$TAB_NAME.jpg" ]]'
```

- [ ] **Step 2: Run the smoke to verify D1 fails**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: existing 12 assertions still pass. The three new `D1:` assertions FAIL — the quarantine file lands at a path with literal `\t` (backslash + t) bytes instead of the real tab byte. Total: 12 pass + 3 fail.

- [ ] **Step 3: Rewrite the parser in `bin/twincut.sh::process_apply_list_jsonin`**

Use Edit. Find this exact block (post-guards, the `while ... done < <(jq @tsv)` section):

```bash
  local total=0 moved=0 skipped=0
  local _type src dst_dir keeper decision
  local src_root abs_src abs_dst
  src_root="$(_resolve_abs "$SOURCE_DIR")"
  while IFS=$'\t' read -r _type src dst_dir keeper decision; do
    total=$((total+1))
```

Replace with:

```bash
  local total=0 moved=0 skipped=0
  local _type src dst_dir keeper decision
  local src_root abs_src abs_dst
  src_root="$(_resolve_abs "$SOURCE_DIR")"
  while IFS= read -r -d '' _type   && \
        IFS= read -r -d '' src     && \
        IFS= read -r -d '' dst_dir && \
        IFS= read -r -d '' keeper  && \
        IFS= read -r -d '' decision; do
    total=$((total+1))
```

Then find this exact block at the bottom of the function (the jq @tsv invocation just before `emit_run_end`):

```bash
  done < <(jq -rc 'select(.type == "apply_move" or .type == "apply_skip") |
                   [.type, (.src // ""), (.dst_dir // ""), (.keeper // ""), (.decision // "")] | @tsv')
  emit_run_end --status succeeded --total "$total" --applied "$moved" --skipped "$skipped"
```

Replace with:

```bash
  done < <(jq -rj 'select(.type == "apply_move" or .type == "apply_skip") |
                   .type, " ",
                   (.src     // ""), " ",
                   (.dst_dir // ""), " ",
                   (.keeper  // ""), " ",
                   (.decision // ""), " "')
  emit_run_end --status succeeded --total "$total" --applied "$moved" --skipped "$skipped"
```

Notes:
- `jq -rj` (lowercase j) suppresses jq's automatic record separator so we control framing.
- `" "` writes a literal NUL byte; jq emits exactly the literal byte without further encoding.
- NUL is the only POSIX-illegal byte in a filename, so it is unambiguously a field separator.
- The 5-chained `read -r -d ''` with `&&` keeps records aligned: a missing trailing field (which jq won't produce given the constant 5-field shape) would short-circuit the while-loop and leave one partial record unprocessed. Acceptable.

- [ ] **Step 4: Run the smoke to verify D1 now passes**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: 15/15 (12 existing + 3 new D1 assertions). All existing assertions still pass — the parser change is invisible to the happy-path tests.

- [ ] **Step 5: Run other smoke tests for no regression**

```bash
bash tests/events_contract.sh
bash tests/legacy_event_ts_seam.sh
bash tests/p1_thumb_phash_smoke.sh
go test ./ui/server/...
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add bin/twincut.sh tests/p1_stage9_smoke.sh
git commit -m "Stage 9.5 T2: NUL-delim apply parser (P1 #5)"
```

---

## Task 3: Empty-stdin short-circuit + pre-flight JSON validation (Chunk B-2)

Adds two upfront safety checks at the top of `process_apply_list_jsonin`: empty stdin is a no-op success; non-JSON stdin emits `apply_failed` and returns non-zero. Subsumes smoke gaps D2 and D3.

**Files:**
- Modify: `bin/twincut.sh` (top of `process_apply_list_jsonin`, after the jq/python3 guards)
- Modify: `tests/p1_stage9_smoke.sh` (add D2 and D3 assertion blocks)

- [ ] **Step 1: Write failing smoke assertions (D2 + D3)**

Append to `tests/p1_stage9_smoke.sh` just before the summary line (after the D1 block):

```bash
# === D2. zero-command apply — empty stdin is a no-op success ===
ZERO_SRC="$TMP/srcD2"; mkdir -p "$ZERO_SRC"
ZERO_NDJSON="$TMP/apply_zero_result.ndjson"
printf '' \
  | "$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$ZERO_SRC" \
    >"$ZERO_NDJSON" 2>/dev/null || true

assert "D2: zero-command apply emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$ZERO_NDJSON") -eq 0 ]]'

assert "D2: zero-command apply emits no error events" \
  '[[ $(grep -c "\"type\":\"error\"" "$ZERO_NDJSON") -eq 0 ]]'

# === D3. malformed JSON stdin — pre-flight rejects with apply_failed ===
BAD_SRC="$TMP/srcD3"; mkdir -p "$BAD_SRC"
BAD_NDJSON="$TMP/apply_malformed_result.ndjson"
printf 'this is definitely not json\n' \
  | "$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$BAD_SRC" \
    >"$BAD_NDJSON" 2>/dev/null || true

assert "D3: malformed JSON emits apply_failed error" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$BAD_NDJSON"'

assert "D3: malformed JSON emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$BAD_NDJSON") -eq 0 ]]'
```

- [ ] **Step 2: Run smoke to verify D2 + D3 fail**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected before fix:
- D2 (both assertions): PASS. Pre-T3, empty stdin produces an empty jq output → empty while loop → the unconditional `emit_run_end` at the bottom of `process_apply_list_jsonin` fires with zero counts. No action, no error events.
- D3 "no action events": PASS. jq exits with a parse error to stderr (the smoke discards it via `2>/dev/null`), the while loop sees no input, no actions emitted.
- D3 "apply_failed": FAIL. The jq parse error is silently swallowed — no `error` event reaches stdout.

Total: 18 pass + 1 fail (out of 19 assertions). D3's `apply_failed` is the TDD-driven failure that motivates the fix.

- [ ] **Step 3: Add the short-circuit + pre-flight in `bin/twincut.sh::process_apply_list_jsonin`**

Use Edit. Find this exact block (the python3 guard + the start of the local declarations):

```bash
  if ! command -v python3 >/dev/null 2>&1; then
    emit_error --code usage_error --detail "python3 required for path validation in --json-in mode"
    die "python3 required for path validation in --json-in mode"
  fi
  local total=0 moved=0 skipped=0
```

Replace with:

```bash
  if ! command -v python3 >/dev/null 2>&1; then
    emit_error --code usage_error --detail "python3 required for path validation in --json-in mode"
    die "python3 required for path validation in --json-in mode"
  fi

  # Buffer stdin once so we can pre-validate without losing the stream.
  # Apply lists for thumbnail_detect are bounded (typically <1MB).
  local stdin_input
  stdin_input=$(cat)

  # Zero-command apply is a no-op success (smoke gap D2).
  if [[ -z "$stdin_input" ]]; then
    emit_run_end --status succeeded --total 0 --applied 0 --skipped 0
    return 0
  fi

  # Pre-flight: every input line must be valid JSON (smoke gap D3).
  if ! printf '%s' "$stdin_input" | jq -e -c '.' >/dev/null 2>&1; then
    emit_error --code apply_failed \
      --detail "malformed apply input (not valid JSON)"
    return 1
  fi

  local total=0 moved=0 skipped=0
```

- [ ] **Step 4: Wire the buffered input into the existing jq invocation**

Use Edit on the `done < <(jq -rj ...)` line introduced in Task 2. Find:

```bash
  done < <(jq -rj 'select(.type == "apply_move" or .type == "apply_skip") |
                   .type, " ",
                   (.src     // ""), " ",
                   (.dst_dir // ""), " ",
                   (.keeper  // ""), " ",
                   (.decision // ""), " "')
```

Replace with:

```bash
  done < <(printf '%s' "$stdin_input" | jq -rj '
    select(.type == "apply_move" or .type == "apply_skip") |
      .type, " ",
      (.src     // ""), " ",
      (.dst_dir // ""), " ",
      (.keeper  // ""), " ",
      (.decision // ""), " "
  ')
```

- [ ] **Step 5: Run smoke to verify D2 + D3 pass**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: 19/19 (12 existing + 3 D1 + 2 D2 + 2 D3).

Note on D2: D2's two assertions ("no action events" / "no error events") already passed before the fix — the pre-T3 code path also handled empty stdin cleanly via the unconditional `emit_run_end` at the bottom of `process_apply_list_jsonin`. The short-circuit doesn't change observable behavior on D2; it makes the path explicit (faster, more readable, and locks the behavior so a future refactor can't break it). D3 is the TDD-driven assertion in this task.

- [ ] **Step 6: Run other test suites for no regression**

```bash
bash tests/events_contract.sh
bash tests/legacy_event_ts_seam.sh
bash tests/p1_thumb_phash_smoke.sh
go test ./ui/server/...
```

Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add bin/twincut.sh tests/p1_stage9_smoke.sh
git commit -m "Stage 9.5 T3: empty-stdin short-circuit + pre-flight JSON validation"
```

---

## Task 4: `_validate_decision` helper + apply_skip parity (Chunk C)

Extracts the 5-value decision allowlist into a `_validate_decision` helper, replaces the inline `case` block in the `apply_move` branch with a call to it, and adds a matching call to the `apply_skip` branch (closing the parity gap). D5 smoke proves apply_skip now rejects `decision="haha_bogus"`.

**Files:**
- Modify: `bin/twincut.sh` (add `_validate_decision`, edit both branches in `process_apply_list_jsonin`)
- Modify: `tests/p1_stage9_smoke.sh` (add D5 assertion block)

- [ ] **Step 1: Write the failing smoke assertion (D5)**

Append to `tests/p1_stage9_smoke.sh` just before the summary line (after the D3 block):

```bash
# === D5. apply_skip with bogus decision — must be rejected ===
SKIP_SRC="$TMP/srcD5"; mkdir -p "$SKIP_SRC"
cp "$SRC/unrelated_big.jpg" "$SKIP_SRC/keep_me.jpg"

APPLY_SKIP_BAD="$TMP/apply_skip_bad.ndjson"
cat > "$APPLY_SKIP_BAD" <<EOF
{"type":"apply_skip","src":"$SKIP_SRC/keep_me.jpg","decision":"haha_bogus"}
EOF

SKIP_BAD_NDJSON="$TMP/apply_skip_bad_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SKIP_SRC" \
  >"$SKIP_BAD_NDJSON" 2>/dev/null < "$APPLY_SKIP_BAD" || true

assert "D5: apply_skip bogus decision emits apply_failed error" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\".*\"detail\":\"unknown decision" "$SKIP_BAD_NDJSON"'

assert "D5: apply_skip bogus decision emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$SKIP_BAD_NDJSON") -eq 0 ]]'

assert "D5: apply_skip bogus decision: source file untouched" \
  '[[ -e "$SKIP_SRC/keep_me.jpg" ]]'
```

- [ ] **Step 2: Run smoke to verify D5 fails**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: D5's `apply_failed` and `no action events` assertions FAIL — current apply_skip branch dispatches straight to `emit_action_skip` (which writes `"type":"action","kind":"skip"`) without decision validation. The `source file untouched` assertion passes (skip never moves the file). Total: 20 pass + 2 fail (out of 22 assertions).

- [ ] **Step 3: Add the `_validate_decision` helper to `bin/twincut.sh`**

Use Edit. Find this exact block (the docstring + opening of `process_apply_list_jsonin`):

```bash
# process_apply_list_jsonin — apply mode via --json-in.
# Reads ApplyCommand JSON-lines from stdin via jq, validates each command,
# and dispatches to qmove. Emits emit_run_end when done.
# Allowed ApplyCommand types: apply_move, apply_skip.
# Allowed decisions for apply_move: thumb_l1_review, thumb_l2_exif,
#   thumb_l3_embed, thumb_confirmed, keep_user_override.
process_apply_list_jsonin(){
```

Replace with:

```bash
# _validate_decision DECISION SRC
#   Returns 0 if DECISION is in the canonical 5-value thumbnail allowlist.
#   Emits apply_failed and returns 1 otherwise.
_validate_decision(){
  local decision="$1" src="$2"
  case "$decision" in
    thumb_l1_review|thumb_l2_exif|thumb_l3_embed|thumb_confirmed|keep_user_override)
      return 0 ;;
    *)
      emit_error --code apply_failed --path "$src" \
        --detail "unknown decision '$decision'"
      return 1 ;;
  esac
}

# process_apply_list_jsonin — apply mode via --json-in.
# Reads ApplyCommand JSON-lines from stdin via jq, validates each command,
# and dispatches to qmove. Emits emit_run_end when done.
# Allowed ApplyCommand types: apply_move, apply_skip.
# Allowed decisions (both branches): thumb_l1_review, thumb_l2_exif,
#   thumb_l3_embed, thumb_confirmed, keep_user_override.
process_apply_list_jsonin(){
```

- [ ] **Step 4: Replace the inline allowlist check in the `apply_move` branch**

Use Edit. Find this exact block (the inline `case "$decision"` inside the apply_move branch):

```bash
        case "$decision" in
          thumb_l1_review|thumb_l2_exif|thumb_l3_embed|thumb_confirmed|keep_user_override) ;;
          *)
            emit_error --code apply_failed --path "$src" \
              --detail "unknown decision '$decision'"
            skipped=$((skipped+1)); continue ;;
        esac
```

Replace with:

```bash
        _validate_decision "$decision" "$src" || { skipped=$((skipped+1)); continue; }
```

- [ ] **Step 5: Add the decision check to the `apply_skip` branch**

Use Edit. Find this exact block (the apply_skip branch as currently shipped):

```bash
      apply_skip)
        if ! _is_under "$abs_src" "$src_root"; then
          emit_error --code apply_failed --path "$src" \
            --detail "src not under \$SOURCE_DIR ($src_root)"
          skipped=$((skipped+1)); continue
        fi
        emit_action_skip --src "$src" --decision "$decision" --reason user_override
        skipped=$((skipped+1))
        ;;
```

Replace with:

```bash
      apply_skip)
        if ! _is_under "$abs_src" "$src_root"; then
          emit_error --code apply_failed --path "$src" \
            --detail "src not under \$SOURCE_DIR ($src_root)"
          skipped=$((skipped+1)); continue
        fi
        _validate_decision "$decision" "$src" || { skipped=$((skipped+1)); continue; }
        emit_action_skip --src "$src" --decision "$decision" --reason user_override
        skipped=$((skipped+1))
        ;;
```

- [ ] **Step 6: Run smoke to verify D5 passes**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: 22/22 (12 existing + 3 D1 + 2 D2 + 2 D3 + 3 D5).

The existing "bad-decision emitted error event" assertion (the original case-5 block in the smoke, lines 100-104 in the pre-Stage-9.5 file) still passes because `_validate_decision` emits the same `apply_failed` error with the same `unknown decision` detail.

- [ ] **Step 7: Run other test suites for no regression**

```bash
bash tests/events_contract.sh
bash tests/legacy_event_ts_seam.sh
bash tests/p1_thumb_phash_smoke.sh
go test ./ui/server/...
```

Expected: all green.

- [ ] **Step 8: Commit**

```bash
git add bin/twincut.sh tests/p1_stage9_smoke.sh
git commit -m "Stage 9.5 T4: _validate_decision helper + apply_skip parity"
```

---

## Task 5: Missing-src warn coverage (Chunk D smoke gap D4)

Adds a smoke assertion that the existing `emit_warn --code missing_file` path at `bin/twincut.sh:422-426` fires when an apply_move references a path that resolves under `$SOURCE_DIR` but doesn't exist on disk. No production code change — this is pure test coverage of a behavior that has shipped since Stage 9.

**Files:**
- Modify: `tests/p1_stage9_smoke.sh` (add D4 assertion block)

- [ ] **Step 1: Write the assertion (it should pass on first run since the prod code already handles this)**

Append to `tests/p1_stage9_smoke.sh` just before the summary line (after the D5 block):

```bash
# === D4. missing src under SOURCE_DIR — emit_warn missing_file path ===
MISS_SRC="$TMP/srcD4"; mkdir -p "$MISS_SRC"
MISS_QUAR="$MISS_SRC/_QUARANTINE/_thumbs"

APPLY_MISS="$TMP/apply_miss.ndjson"
cat > "$APPLY_MISS" <<EOF
{"type":"apply_move","src":"$MISS_SRC/never_existed.jpg","dst_dir":"$MISS_QUAR","keeper":"","decision":"thumb_l2_exif"}
EOF

MISS_NDJSON="$TMP/apply_miss_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$MISS_SRC" \
  >"$MISS_NDJSON" 2>/dev/null < "$APPLY_MISS" || true

assert "D4: missing-src emits warn missing_file" \
  'grep -q "\"type\":\"warn\".*\"code\":\"missing_file\"" "$MISS_NDJSON"'

assert "D4: missing-src emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$MISS_NDJSON") -eq 0 ]]'

assert "D4: missing-src emits run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$MISS_NDJSON"'
```

- [ ] **Step 2: Run smoke to verify D4 passes immediately**

```bash
bash tests/p1_stage9_smoke.sh
```

Expected: 25/25 (22 from T4 + 3 D4). All assertions pass on first run — D4 is a coverage gap, not a behavior fix.

If `D4: missing-src emits warn missing_file` fails, the prod code path at `bin/twincut.sh:422-426` is broken; that would be a Task-5-discovered regression and needs a separate diagnostic before continuing. (Not expected — current Stage 9 code emits this warn.)

- [ ] **Step 3: Run other test suites for no regression**

```bash
bash tests/events_contract.sh
bash tests/legacy_event_ts_seam.sh
bash tests/p1_thumb_phash_smoke.sh
go test ./ui/server/...
```

Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add tests/p1_stage9_smoke.sh
git commit -m "Stage 9.5 T5: smoke coverage for missing-src warn path (D4)"
```

---

## Cumulative pre-PR sweep

After T5 is green, run the full suite once more to confirm the branch is shippable:

```bash
bash tests/legacy_event_ts_seam.sh        # new — should be green
bash tests/p1_stage9_smoke.sh              # 25/25 (was 12/12)
bash tests/events_contract.sh              # 14/14 unchanged
bash tests/p1_thumb_phash_smoke.sh         # 26/26 unchanged
go test ./ui/server/...                    # unchanged
```

Then dispatch `reviewer-codex` for the adversarial pre-PR review (this session's substitution for `reviewer-gemini` per session rule; Codex quota resets tomorrow). Address any P0/P1 findings before opening the PR. PR title: `Stage 9.5: hygiene follow-up`. PR body should list the 5 task commits and link the closed spec items.

## Out of scope (Stage 10 candidates)

Per the spec's §9, these remain for a later beat:
- Migrate the 7 retained `emit_event` callers to typed helpers (requires schema extension).
- `SpawnHook` test-mode guard + failure simulation.
- `progress` events during long apply streams.
- Restore-flow typed migration.
- Streaming (non-buffered) apply-list parser for hypothetical large applies.
