#!/usr/bin/env bash
# tests/p1_stage9_smoke.sh — Stage 9 end-to-end smoke.
#
# Sets up a tiny fixture image dir, runs a scan with --json-events,
# composes a synthetic ApplyCommand JSON-lines stream, pipes it to
# --thumbnail-detect-apply --json-in --json-events, and asserts the
# resulting events.ndjson + filesystem state.
#
# Exit-code contract (asserted per invocation below): apply mode exits 0
# even when individual records fail — per-record errors go to the event
# channel (error/warn events, record counted as skipped) and the run ends
# run_end status=succeeded. Only an apply-flow pre-flight failure
# (malformed JSON stdin) exits 1 with run_end status=failed. (Usage
# errors elsewhere in the CLI die with other codes — not covered here.)

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

# === F-H7A. guarded apply_move records must not create destination dirs ===
# These apply-only contracts intentionally run before the Pillow gate below.
GUARD_SRC="$TMP/srcF_H7_guards"; mkdir -p "$GUARD_SRC"
printf 'excluded' > "$GUARD_SRC/excluded.jpg"
printf 'hardlink' > "$GUARD_SRC/hardlink.jpg"
printf 'move' > "$GUARD_SRC/move.jpg"
printf 'mkdir-fail' > "$GUARD_SRC/mkdir-fail.jpg"
ln "$GUARD_SRC/hardlink.jpg" "$GUARD_SRC/hardlink-keeper.jpg"
GUARD_EXCLUDED_DIR="$GUARD_SRC/should_not_exist_excluded"
GUARD_HARDLINK_DIR="$GUARD_SRC/should_not_exist_hardlink"
GUARD_MOVED_DIR="$GUARD_SRC/created_for_real_move"
GUARD_MKDIR_BLOCKER="$GUARD_SRC/not_a_directory"; printf 'blocker' > "$GUARD_MKDIR_BLOCKER"
GUARD_INPUT="$TMP/apply_guarded.ndjson"
cat > "$GUARD_INPUT" <<EOF
{"type":"apply_move","src":"$GUARD_SRC/excluded.jpg","dst_dir":"$GUARD_EXCLUDED_DIR","keeper":"","decision":"thumb_l2_exif"}
{"type":"apply_move","src":"$GUARD_SRC/hardlink.jpg","dst_dir":"$GUARD_HARDLINK_DIR","keeper":"$GUARD_SRC/hardlink-keeper.jpg","decision":"thumb_l2_exif"}
{"type":"apply_move","src":"$GUARD_SRC/move.jpg","dst_dir":"$GUARD_MOVED_DIR","keeper":"","decision":"thumb_l2_exif"}
{"type":"apply_move","src":"$GUARD_SRC/mkdir-fail.jpg","dst_dir":"$GUARD_MKDIR_BLOCKER","keeper":"","decision":"thumb_l2_exif"}
EOF
GUARD_NDJSON="$TMP/apply_guarded_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$GUARD_SRC" \
  --exclude-path "$GUARD_SRC/excluded.jpg" \
  >"$GUARD_NDJSON" 2>/dev/null < "$GUARD_INPUT" || rc=$?

assert "F-H7A: guarded apply exited 0" "[[ $rc -eq 0 ]]"
assert "F-H7A: excluded source remains" '[[ -e "$GUARD_SRC/excluded.jpg" ]]'
assert "F-H7A: hardlink source remains" '[[ -e "$GUARD_SRC/hardlink.jpg" ]]'
assert "F-H7A: excluded skip creates no destination dir" '[[ ! -e "$GUARD_EXCLUDED_DIR" ]]'
assert "F-H7A: hardlink skip creates no destination dir" '[[ ! -e "$GUARD_HARDLINK_DIR" ]]'
assert "F-H7A: real move removes its source" '[[ ! -e "$GUARD_SRC/move.jpg" ]]'
assert "F-H7A: real move creates its destination file" '[[ -e "$GUARD_MOVED_DIR/move.jpg" ]]'
assert "F-H7A: mkdir failure leaves its source" '[[ -e "$GUARD_SRC/mkdir-fail.jpg" ]]'
assert "F-H7A: excluded and hardlink skips are both emitted" \
  'grep -q "\"reason\":\"excluded\"" "$GUARD_NDJSON" && grep -q "\"reason\":\"hardlink\"" "$GUARD_NDJSON"'
assert "F-H7A: qmove owns mkdir failure warning" \
  'grep -q "\"type\":\"warn\".*\"code\":\"io_error\".*\"path\":\"$GUARD_MKDIR_BLOCKER\".*\"detail\":\"mkdir failed\"" "$GUARD_NDJSON"'
assert "F-H7A: guarded apply ends run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\".*\"applied\":1.*\"skipped\":3" "$GUARD_NDJSON"'
assert "F-H7A: strict subshell emits exactly one run_end" \
  '[[ $(grep -c "\"type\":\"run_end\"" "$GUARD_NDJSON") -eq 1 ]]'

# === F-H7B / D3. malformed JSON propagates the pre-flight exit status ===
BAD_SRC="$TMP/srcD3"; mkdir -p "$BAD_SRC"
BAD_NDJSON="$TMP/apply_malformed_result.ndjson"
rc=0
printf 'this is definitely not json\n' \
  | "$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$BAD_SRC" \
    >"$BAD_NDJSON" 2>/dev/null || rc=$?

assert "D3: malformed JSON exited 1 (pre-flight failure IS the exit code)" \
  "[[ $rc -eq 1 ]]"
assert "D3: malformed JSON emits apply_failed error" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$BAD_NDJSON"'
assert "D3: malformed JSON emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$BAD_NDJSON") -eq 0 ]]'
assert "D3: malformed JSON emits run_end status=failed" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"failed\"" "$BAD_NDJSON"'
assert "D3: strict subshell emits exactly one run_end" \
  '[[ $(grep -c "\"type\":\"run_end\"" "$BAD_NDJSON") -eq 1 ]]'

# Use Python to build gradient PNGs if Pillow is available; else skip.
if ! python3 -c 'import PIL' 2>/dev/null; then
  echo "[skip] Pillow not installed — Stage 9 detection sections skipped"
  echo "========================================="
  echo "PASS=$PASS FAIL=$FAIL (apply-only sections)"
  exit $(( FAIL > 0 ? 1 : 0 ))
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
# --json-events: twincut does exec 3>&1 1>&2 so NDJSON lands on original stdout.
PREVIEW_NDJSON="$TMP/preview.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect --source "$SRC" --json-events \
  >"$PREVIEW_NDJSON" 2>/dev/null || rc=$?

assert "preview scan exited 0" "[[ $rc -eq 0 ]]"

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
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SRC" \
  >"$APPLY_NDJSON" 2>/dev/null < "$APPLY_INPUT" || rc=$?

assert "apply exited 0" "[[ $rc -eq 0 ]]"

assert "apply emitted one action kind=move" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_NDJSON") -eq 1 ]]'

assert "apply quarantine file exists" \
  '[[ -e "$QUAR_DIR/suspect_small.jpg" ]]'

assert "apply source file removed" \
  '[[ ! -e "$SRC/suspect_small.jpg" ]]'

assert "apply emitted run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_NDJSON"'

assert "apply emitted progress phase=apply" \
  'grep -q "\"type\":\"progress\".*\"phase\":\"apply\"" "$APPLY_NDJSON"'

# === 5. unknown decision triggers error event, no crash ===
APPLY_BAD="$TMP/apply_bad.ndjson"
cat > "$APPLY_BAD" <<EOF
{"type":"apply_move","src":"$SRC/unrelated_big.jpg","dst_dir":"$QUAR_DIR","keeper":"","decision":"NOT_A_VALID_DECISION"}
EOF
APPLY_BAD_NDJSON="$TMP/apply_bad_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SRC" \
  >"$APPLY_BAD_NDJSON" 2>/dev/null < "$APPLY_BAD" || rc=$?

assert "bad-decision exited 0 (per-record failure is event-channel, not exit code)" \
  "[[ $rc -eq 0 ]]"

assert "bad-decision emitted error event" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$APPLY_BAD_NDJSON"'

assert "bad-decision left source file intact" \
  '[[ -e "$SRC/unrelated_big.jpg" ]]'

assert "bad-decision run ends run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_BAD_NDJSON"'

# === 6. traversal attempt — dst_dir outside SOURCE_DIR must be rejected ===
ESCAPE_TARGET="$(mktemp -d)"
trap 'rm -rf "$TMP" "$ESCAPE_TARGET"' EXIT
APPLY_ESCAPE="$TMP/apply_escape.ndjson"
cat > "$APPLY_ESCAPE" <<EOF
{"type":"apply_move","src":"$SRC/unrelated_big.jpg","dst_dir":"$ESCAPE_TARGET","keeper":"","decision":"thumb_l2_exif"}
EOF
APPLY_ESCAPE_NDJSON="$TMP/apply_escape_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SRC" \
  >"$APPLY_ESCAPE_NDJSON" 2>/dev/null < "$APPLY_ESCAPE" || rc=$?

assert "traversal attempt exited 0 (rejected record is event-channel)" \
  "[[ $rc -eq 0 ]]"

assert "traversal attempt emitted apply_failed error" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$APPLY_ESCAPE_NDJSON"'

assert "traversal attempt: source file still at original location" \
  '[[ -e "$SRC/unrelated_big.jpg" ]]'

assert "traversal attempt: escape target dir is empty" \
  '[[ -z "$(ls -A "$ESCAPE_TARGET" 2>/dev/null)" ]]'

assert "traversal attempt run ends run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_ESCAPE_NDJSON"'

# === D1. tab-in-path — NUL-delim parser must preserve literal \t; the
# qmove TSV guard (hygiene Step 7) must then safely refuse to move it
# (a raw tab in the manifest TSV would corrupt --restore for that row). ===
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
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$TAB_SRC" \
  >"$APPLY_TAB_NDJSON" 2>/dev/null < "$APPLY_TAB" || rc=$?

assert "D1: tab-in-path apply exited 0" "[[ $rc -eq 0 ]]"

assert "D1: tab-in-path apply emits no action kind=move (TSV guard blocks it)" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_TAB_NDJSON") -eq 0 ]]'

assert "D1: tab-in-path apply emits warn io_error (path correctly parsed with literal tab)" \
  '[[ $(grep -c "\"type\":\"warn\".*\"code\":\"io_error\"" "$APPLY_TAB_NDJSON") -eq 1 ]]'

assert "D1: tab-in-path: quarantine file with tab in name does not exist" \
  '[[ ! -e "$TAB_QUAR/$TAB_NAME.jpg" ]]'

assert "D1: tab-in-path: original source file left untouched" \
  '[[ -e "$TAB_SRC/$TAB_NAME.jpg" ]]'

assert "D1: tab-in-path apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_TAB_NDJSON"'

# === D1b. newline-in-path — @base64 parser must preserve literal \n; same
# qmove TSV guard must safely refuse to move it. ===
NL_NAME="$(printf 'line1\nline2')"
NL_SRC="$TMP/srcD1b"; mkdir -p "$NL_SRC"
cp "$SRC/keeper.jpg" "$NL_SRC/keeper.jpg"
cp "$SRC/unrelated_big.jpg" "$NL_SRC/$NL_NAME.jpg"
NL_QUAR="$NL_SRC/_QUARANTINE/_thumbs"

APPLY_NL="$TMP/apply_nl.ndjson"
jq -cn --arg src "$NL_SRC/$NL_NAME.jpg" \
      --arg dst "$NL_QUAR" \
      --arg keep "$NL_SRC/keeper.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  > "$APPLY_NL"

APPLY_NL_NDJSON="$TMP/apply_nl_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$NL_SRC" \
  >"$APPLY_NL_NDJSON" 2>/dev/null < "$APPLY_NL" || rc=$?

assert "D1b: newline-in-path apply exited 0" "[[ $rc -eq 0 ]]"

assert "D1b: newline-in-path apply emits no action kind=move (TSV guard blocks it)" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_NL_NDJSON") -eq 0 ]]'

assert "D1b: newline-in-path apply emits warn io_error (path correctly parsed with literal newline)" \
  '[[ $(grep -c "\"type\":\"warn\".*\"code\":\"io_error\"" "$APPLY_NL_NDJSON") -eq 1 ]]'

assert "D1b: newline-in-path: quarantine file does not exist" \
  '[[ ! -e "$NL_QUAR/$NL_NAME.jpg" ]]'

assert "D1b: newline-in-path: original source file left untouched" \
  '[[ -e "$NL_SRC/$NL_NAME.jpg" ]]'

assert "D1b: newline-in-path apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_NL_NDJSON"'

# === D1c. tab-in-KEEPER — the matched path is written into the manifest
# TSV `matched` column; the guard must refuse the move when the keeper
# (not just the src) cannot be represented in the TSV. ===
KTAB_NAME="$(printf 'keep%bafter' '\t')"
KTAB_SRC="$TMP/srcD1c"; mkdir -p "$KTAB_SRC"
cp "$SRC/keeper.jpg" "$KTAB_SRC/$KTAB_NAME.jpg"
cp "$SRC/unrelated_big.jpg" "$KTAB_SRC/victim.jpg"
KTAB_QUAR="$KTAB_SRC/_QUARANTINE/_thumbs"

APPLY_KTAB="$TMP/apply_ktab.ndjson"
jq -cn --arg src "$KTAB_SRC/victim.jpg" \
      --arg dst "$KTAB_QUAR" \
      --arg keep "$KTAB_SRC/$KTAB_NAME.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  > "$APPLY_KTAB"

APPLY_KTAB_NDJSON="$TMP/apply_ktab_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$KTAB_SRC" \
  >"$APPLY_KTAB_NDJSON" 2>/dev/null < "$APPLY_KTAB" || rc=$?

assert "D1c: tab-in-keeper apply exited 0" "[[ $rc -eq 0 ]]"

assert "D1c: tab-in-keeper apply emits no action kind=move (TSV guard blocks it)" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_KTAB_NDJSON") -eq 0 ]]'

assert "D1c: tab-in-keeper apply emits warn io_error" \
  '[[ $(grep -c "\"type\":\"warn\".*\"code\":\"io_error\"" "$APPLY_KTAB_NDJSON") -eq 1 ]]'

assert "D1c: tab-in-keeper: victim file left untouched" \
  '[[ -e "$KTAB_SRC/victim.jpg" ]]'

assert "D1c: tab-in-keeper apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_KTAB_NDJSON"'

# === D1d. CR-in-path — carriage returns are invisible in logs and break
# path round-trips through line-oriented consumers; guard must refuse. ===
CR_NAME="$(printf 'cr%bafter' '\r')"
CR_SRC="$TMP/srcD1d"; mkdir -p "$CR_SRC"
cp "$SRC/keeper.jpg" "$CR_SRC/keeper.jpg"
cp "$SRC/unrelated_big.jpg" "$CR_SRC/$CR_NAME.jpg"
CR_QUAR="$CR_SRC/_QUARANTINE/_thumbs"

APPLY_CR="$TMP/apply_cr.ndjson"
jq -cn --arg src "$CR_SRC/$CR_NAME.jpg" \
      --arg dst "$CR_QUAR" \
      --arg keep "$CR_SRC/keeper.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  > "$APPLY_CR"

APPLY_CR_NDJSON="$TMP/apply_cr_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$CR_SRC" \
  >"$APPLY_CR_NDJSON" 2>/dev/null < "$APPLY_CR" || rc=$?

assert "D1d: CR-in-path apply exited 0" "[[ $rc -eq 0 ]]"

assert "D1d: CR-in-path apply emits no action kind=move (TSV guard blocks it)" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_CR_NDJSON") -eq 0 ]]'

assert "D1d: CR-in-path apply emits warn io_error" \
  '[[ $(grep -c "\"type\":\"warn\".*\"code\":\"io_error\"" "$APPLY_CR_NDJSON") -eq 1 ]]'

assert "D1d: CR-in-path: original source file left untouched" \
  '[[ -e "$CR_SRC/$CR_NAME.jpg" ]]'

assert "D1d: CR-in-path apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_CR_NDJSON"'

# === D2. zero-command apply — empty stdin is a no-op success ===
ZERO_SRC="$TMP/srcD2"; mkdir -p "$ZERO_SRC"
ZERO_NDJSON="$TMP/apply_zero_result.ndjson"
rc=0
printf '' \
  | "$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$ZERO_SRC" \
    >"$ZERO_NDJSON" 2>/dev/null || rc=$?

assert "D2: zero-command apply exited 0 (no-op success)" "[[ $rc -eq 0 ]]"

assert "D2: zero-command apply emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$ZERO_NDJSON") -eq 0 ]]'

assert "D2: zero-command apply emits no error events" \
  '[[ $(grep -c "\"type\":\"error\"" "$ZERO_NDJSON") -eq 0 ]]'

assert "D2: zero-command apply ends run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$ZERO_NDJSON"'

# === D5. apply_skip with bogus decision — must be rejected ===
SKIP_SRC="$TMP/srcD5"; mkdir -p "$SKIP_SRC"
cp "$SRC/unrelated_big.jpg" "$SKIP_SRC/keep_me.jpg"

APPLY_SKIP_BAD="$TMP/apply_skip_bad.ndjson"
cat > "$APPLY_SKIP_BAD" <<EOF
{"type":"apply_skip","src":"$SKIP_SRC/keep_me.jpg","decision":"haha_bogus"}
EOF

SKIP_BAD_NDJSON="$TMP/apply_skip_bad_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SKIP_SRC" \
  >"$SKIP_BAD_NDJSON" 2>/dev/null < "$APPLY_SKIP_BAD" || rc=$?

assert "D5: apply_skip bogus decision exited 0" "[[ $rc -eq 0 ]]"

assert "D5: apply_skip bogus decision emits apply_failed error" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\".*\"detail\":\"unknown decision" "$SKIP_BAD_NDJSON"'

assert "D5: apply_skip bogus decision emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$SKIP_BAD_NDJSON") -eq 0 ]]'

assert "D5: apply_skip bogus decision: source file untouched" \
  '[[ -e "$SKIP_SRC/keep_me.jpg" ]]'

assert "D5: apply_skip bogus decision run ends run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$SKIP_BAD_NDJSON"'

# === D4. missing src under SOURCE_DIR — emit_warn missing_file path ===
MISS_SRC="$TMP/srcD4"; mkdir -p "$MISS_SRC"
MISS_QUAR="$MISS_SRC/_QUARANTINE/_thumbs"

APPLY_MISS="$TMP/apply_miss.ndjson"
cat > "$APPLY_MISS" <<EOF
{"type":"apply_move","src":"$MISS_SRC/never_existed.jpg","dst_dir":"$MISS_QUAR","keeper":"","decision":"thumb_l2_exif"}
EOF

MISS_NDJSON="$TMP/apply_miss_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$MISS_SRC" \
  >"$MISS_NDJSON" 2>/dev/null < "$APPLY_MISS" || rc=$?

assert "D4: missing-src exited 0" "[[ $rc -eq 0 ]]"

assert "D4: missing-src emits warn missing_file" \
  'grep -q "\"type\":\"warn\".*\"code\":\"missing_file\"" "$MISS_NDJSON"'

assert "D4: missing-src emits no action events" \
  '[[ $(grep -c "\"type\":\"action\"" "$MISS_NDJSON") -eq 0 ]]'

assert "D4: missing-src emits run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$MISS_NDJSON"'

# === E2E. detect→review→apply seam ===
# Earlier sections prove (a) detection emits thumb_candidate and (b) a
# hand-written ApplyCommand applies — but nothing proved the detector's OWN
# output round-trips through the typed apply contract. Here we derive the
# ApplyCommand straight from a real thumb_candidate event (mirroring the Go
# UI's composeApplyCommands: type, src, dst_dir, keeper, decision), pipe it
# back through --json-in, and verify the on-disk move. Closes the seam that
# the three-way detect/phash/apply test split otherwise left uncovered.
E2E_SRC="$TMP/srcE2E"; mkdir -p "$E2E_SRC"
python3 - <<PY
from PIL import Image
import os
src = "$E2E_SRC"
def grad(name, size):
    # Paint a small gradient then C-resize to target (fast; avoids a
    # multi-million-iteration putpixel loop on large fixtures).
    small = Image.new("RGB", (64, 64))
    for x in range(64):
        for y in range(64):
            small.putpixel((x, y), ((x*4) % 256, (y*4) % 256, (x*y) % 256))
    small.resize(size).save(os.path.join(src, name))
grad("e2e_keeper.jpg", (1600, 1600))  # >1024 long edge, >0.5MP → not a candidate
grad("e2e_thumb.jpg", (320, 240))     # small dims → L1 thumbnail candidate
PY

E2E_PREVIEW="$TMP/e2e_preview.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect --source "$E2E_SRC" --json-events \
  >"$E2E_PREVIEW" 2>/dev/null || rc=$?

assert "E2E: preview scan exited 0" "[[ $rc -eq 0 ]]"

# The real candidate path the detector reported (verify the move against it).
# shellcheck disable=SC2034  # read via eval inside assert()'s quoted condition strings below
E2E_CAND="$(jq -rs 'map(select(.type=="thumb_candidate")) | (.[0].path // "")' "$E2E_PREVIEW")"
assert "E2E: detector reported a thumb_candidate path on disk" \
  '[[ -n "$E2E_CAND" && -e "$E2E_CAND" ]]'

# Derive the ApplyCommand from that event (slurp + take first; no head/SIGPIPE
# under pipefail). dst_dir is the UI-chosen quarantine subdir.
E2E_QUAR="$E2E_SRC/_QUARANTINE/_thumbs"
E2E_APPLY="$TMP/e2e_apply.ndjson"
jq -cs --arg quar "$E2E_QUAR" '
  (map(select(.type=="thumb_candidate")) | .[0]) as $c
  | if $c == null then empty
    else {type:"apply_move", src:$c.path, dst_dir:$quar, keeper:($c.keeper // ""), decision:$c.decision}
    end
' "$E2E_PREVIEW" > "$E2E_APPLY"

assert "E2E: derived exactly one apply_move from detector output" \
  '[[ $(grep -c "\"type\":\"apply_move\"" "$E2E_APPLY") -eq 1 ]]'

E2E_RESULT="$TMP/e2e_result.ndjson"
rc=0
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$E2E_SRC" \
  >"$E2E_RESULT" 2>/dev/null < "$E2E_APPLY" || rc=$?

assert "E2E: detector-derived apply exited 0" "[[ $rc -eq 0 ]]"

assert "E2E: detector-derived apply emitted action kind=move" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$E2E_RESULT") -eq 1 ]]'

assert "E2E: detector-derived candidate left the source tree" \
  '[[ ! -e "$E2E_CAND" ]]'

assert "E2E: detector-derived candidate now in quarantine" \
  '[[ -e "$E2E_QUAR/$(basename "$E2E_CAND")" ]]'

assert "E2E: apply emitted run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$E2E_RESULT"'

echo "========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
