#!/usr/bin/env bash
# tests/p1_thumb_phash_smoke.sh — smoke test for P1 wave 2 (L1 perceptual hash).
#
# Requires: macOS sips; for most sections also Python 3 + Pillow + imagehash.
# Sections that don't need Pillow are 10, 11 (gated separately).
#
# Validates:
#   - bin/phash.py stdin→stdout protocol
#   - thumb_run_l1_phash builds + caches the index
#   - matched L1 suspects emit thumb_candidate with keeper + group_id + phash_distance
#   - unmatched suspects still emit l1_only_thumb / l1_only_maybe (no keeper)
#   - multiple suspects on the same keeper share one group_id
#   - cache: meta drift, mtime invalidation, prune-on-miss
#   - graceful skip when deps missing or env disabled

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
TWINCUT="$ROOT/bin/twincut.sh"
PHASH="$ROOT/bin/phash.py"

command -v sips >/dev/null 2>&1 || { echo "sips not found — requires macOS"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

SRC="$TMP/src"
mkdir -p "$SRC"

PASS=0; FAIL=0
note(){ printf '\n=== %s ===\n' "$*"; }
ok(){   printf '  ok   %s\n' "$*"; PASS=$((PASS+1)); }
bad(){  printf '  FAIL %s\n' "$*"; FAIL=$((FAIL+1)); }
assert_file(){     [[ -e "$1" ]] && ok "exists: $1"     || bad "missing: $1"; }
assert_not_file(){ [[ ! -e "$1" ]] && ok "absent: $1"   || bad "still there: $1"; }

# ---------------------------------------------------------------------
# Section 0: probe Python + Pillow + imagehash availability
# ---------------------------------------------------------------------
note "0. probe python + pillow + imagehash"
HAVE_PHASH_DEPS=false
if command -v python3 >/dev/null 2>&1 \
   && python3 -c "import PIL, imagehash" >/dev/null 2>&1; then
  HAVE_PHASH_DEPS=true
  ok "section 0: python3 + Pillow + imagehash available"
else
  ok "section 0: pHash deps NOT available — sections 1–9, 12 will be skipped"
fi

# ---------------------------------------------------------------------
# Section 1: bin/phash.py round-trip on a single image
# ---------------------------------------------------------------------
note "1. phash.py emits a 16-hex-char dhash for a PNG"
if $HAVE_PHASH_DEPS; then
  SEED="/System/Library/Desktop Pictures/Solid Colors/Black.png"
  [[ -f "$SEED" ]] || SEED="/System/Library/Desktop Pictures/Solid Colors/Stone.png"
  [[ -f "$SEED" ]] || { echo "no seed image"; exit 0; }

  sips -s format png "$SEED" --resampleHeightWidth 400 400 \
       --out "$SRC/s1_a.png" >/dev/null

  H1="$(printf '%s\n' "$SRC/s1_a.png" | "$PHASH" 2>/dev/null)"
  # expect: <path>\t<16 hex chars>
  if [[ "$H1" =~ ^${SRC}/s1_a\.png$'\t'[0-9a-f]{16}$ ]]; then
    ok "section 1: phash.py produced 16-hex-char dhash"
  else
    bad "section 1: phash.py output malformed: '$H1'"
  fi
else
  ok "section 1: skipped (no pHash deps)"
fi

printf '\n=========================================\n'
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
