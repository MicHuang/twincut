#!/usr/bin/env bash
# Build thumbnail-detect fixture set for stage-8 smoke tests.
# Requires: sips (macOS built-in), exiftool (brew install exiftool)
#
# Generated layout:
#   l2_keeper.jpg      — 1600×1600, EXIF fingerprint SN=STAGE8SN
#   l2_thumb_a.jpg     — 200×200, same EXIF fingerprint
#   l2_thumb_b.jpg     — 300×300, same EXIF fingerprint
#   l3_big.jpg         — 1400×1400, embedded thumbnail == l3_small.jpg pixels
#   l3_small.jpg       — 140×140, matches embedded thumb of l3_big.jpg
#   l1_only_thumb.jpg  — 200×200, no peer (L1 review)
#   l1_only_maybe.jpg  — 800×600, no peer (L1 maybe review)
#   clean_a.jpg        — 2000×2000, must NOT be flagged
#   clean_b.jpg        — 2100×2100, must NOT be flagged
#   clean_c.jpg        — 1800×1800, must NOT be flagged
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="$SCRIPT_DIR"

# Seed: any JPEG on the system.
SEED="${SEED:-}"
if [[ -z "$SEED" ]]; then
  SEED="$(find /Library/Desktop\ Pictures /System/Library/Desktop\ Pictures \
    -name '*.jpg' -o -name '*.jpeg' 2>/dev/null | head -n1 || true)"
fi
if [[ -z "$SEED" ]]; then
  echo "ERROR: no seed JPEG found. Set SEED=/path/to/file.jpg" >&2
  exit 1
fi
echo "Using seed: $SEED"

# ----- L2 cluster -----
echo "Building L2 cluster…"
sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$OUT/l2_keeper.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original \
    -Make=TestCam -Model=StageEightCam -SerialNumber=STAGE8SN \
    -DateTimeOriginal="2025:06:01 10:00:00" \
    "$OUT/l2_keeper.jpg" >/dev/null
fi

sips -s format jpeg "$SEED" --resampleHeightWidth 200 200 --out "$OUT/l2_thumb_a.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original \
    -Make=TestCam -Model=StageEightCam -SerialNumber=STAGE8SN \
    -DateTimeOriginal="2025:06:01 10:00:00" \
    "$OUT/l2_thumb_a.jpg" >/dev/null
fi

sips -s format jpeg "$SEED" --resampleHeightWidth 300 300 --out "$OUT/l2_thumb_b.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original \
    -Make=TestCam -Model=StageEightCam -SerialNumber=STAGE8SN \
    -DateTimeOriginal="2025:06:01 10:00:00" \
    "$OUT/l2_thumb_b.jpg" >/dev/null
fi

# ----- L3 pair -----
echo "Building L3 pair…"
sips -s format jpeg "$SEED" --resampleHeightWidth 1400 1400 --out "$OUT/l3_big.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 140 140 --out "$OUT/l3_small.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original -ThumbnailImage="$OUT/l3_small.jpg" \
    "$OUT/l3_big.jpg" >/dev/null 2>&1 || true
fi

# ----- L1 suspects -----
echo "Building L1 suspects…"
sips -s format jpeg "$SEED" --resampleHeightWidth 200 200 --out "$OUT/l1_only_thumb.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 800 600 --out "$OUT/l1_only_maybe.jpg" >/dev/null

# ----- Clean images -----
echo "Building clean images…"
sips -s format jpeg "$SEED" --resampleHeightWidth 2000 2000 --out "$OUT/clean_a.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 2100 2100 --out "$OUT/clean_b.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 1800 1800 --out "$OUT/clean_c.jpg" >/dev/null

echo "Done. Files:"
ls -lh "$OUT/"*.jpg
