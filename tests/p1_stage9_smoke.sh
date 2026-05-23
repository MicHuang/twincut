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
# --json-events: twincut does exec 3>&1 1>&2 so NDJSON lands on original stdout.
PREVIEW_NDJSON="$TMP/preview.ndjson"
"$TWINCUT" --thumbnail-detect --source "$SRC" --json-events \
  >"$PREVIEW_NDJSON" 2>/dev/null || true

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
  >"$APPLY_NDJSON" 2>/dev/null < "$APPLY_INPUT" || true

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
  >"$APPLY_BAD_NDJSON" 2>/dev/null < "$APPLY_BAD" || true

assert "bad-decision emitted error event" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$APPLY_BAD_NDJSON"'

assert "bad-decision left source file intact" \
  '[[ -e "$SRC/unrelated_big.jpg" ]]'

# === 6. traversal attempt — dst_dir outside SOURCE_DIR must be rejected ===
ESCAPE_TARGET="$(mktemp -d)"
trap 'rm -rf "$TMP" "$ESCAPE_TARGET"' EXIT
APPLY_ESCAPE="$TMP/apply_escape.ndjson"
cat > "$APPLY_ESCAPE" <<EOF
{"type":"apply_move","src":"$SRC/unrelated_big.jpg","dst_dir":"$ESCAPE_TARGET","keeper":"","decision":"thumb_l2_exif"}
EOF
APPLY_ESCAPE_NDJSON="$TMP/apply_escape_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$SRC" \
  >"$APPLY_ESCAPE_NDJSON" 2>/dev/null < "$APPLY_ESCAPE" || true

assert "traversal attempt emitted apply_failed error" \
  'grep -q "\"type\":\"error\".*\"code\":\"apply_failed\"" "$APPLY_ESCAPE_NDJSON"'

assert "traversal attempt: source file still at original location" \
  '[[ -e "$SRC/unrelated_big.jpg" ]]'

assert "traversal attempt: escape target dir is empty" \
  '[[ -z "$(ls -A "$ESCAPE_TARGET" 2>/dev/null)" ]]'

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

echo "========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
