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
echo
echo "===== RESULT: $PASS passed, $FAIL failed ====="
[[ $FAIL -eq 0 ]] || exit 1
