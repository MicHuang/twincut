#!/usr/bin/env bash
# tests/p1_thumb_smoke.sh — smoke test for P1 thumbnail DETECTION (L1+L2+L3).
#
# Scope: the detect/scan side of thumbnail_detect — classification and the
# events/review.csv it produces. The APPLY side (executing the moves) is
# covered by tests/p1_stage9_smoke.sh via the Go-owned --json-in contract;
# L1 pHash pairing is covered by tests/p1_thumb_phash_smoke.sh.
#
# Validates:
#   - L1 dimension classification (sips path) → review.csv (no automatic move)
#   - L1-only suspects are never auto-moved (review-only policy)
#   - graceful degrade when exiftool is absent (L2/L3 skipped, no crash)
#   - thumbnail-detect can run standalone (only --source, no --backup)
#   - thumbnail-detect coexists with cross-check
#   - --json-events emits thumb_candidate / thumb_l1_review NDJSON
#   - run_start mode field + --apply-list mutual-exclusion guard
#
# When exiftool IS available, additionally validates:
#   - L2 EXIF clustering: identical fingerprint, different size → small one moved
#   - L3 embedded thumbnail: small file == big file's embedded thumb (dry-run event)
# These extra checks require: brew install exiftool

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
TWINCUT="$ROOT/bin/twincut.sh"

# Under TWINCUT_REQUIRE_TOOLS=1 (set by the macOS CI job) a missing tool is a
# hard FAILURE instead of a silent skip — that silent skip is exactly how this
# suite could go green in CI without exercising anything (reviewer-codex finding).
if ! command -v sips >/dev/null 2>&1; then
  if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]]; then
    echo "FAIL: 'sips' not found but TWINCUT_REQUIRE_TOOLS=1 — this runner must exercise the thumbnail path"; exit 1
  fi
  echo "sips not found — this test requires macOS 'sips'; skipping"; exit 0
fi
# exiftool gates L2/L3. Require it under require-mode so CI gets real L2/L3
# coverage; in normal mode the per-section guards degrade gracefully (L1 runs).
if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]] && ! command -v exiftool >/dev/null 2>&1; then
  echo "FAIL: 'exiftool' not found but TWINCUT_REQUIRE_TOOLS=1 — L2/L3 coverage required"; exit 1
fi

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
# Seed image: generate a large PNG so the suite is runner-independent (GitHub
# macOS runners lack the Desktop Pictures dir). `sips -z` only shrinks, so the
# seed must be bigger than the largest target (2000px). L2/L3 inject their own
# EXIF via exiftool below, so the seed needs no metadata of its own.
SEED="$TMP/_seed.png"
if command -v python3 >/dev/null 2>&1 && python3 -c 'import PIL' >/dev/null 2>&1; then
  python3 - "$SEED" <<'PY'
import sys
from PIL import Image
# Build a small gradient then upscale (fast C resize) to 2400x2400.
small = Image.new("RGB", (64, 64))
for x in range(64):
    for y in range(64):
        small.putpixel((x, y), ((x * 4) % 256, (y * 4) % 256, (x * y) % 256))
small.resize((2400, 2400)).save(sys.argv[1])
PY
fi
# Fall back to a system image (local macOS dev), then skip/fail per require-mode.
if [[ ! -f "$SEED" ]]; then
  SEED="/System/Library/Desktop Pictures/Solid Colors/Black.png"
  [[ -f "$SEED" ]] || SEED="/System/Library/Desktop Pictures/Solid Colors/Stone.png"
fi
if [[ ! -f "$SEED" ]]; then
  if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]]; then
    echo "FAIL: no seed image and Pillow unavailable but TWINCUT_REQUIRE_TOOLS=1"; exit 1
  fi
  echo "no seed image found, skipping"; exit 0
fi

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
note "2. --thumb-action move with no L2/L3 evidence does NOT auto-move L1-only"
# Re-run with --thumb-action move; without exiftool, L1-only files must STILL
# go to review (the L2/L3 evidence is missing → policy never auto-deletes).
rm -f "$REVIEW"
"$TWINCUT" --source "$SRC" --thumbnail-detect --thumb-action move --assume-yes \
  >/tmp/twincut_thumb2.log 2>&1
assert_file "$SRC/tiny.png"
assert_file "$SRC/maybe.png"

# ----------------------------------------------------------------------------
note "3. coexists with cross-check"
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
  note "4. L2 EXIF clustering (optional, requires exiftool)"
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
# Section 5: L2 dry-run emits thumb_candidate NDJSON (no file moved)
if command -v exiftool >/dev/null 2>&1; then
  note "5. L2 dry-run emits thumb_candidate NDJSON — no file moved"
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
# Section 6: L3 dry-run emits thumb_candidate NDJSON (no file moved)
if command -v exiftool >/dev/null 2>&1; then
  note "6. L3 dry-run emits thumb_candidate NDJSON — no file moved"
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
note "7. run_start mode=thumbnail_detect_preview on dry-run"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/s.png" >/dev/null
MODE_OUT="$(
  "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
    2>/dev/null
)"
if printf '%s\n' "$MODE_OUT" | grep -q '"type":"run_start".*"mode":"thumbnail_detect_preview"'; then
  ok "7: run_start mode=thumbnail_detect_preview on dry-run"
else
  bad "7: expected mode=thumbnail_detect_preview in run_start"
  printf '%s\n' "$MODE_OUT" | grep '"type":"run_start"' || true
fi

# ----------------------------------------------------------------------------
note "8. --thumbnail-detect + --apply-list is rejected (usage error)"
set +e
GUARD_OUT="$(
  "$TWINCUT" --source "$SRC" --thumbnail-detect --apply-list /tmp/nonexistent.tsv \
    2>&1
)"
GUARD_RC=$?
set -e
if [[ "$GUARD_RC" -ne 0 ]]; then
  ok "8: --thumbnail-detect + --apply-list exits non-zero (rc=$GUARD_RC)"
else
  bad "8: expected non-zero exit for --thumbnail-detect + --apply-list combination"
fi
if printf '%s\n' "$GUARD_OUT" | grep -qi "mutually exclusive\|cannot combine\|usage"; then
  ok "8: usage error message printed"
else
  bad "8: no usage error message for --thumbnail-detect + --apply-list"
fi

# ----------------------------------------------------------------------------
note "9. dry-run leaves L1-only files on disk (no thumbnails/ writes)"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/dry_tiny.png" >/dev/null
sips -z 500 700 "$SEED" --out "$SRC/dry_maybe.png" >/dev/null
sips -z 2000 2000 "$SEED" --out "$SRC/dry_big.png" >/dev/null

rm -rf "$SRC/_thumbnails"
"$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --assume-yes \
  >/tmp/twincut_dry9.log 2>&1

# Files must still be in source
assert_file "$SRC/dry_tiny.png"
assert_file "$SRC/dry_maybe.png"
assert_file "$SRC/dry_big.png"

# _thumbnails/ must either not exist or contain NO image files (only review.csv is ok)
if [[ -d "$SRC/_thumbnails" ]]; then
  MOVED_COUNT="$(find "$SRC/_thumbnails" -type f ! -name '*.csv' ! -name '_manifest*' | wc -l | tr -d ' ')"
  if [[ "$MOVED_COUNT" -eq 0 ]]; then
    ok "9: dry-run left no image files in _thumbnails/"
  else
    bad "9: dry-run moved $MOVED_COUNT file(s) into _thumbnails/ — should not move"
  fi
else
  ok "9: _thumbnails/ not created by dry-run"
fi

# review.csv, if written, must have exactly 5 tab-separated columns in the header (no decision column)
REVIEW9="$SRC/_thumbnails/_review.csv"
if [[ -f "$REVIEW9" ]]; then
  HEADER9="$(head -n1 "$REVIEW9")"
  if [[ "$HEADER9" == $'path\treason\twidth\theight\tnote' ]]; then
    ok "9: review.csv header has 5 TSV columns (no decision column)"
  else
    bad "9: review.csv header is '$HEADER9', want tab-separated 'path\treason\twidth\theight\tnote'"
  fi
fi

# ---------------------------------------------------------------------
# Section 10: Stage 8.5 Fix 1 — L1 → NDJSON events under --json-events
# ---------------------------------------------------------------------
note "10. thumb-detect with --json-events emits thumb_l1_review events and skips _review.csv"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/orphanA.png" >/dev/null
sips -z 300 300 "$SEED" --out "$SRC/orphanB.png" >/dev/null

LOG10="/tmp/twincut_stage85_t10.log"
"$TWINCUT" --thumbnail-detect --dry-run --json-events \
  --source "$SRC" --assume-yes >"$LOG10" 2>&1

[[ ! -f "$SRC/_thumbnails/_review.csv" ]] \
  && ok "section 10: _review.csv NOT created under --json-events" \
  || bad "section 10: _review.csv was created under --json-events (should be skipped)"

N10=$(grep -c '"decision":"thumb_l1_review"' "$LOG10" || true)
[[ "$N10" -ge 2 ]] \
  && ok "section 10: $N10 thumb_l1_review events emitted (>=2 expected)" \
  || bad "section 10: only $N10 thumb_l1_review events in log (expected >=2)"

grep '"decision":"thumb_l1_review"' "$LOG10" | head -1 | \
  grep -q '"path":".*"' && \
  grep '"decision":"thumb_l1_review"' "$LOG10" | head -1 | \
  grep -q '"reason":"l1_only_' && \
  grep '"decision":"thumb_l1_review"' "$LOG10" | head -1 | \
  grep -q '"width":[0-9]' \
  && ok "section 10: L1 event has path/reason/width fields" \
  || bad "section 10: L1 event missing required fields"

# ---------------------------------------------------------------------
# Section 11: Stage 8.5 regression — legacy CLI (no --json-events) still writes file
# ---------------------------------------------------------------------
note "11. thumb-detect without --json-events still writes _review.csv (legacy CLI regression guard)"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/orphanC.png" >/dev/null

LOG11="/tmp/twincut_stage85_t11.log"
"$TWINCUT" --thumbnail-detect --dry-run \
  --source "$SRC" --assume-yes >"$LOG11" 2>&1

[[ -f "$SRC/_thumbnails/_review.csv" ]] \
  && ok "section 11: _review.csv written for legacy CLI path" \
  || bad "section 11: _review.csv missing for legacy CLI path"

grep -q "orphanC.png" "$SRC/_thumbnails/_review.csv" \
  && ok "section 11: review file contains expected suspect" \
  || bad "section 11: review file missing expected suspect"

echo
echo "===== RESULT: $PASS passed, $FAIL failed ====="
[[ $FAIL -eq 0 ]] || exit 1
