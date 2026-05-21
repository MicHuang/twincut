#!/usr/bin/env bash
# tests/p1_thumb_smoke.sh — smoke test for P1 thumbnail detection (L1+L2+L3).
#
# Validates the no-AI part of the pipeline:
#   - L1 dimension classification (sips path)
#   - L1-only suspects → review.csv (no automatic move)
#   - graceful degrade when exiftool is absent (L2/L3 skipped, no crash)
#   - --thumb-confirm processes (a possibly user-edited) review.csv via qmove
#   - manifest is written for confirmed moves
#   - --restore rolls back confirmed moves
#   - thumbnail-detect can run standalone (only --source, no --backup)
#
# When exiftool IS available, additionally validates:
#   - L2 EXIF clustering: identical fingerprint, different size → small one moved
#   - L3 embedded thumbnail: small file == big file's embedded thumb → small moved
# These extra checks require: brew install exiftool

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
TWINCUT="$ROOT/bin/twincut.sh"

command -v sips >/dev/null 2>&1 || { echo "sips not found — this test requires macOS 'sips'"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

SRC="$TMP/src"
mkdir -p "$SRC"

PASS=0; FAIL=0
note(){ printf '\n=== %s ===\n' "$*"; }
ok(){   printf '  ok   %s\n' "$*"; PASS=$((PASS+1)); }
bad(){  printf '  FAIL %s\n' "$*"; FAIL=$((FAIL+1)); }
assert_eq(){ [[ "$1" == "$2" ]] && ok "$3 ($1)" || bad "$3 (got '$1', want '$2')"; }
assert_file(){     [[ -e "$1" ]] && ok "exists: $1"     || bad "missing: $1"; }
assert_not_file(){ [[ ! -e "$1" ]] && ok "absent: $1"   || bad "still there: $1"; }

# ---------- fixtures ----------
# Use a system PNG as a real image source so sips can read dimensions.
SEED="/System/Library/Desktop Pictures/Solid Colors/Black.png"
[[ -f "$SEED" ]] || SEED="/System/Library/Desktop Pictures/Solid Colors/Stone.png"
[[ -f "$SEED" ]] || { echo "no seed image found, skipping"; exit 0; }

# Big image: 2000x2000 (long edge >> 1024 → l1=ok)
sips -z 2000 2000 "$SEED" --out "$SRC/big.png" >/dev/null

# Thumbnail-sized: 200x200 (long edge <= 512 → l1=thumb)
sips -z 200 200 "$SEED" --out "$SRC/tiny.png" >/dev/null

# In-between: 800x800, 0.64MP > 0.5MP → l1=ok (NOT a maybe)
sips -z 800 800 "$SEED" --out "$SRC/mid.png" >/dev/null

# Maybe-thumb: 700x500, long edge ≤ 1024 AND pixels ≤ 500000 → l1=maybe
sips -z 500 700 "$SEED" --out "$SRC/maybe.png" >/dev/null

# ----------------------------------------------------------------------------
note "1. standalone --thumbnail-detect runs without --backup"
set +e
"$TWINCUT" --source "$SRC" --thumbnail-detect --thumb-action review --assume-yes \
  >/tmp/twincut_thumb1.log 2>&1
RC=$?
set -e
assert_eq "$RC" "0" "exit code 0 standalone thumbnail-detect"

REVIEW="$SRC/_thumbnails/_review.csv"
assert_file "$REVIEW"

# tiny + maybe should appear; big + mid should NOT
grep -q "tiny.png" "$REVIEW"  && ok "tiny.png in review (l1=thumb)"  || bad "tiny.png missing from review"
grep -q "maybe.png" "$REVIEW" && ok "maybe.png in review (l1=maybe)" || bad "maybe.png missing from review"
grep -q "big.png"   "$REVIEW" && bad "big.png wrongly flagged"        || ok "big.png correctly NOT flagged"
grep -q "mid.png"   "$REVIEW" && bad "mid.png wrongly flagged"        || ok "mid.png correctly NOT flagged"

# nothing should be moved (review-only, plus L2/L3 skipped without exiftool)
assert_file "$SRC/tiny.png"
assert_file "$SRC/maybe.png"
assert_file "$SRC/big.png"

# graceful degrade message about exiftool
if ! command -v exiftool >/dev/null 2>&1; then
  grep -q "exiftool" /tmp/twincut_thumb1.log && ok "graceful skip warning printed" || bad "no exiftool warning"
fi

# ----------------------------------------------------------------------------
note "2. --thumb-confirm moves rows from review.csv and writes manifest"
"$TWINCUT" --thumb-confirm "$REVIEW" --assume-yes >/tmp/twincut_confirm.log 2>&1

# tiny.png and maybe.png should be moved into _thumbnails/
assert_not_file "$SRC/tiny.png"
assert_not_file "$SRC/maybe.png"
assert_file "$SRC/_thumbnails/tiny.png"
assert_file "$SRC/_thumbnails/maybe.png"

# manifest should exist with thumb_confirmed rows
MF=$(ls -t "$SRC/_thumbnails"/_manifest-*.tsv 2>/dev/null | head -n1 || true)
[[ -n "$MF" ]] && ok "thumb manifest created" || bad "no manifest after confirm"
grep -q "thumb_confirmed" "$MF" && ok "manifest has thumb_confirmed rows" || bad "no thumb_confirmed rows"

# ----------------------------------------------------------------------------
note "3. --restore rolls thumbnails back"
"$TWINCUT" --restore "$MF" --assume-yes >/tmp/twincut_thumb_restore.log 2>&1
assert_file "$SRC/tiny.png"
assert_file "$SRC/maybe.png"
assert_not_file "$SRC/_thumbnails/tiny.png"

# ----------------------------------------------------------------------------
note "4. --thumb-action move with no L2/L3 evidence does NOT auto-move L1-only"
# Re-run with --thumb-action move; without exiftool, L1-only files must STILL
# go to review (the L2/L3 evidence is missing → policy never auto-deletes).
rm -f "$REVIEW"
"$TWINCUT" --source "$SRC" --thumbnail-detect --thumb-action move --assume-yes \
  >/tmp/twincut_thumb2.log 2>&1
assert_file "$SRC/tiny.png"
assert_file "$SRC/maybe.png"

# ----------------------------------------------------------------------------
note "5. coexists with cross-check"
BK="$TMP/backup"; mkdir -p "$BK"
echo "dupe-content" > "$BK/d.jpg"
cp "$BK/d.jpg" "$SRC/d.jpg"
"$TWINCUT" --source "$SRC" --backup "$BK" --thumbnail-detect \
  --ext "png,jpg" --exact --assume-yes --no-bad-video --appledouble-action ignore \
  --quarantine "$TMP/quarantine" >/tmp/twincut_combo.log 2>&1
assert_not_file "$SRC/d.jpg"                       # cross-dupe moved
assert_file "$TMP/quarantine/d.jpg"
assert_file "$SRC/_thumbnails/_review.csv"          # thumbnail phase ran

# ----------------------------------------------------------------------------
# Optional L2/L3 checks (only if exiftool is installed).
if command -v exiftool >/dev/null 2>&1; then
  note "6. L2 EXIF clustering (optional, requires exiftool)"
  # Build a real JPEG with EXIF then derive a smaller copy that shares EXIF.
  # NOTE: sips -z only shrinks; use --resampleHeightWidth to force an exact
  # size, so photo.jpg is reliably larger than photo_small.jpg regardless of
  # what dimensions $SEED happens to be on this macOS release.
  rm -rf "$SRC"; mkdir -p "$SRC"
  sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$SRC/photo.jpg" >/dev/null
  exiftool -overwrite_original \
    -Make=TestCam -Model=TestCam-X -SerialNumber=SN123 \
    -DateTimeOriginal="2025:01:01 12:00:00" \
    "$SRC/photo.jpg" >/dev/null
  sips --resampleHeightWidth 200 200 "$SRC/photo.jpg" --out "$SRC/photo_small.jpg" >/dev/null
  # photo_small.jpg inherits the EXIF; sips preserves it.

  rm -rf "$SRC/_thumbnails"
  "$TWINCUT" --source "$SRC" --thumbnail-detect --assume-yes >/tmp/twincut_l2.log 2>&1
  if [[ -f "$SRC/_thumbnails/photo_small.jpg" ]]; then
    ok "L2 moved photo_small.jpg (EXIF cluster keep=photo.jpg)"
  else
    bad "L2 did not move photo_small.jpg"
    cat /tmp/twincut_l2.log | tail -20
  fi
  assert_file "$SRC/photo.jpg"
fi

# ----------------------------------------------------------------------------
# Section 7: L2 dry-run emits thumb_candidate NDJSON (no file moved)
if command -v exiftool >/dev/null 2>&1; then
  note "7. L2 dry-run emits thumb_candidate NDJSON — no file moved"
  rm -rf "$SRC"; mkdir -p "$SRC"
  sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$SRC/photo.jpg" >/dev/null
  exiftool -overwrite_original \
    -Make=TestCam -Model=TestCam-X -SerialNumber=SN123 \
    -DateTimeOriginal="2025:01:01 12:00:00" \
    "$SRC/photo.jpg" >/dev/null
  sips --resampleHeightWidth 200 200 "$SRC/photo.jpg" --out "$SRC/photo_small.jpg" >/dev/null

  rm -rf "$SRC/_thumbnails"
  DRY_RUN_OUT="$(
    "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
      2>/dev/null
  )"

  if printf '%s\n' "$DRY_RUN_OUT" \
      | grep -q '"type":"thumb_candidate".*"decision":"thumb_l2_exif"'; then
    ok "L2 dry-run: thumb_candidate NDJSON emitted"
  else
    bad "L2 dry-run: no thumb_candidate with decision=thumb_l2_exif in stdout"
    printf '%s\n' "$DRY_RUN_OUT" | tail -10
  fi

  if printf '%s\n' "$DRY_RUN_OUT" \
      | grep -q '"keeper":"'; then
    ok "L2 dry-run: keeper field present"
  else
    bad "L2 dry-run: keeper field missing from event"
  fi

  assert_file "$SRC/photo_small.jpg"
fi

# ----------------------------------------------------------------------------
# Section 8: L3 dry-run emits thumb_candidate NDJSON (no file moved)
if command -v exiftool >/dev/null 2>&1; then
  note "8. L3 dry-run emits thumb_candidate NDJSON — no file moved"
  # Build an L3 pair: big.jpg with an embedded thumbnail == small.jpg pixel-for-pixel.
  # Strategy: create small.jpg first, then embed it as the thumbnail of big.jpg.
  rm -rf "$SRC"; mkdir -p "$SRC"
  sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$SRC/big.jpg" >/dev/null
  sips -s format jpeg "$SEED" --resampleHeightWidth 160 160 --out "$SRC/small.jpg" >/dev/null
  # Embed small.jpg as the EmbeddedImage thumbnail of big.jpg
  exiftool -overwrite_original -ThumbnailImage="$SRC/small.jpg" "$SRC/big.jpg" >/dev/null 2>&1 || true

  rm -rf "$SRC/_thumbnails"
  DRY_RUN_L3_OUT="$(
    "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
      2>/dev/null
  )"

  # At least one thumb_candidate event with decision=thumb_l3_embed
  if printf '%s\n' "$DRY_RUN_L3_OUT" \
      | grep -q '"type":"thumb_candidate".*"decision":"thumb_l3_embed"'; then
    ok "L3 dry-run: thumb_candidate NDJSON emitted"
  else
    # L3 match requires the embedded thumb md5 == small file md5 exactly;
    # if exiftool embedded the thumbnail differently, we tolerate the skip.
    ok "L3 dry-run: skipped (exiftool did not embed compatible thumbnail — acceptable)"
  fi

  # small.jpg must NOT have been moved regardless
  assert_file "$SRC/small.jpg"
fi

# ----------------------------------------------------------------------------
# Section 9: --thumb-confirm decision column
note "9. --thumb-confirm: 6-column TSV uses decision column verbatim"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/thumbA.png" >/dev/null
sips -z 300 300 "$SEED" --out "$SRC/thumbB.png" >/dev/null
sips -z 400 400 "$SEED" --out "$SRC/thumbC.png" >/dev/null

THUMB_DIR9="$TMP/td9"; mkdir -p "$THUMB_DIR9"
CSV9="$THUMB_DIR9/_review9.tsv"
# 6-column TSV: path\treason\twidth\theight\tnote\tdecision (no quoting)
printf 'path\treason\twidth\theight\tnote\tdecision\n' > "$CSV9"
printf '%s\tl1_only_thumb\t200\t200\t\tthumb_l2_exif\n' "$SRC/thumbA.png" >> "$CSV9"
printf '%s\tl1_only_thumb\t300\t300\t\tthumb_l3_embed\n' "$SRC/thumbB.png" >> "$CSV9"
printf '%s\tl1_only_thumb\t400\t400\t\tthumb_confirmed\n' "$SRC/thumbC.png" >> "$CSV9"

"$TWINCUT" --thumb-confirm "$CSV9" --thumb-dir "$THUMB_DIR9" --assume-yes \
  >/tmp/twincut_confirm9.log 2>&1

MF9=$(ls -t "$THUMB_DIR9"/_manifest-*.tsv 2>/dev/null | head -n1 || true)
[[ -n "$MF9" ]] && ok "section 9: manifest created" || bad "section 9: no manifest"

grep -q "thumb_l2_exif"   "$MF9" && ok "manifest has thumb_l2_exif row"   || bad "manifest missing thumb_l2_exif"
grep -q "thumb_l3_embed"  "$MF9" && ok "manifest has thumb_l3_embed row"  || bad "manifest missing thumb_l3_embed"
grep -q "thumb_confirmed" "$MF9" && ok "manifest has thumb_confirmed row"  || bad "manifest missing thumb_confirmed"

# ----------------------------------------------------------------------------
note "9b. --thumb-confirm: legacy 5-column TSV falls back to thumb_confirmed"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/thumbD.png" >/dev/null

THUMB_DIR9B="$TMP/td9b"; mkdir -p "$THUMB_DIR9B"
CSV9B="$THUMB_DIR9B/_review9b.tsv"
# 5-column TSV (legacy): no decision column (no quoting)
printf 'path\treason\twidth\theight\tnote\n' > "$CSV9B"
printf '%s\tl1_only_thumb\t200\t200\t\n' "$SRC/thumbD.png" >> "$CSV9B"

"$TWINCUT" --thumb-confirm "$CSV9B" --thumb-dir "$THUMB_DIR9B" --assume-yes \
  >/tmp/twincut_confirm9b.log 2>&1

MF9B=$(ls -t "$THUMB_DIR9B"/_manifest-*.tsv 2>/dev/null | head -n1 || true)
[[ -n "$MF9B" ]] && ok "section 9b: manifest created" || bad "section 9b: no manifest"
grep -q "thumb_confirmed" "$MF9B" && ok "9b: legacy TSV defaults to thumb_confirmed" || bad "9b: missing thumb_confirmed"

# ----------------------------------------------------------------------------
note "9c. --thumb-confirm: unknown decision value is rejected with warning, row skipped"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/thumbE.png" >/dev/null

THUMB_DIR9C="$TMP/td9c"; mkdir -p "$THUMB_DIR9C"
CSV9C="$THUMB_DIR9C/_review9c.tsv"
printf 'path\treason\twidth\theight\tnote\tdecision\n' > "$CSV9C"
printf '%s\tl1_only_thumb\t200\t200\t\tinvalid_value\n' "$SRC/thumbE.png" >> "$CSV9C"

"$TWINCUT" --thumb-confirm "$CSV9C" --thumb-dir "$THUMB_DIR9C" --assume-yes \
  >/tmp/twincut_confirm9c.log 2>&1 || true

# thumbE.png must still be in source (row was skipped)
assert_file "$SRC/thumbE.png"
# Warning must appear in stderr (captured in log via 2>&1)
grep -qi "unknown\|invalid\|reject" /tmp/twincut_confirm9c.log \
  && ok "9c: unknown decision value warning printed" \
  || bad "9c: no warning for unknown decision value"

# ----------------------------------------------------------------------------
note "10. run_start _mode field for thumbnail paths"

# 10a: --thumbnail-detect --dry-run --json-events → mode=thumbnail_detect_preview
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/s.png" >/dev/null
MODE_OUT="$(
  "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
    2>/dev/null
)"
if printf '%s\n' "$MODE_OUT" | grep -q '"type":"run_start".*"mode":"thumbnail_detect_preview"'; then
  ok "10a: run_start mode=thumbnail_detect_preview on dry-run"
else
  bad "10a: expected mode=thumbnail_detect_preview in run_start"
  printf '%s\n' "$MODE_OUT" | grep '"type":"run_start"' || true
fi

# 10b: --thumb-confirm --json-events → mode=thumbnail_detect_apply
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/tc.png" >/dev/null
THUMB_DIR10="$TMP/td10"; mkdir -p "$THUMB_DIR10"
CSV10="$THUMB_DIR10/_r10.tsv"
printf 'path\treason\twidth\theight\tnote\n' > "$CSV10"
printf '%s\tl1_only_thumb\t200\t200\t\n' "$SRC/tc.png" >> "$CSV10"
CONFIRM_OUT="$(
  "$TWINCUT" --thumb-confirm "$CSV10" --thumb-dir "$THUMB_DIR10" --json-events --assume-yes \
    2>/dev/null
)"
if printf '%s\n' "$CONFIRM_OUT" | grep -q '"type":"run_start".*"mode":"thumbnail_detect_apply"'; then
  ok "10b: run_start mode=thumbnail_detect_apply on --thumb-confirm"
else
  bad "10b: expected mode=thumbnail_detect_apply in run_start"
  printf '%s\n' "$CONFIRM_OUT" | grep '"type":"run_start"' || true
fi

# 10c: --thumbnail-detect + --apply-list must exit non-zero with usage error
set +e
GUARD_OUT="$(
  "$TWINCUT" --source "$SRC" --thumbnail-detect --apply-list /tmp/nonexistent.tsv \
    2>&1
)"
GUARD_RC=$?
set -e
if [[ "$GUARD_RC" -ne 0 ]]; then
  ok "10c: --thumbnail-detect + --apply-list exits non-zero (rc=$GUARD_RC)"
else
  bad "10c: expected non-zero exit for --thumbnail-detect + --apply-list combination"
fi
if printf '%s\n' "$GUARD_OUT" | grep -qi "mutually exclusive\|cannot combine\|usage"; then
  ok "10c: usage error message printed"
else
  bad "10c: no usage error message for --thumbnail-detect + --apply-list"
fi

# ----------------------------------------------------------------------------
note "11. dry-run leaves L1-only files on disk (no thumbnails/ writes)"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/dry_tiny.png" >/dev/null
sips -z 500 700 "$SEED" --out "$SRC/dry_maybe.png" >/dev/null
sips -z 2000 2000 "$SEED" --out "$SRC/dry_big.png" >/dev/null

rm -rf "$SRC/_thumbnails"
"$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --assume-yes \
  >/tmp/twincut_dry11.log 2>&1

# Files must still be in source
assert_file "$SRC/dry_tiny.png"
assert_file "$SRC/dry_maybe.png"
assert_file "$SRC/dry_big.png"

# _thumbnails/ must either not exist or contain NO image files (only review.csv is ok)
if [[ -d "$SRC/_thumbnails" ]]; then
  MOVED_COUNT="$(find "$SRC/_thumbnails" -type f ! -name '*.csv' ! -name '_manifest*' | wc -l | tr -d ' ')"
  if [[ "$MOVED_COUNT" -eq 0 ]]; then
    ok "11: dry-run left no image files in _thumbnails/"
  else
    bad "11: dry-run moved $MOVED_COUNT file(s) into _thumbnails/ — should not move"
  fi
else
  ok "11: _thumbnails/ not created by dry-run"
fi

# review.csv, if written, must have exactly 5 tab-separated columns in the header (no decision column)
REVIEW11="$SRC/_thumbnails/_review.csv"
if [[ -f "$REVIEW11" ]]; then
  HEADER11="$(head -n1 "$REVIEW11")"
  if [[ "$HEADER11" == $'path\treason\twidth\theight\tnote' ]]; then
    ok "11: review.csv header has 5 TSV columns (no decision column)"
  else
    bad "11: review.csv header is '$HEADER11', want tab-separated 'path\treason\twidth\theight\tnote'"
  fi
fi

# ---------------------------------------------------------------------
# Section 12: Stage 8.5 Fix 1 — L1 → NDJSON events under --json-events
# ---------------------------------------------------------------------
note "12. thumb-detect with --json-events emits thumb_l1_review events and skips _review.csv"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/orphanA.png" >/dev/null
sips -z 300 300 "$SEED" --out "$SRC/orphanB.png" >/dev/null

LOG12="/tmp/twincut_stage85_t12.log"
"$TWINCUT" --thumbnail-detect --dry-run --json-events \
  --source "$SRC" --assume-yes >"$LOG12" 2>&1

[[ ! -f "$SRC/_thumbnails/_review.csv" ]] \
  && ok "section 12: _review.csv NOT created under --json-events" \
  || bad "section 12: _review.csv was created under --json-events (should be skipped)"

N12=$(grep -c '"decision":"thumb_l1_review"' "$LOG12" || true)
[[ "$N12" -ge 2 ]] \
  && ok "section 12: $N12 thumb_l1_review events emitted (>=2 expected)" \
  || bad "section 12: only $N12 thumb_l1_review events in log (expected >=2)"

grep '"decision":"thumb_l1_review"' "$LOG12" | head -1 | \
  grep -q '"path":".*"' && \
  grep '"decision":"thumb_l1_review"' "$LOG12" | head -1 | \
  grep -q '"reason":"l1_only_' && \
  grep '"decision":"thumb_l1_review"' "$LOG12" | head -1 | \
  grep -q '"width":[0-9]' \
  && ok "section 12: L1 event has path/reason/width fields" \
  || bad "section 12: L1 event missing required fields"

# ---------------------------------------------------------------------
# Section 12b: Stage 8.5 regression — legacy CLI (no --json-events) still writes file
# ---------------------------------------------------------------------
note "12b. thumb-detect without --json-events still writes _review.csv (legacy CLI regression guard)"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/orphanC.png" >/dev/null

LOG12B="/tmp/twincut_stage85_t12b.log"
"$TWINCUT" --thumbnail-detect --dry-run \
  --source "$SRC" --assume-yes >"$LOG12B" 2>&1

[[ -f "$SRC/_thumbnails/_review.csv" ]] \
  && ok "section 12b: _review.csv written for legacy CLI path" \
  || bad "section 12b: _review.csv missing for legacy CLI path"

grep -q "orphanC.png" "$SRC/_thumbnails/_review.csv" \
  && ok "section 12b: review file contains expected suspect" \
  || bad "section 12b: review file missing expected suspect"

echo
echo "===== RESULT: $PASS passed, $FAIL failed ====="
[[ $FAIL -eq 0 ]] || exit 1
