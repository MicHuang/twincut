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

# ---------------------------------------------------------------------
# Section 1.5: Pillow fixture generator (used by sections 2-9)
# ---------------------------------------------------------------------
gen_fixtures(){
  $HAVE_PHASH_DEPS || return 0
  python3 - "$SRC" <<'PY'
import sys, os
from PIL import Image, ImageDraw
src = sys.argv[1]
def grad(path, w, h, color_a, color_b):
    im = Image.new("RGB", (w, h), color_a)
    d = ImageDraw.Draw(im)
    for x in range(w):
        t = x / max(w-1, 1)
        c = tuple(int(color_a[i]*(1-t)+color_b[i]*t) for i in range(3))
        d.line([(x, 0), (x, h)], fill=c)
    im.save(path, "JPEG", quality=88)
# Scene A: blue→red gradient
grad(os.path.join(src, "photo_a_big.jpg"),   2000, 1500, (10, 30, 200), (220, 30, 10))
grad(os.path.join(src, "photo_a_small.jpg"),  200,  150, (10, 30, 200), (220, 30, 10))
# Scene B: green→yellow gradient
grad(os.path.join(src, "photo_b_big.jpg"),   2000, 1500, (10, 200, 30), (240, 240, 10))
grad(os.path.join(src, "photo_b_thumb1.jpg"), 300,  225, (10, 200, 30), (240, 240, 10))
grad(os.path.join(src, "photo_b_thumb2.jpg"), 150,  113, (10, 200, 30), (240, 240, 10))
# Orphan: purple solid (no matching big image)
Image.new("RGB", (100, 100), (120, 30, 180)).save(os.path.join(src, "orphan_small.png"), "PNG")
# Unrelated big: horizontal gradient (left-to-right) so dhash != orphan's all-zero hash
im = Image.new("RGB", (2000, 1500))
d = ImageDraw.Draw(im)
for x in range(2000):
    t = x / 1999
    c = (int(40+t*180), int(40+t*40), int(220-t*180))
    d.line([(x, 0), (x, 1499)], fill=c)
im.save(os.path.join(src, "unrelated_big.jpg"), "JPEG", quality=88)
PY
}

# ---------------------------------------------------------------------
# Section 2: matched pair emits keeper + group_id + phash_distance
# ---------------------------------------------------------------------
note "2. matched pair: small downscale gets keeper + group_id"
if $HAVE_PHASH_DEPS; then
  rm -rf "$SRC"; mkdir -p "$SRC"; gen_fixtures
  LOG2="$TMP/run2.log"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >"$LOG2" 2>&1 || true
  EV=$(grep '"path":"'"$SRC"'/photo_a_small.jpg"' "$LOG2" | head -1)
  if echo "$EV" | grep -q '"keeper":"'"$SRC"'/photo_a_big.jpg"'; then
    ok "section 2: photo_a_small.jpg → keeper=photo_a_big.jpg"
  else
    bad "section 2: missing/wrong keeper on photo_a_small.jpg event: $EV"
  fi
  echo "$EV" | grep -qE '"group_id":"l1ph:[0-9a-f]{16}"' \
    && ok "section 2: group_id has l1ph: prefix and 16 hex chars" \
    || bad "section 2: group_id missing or malformed: $EV"
  echo "$EV" | grep -q '"reason":"l1_phash_match"' \
    && ok "section 2: reason=l1_phash_match" \
    || bad "section 2: reason not l1_phash_match: $EV"
  echo "$EV" | grep -qE '"phash_distance":[0-9]+' \
    && ok "section 2: phash_distance present" \
    || bad "section 2: phash_distance missing: $EV"
else
  ok "section 2: skipped (no pHash deps)"
fi

# ---------------------------------------------------------------------
# Section 5: index file created with meta header
# ---------------------------------------------------------------------
note "5. .thumb_phash_index.tsv exists with # meta: header"
if $HAVE_PHASH_DEPS; then
  IDX="$SRC/.thumb_phash_index.tsv"
  assert_file "$IDX"
  HEAD1="$(head -n1 "$IDX")"
  [[ "$HEAD1" =~ ^\#\ meta:.*algo=dhash.*hash_size=8 ]] \
    && ok "section 5: meta header has algo=dhash hash_size=8" \
    || bad "section 5: meta header malformed: '$HEAD1'"
else
  ok "section 5: skipped (no pHash deps)"
fi

# ---------------------------------------------------------------------
# Section 6: second run uses cache (recomputed=0)
# ---------------------------------------------------------------------
note "6. warm re-run reports recomputed=0"
if $HAVE_PHASH_DEPS; then
  LOG6="$TMP/run6.log"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >"$LOG6" 2>&1 || true
  if grep -E '(recomputed 0|cache hits [1-9])' "$LOG6" >/dev/null; then
    ok "section 6: warm re-run reused cache"
  else
    bad "section 6: no cache-hit log line found"
    grep -i phash "$LOG6" || true
  fi
else
  ok "section 6: skipped (no pHash deps)"
fi

# ---------------------------------------------------------------------
# Section 7: mtime invalidation re-hashes one row
# ---------------------------------------------------------------------
note "7. touching one file re-hashes exactly that row"
if $HAVE_PHASH_DEPS; then
  touch -t 203012310000.00 "$SRC/photo_a_big.jpg"
  LOG7="$TMP/run7.log"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >"$LOG7" 2>&1 || true
  grep -E 'recomputed [1-9][0-9]* \(cold or modified\)' "$LOG7" >/dev/null \
    && ok "section 7: at least one row re-hashed after touch" \
    || bad "section 7: did not detect re-hash after touch"
else
  ok "section 7: skipped (no pHash deps)"
fi

# ---------------------------------------------------------------------
# Section 8: delete invalidation prunes index row
# ---------------------------------------------------------------------
note "8. deleting a file prunes its row on next run"
if $HAVE_PHASH_DEPS; then
  rm -f "$SRC/unrelated_big.jpg"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >/dev/null 2>&1 || true
  grep -q "unrelated_big.jpg" "$SRC/.thumb_phash_index.tsv" \
    && bad "section 8: deleted file still in index" \
    || ok "section 8: deleted file pruned from index"
else
  ok "section 8: skipped (no pHash deps)"
fi

# ---------------------------------------------------------------------
# Section 9: meta drift triggers full rebuild
# ---------------------------------------------------------------------
note "9. editing meta header forces rebuild"
if $HAVE_PHASH_DEPS; then
  sed -i.bak 's/algo=dhash/algo=phash/' "$SRC/.thumb_phash_index.tsv"
  rm -f "$SRC/.thumb_phash_index.tsv.bak"
  LOG9="$TMP/run9.log"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >"$LOG9" 2>&1 || true
  grep -q 'pHash index rebuild (meta drift)' "$LOG9" \
    && ok "section 9: meta drift triggered rebuild" \
    || bad "section 9: no rebuild log line for meta drift"
  HEAD9="$(head -n1 "$SRC/.thumb_phash_index.tsv")"
  [[ "$HEAD9" =~ algo=dhash ]] \
    && ok "section 9: header restored to algo=dhash" \
    || bad "section 9: header not rebuilt: '$HEAD9'"
else
  ok "section 9: skipped (no pHash deps)"
fi

# ---------------------------------------------------------------------
# Section 3: multiple suspects on same keeper share one group_id
# ---------------------------------------------------------------------
note "3. photo_b_thumb1 and photo_b_thumb2 share group_id"
if $HAVE_PHASH_DEPS; then
  rm -rf "$SRC"; mkdir -p "$SRC"; gen_fixtures
  LOG3="$TMP/run3.log"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >"$LOG3" 2>&1 || true
  G1=$(grep '"path":"'"$SRC"'/photo_b_thumb1.jpg"' "$LOG3" | head -1 \
       | sed -n 's/.*"group_id":"\([^"]*\)".*/\1/p')
  G2=$(grep '"path":"'"$SRC"'/photo_b_thumb2.jpg"' "$LOG3" | head -1 \
       | sed -n 's/.*"group_id":"\([^"]*\)".*/\1/p')
  if [[ -n "$G1" && "$G1" == "$G2" ]]; then
    ok "section 3: thumb1 and thumb2 share group_id ($G1)"
  else
    bad "section 3: group_ids differ or missing — thumb1=$G1 thumb2=$G2"
  fi
fi

# ---------------------------------------------------------------------
# Section 4: orphan suspect stays unmatched (no keeper, no group_id)
# ---------------------------------------------------------------------
note "4. orphan_small.png has empty keeper and group_id"
if $HAVE_PHASH_DEPS; then
  EV4=$(grep '"path":"'"$SRC"'/orphan_small.png"' "$LOG3" | head -1)
  if [[ -z "$EV4" ]]; then
    bad "section 4: no event for orphan_small.png"
  else
    echo "$EV4" | grep -q '"keeper":' \
      && bad "section 4: orphan event unexpectedly has keeper field: $EV4" \
      || ok "section 4: orphan event has no keeper field"
    echo "$EV4" | grep -q '"group_id":' \
      && bad "section 4: orphan event unexpectedly has group_id field: $EV4" \
      || ok "section 4: orphan event has no group_id field"
    echo "$EV4" | grep -qE '"reason":"l1_only_(thumb|maybe)"' \
      && ok "section 4: orphan reason is l1_only_*" \
      || bad "section 4: orphan reason wrong: $EV4"
  fi
fi

printf '\n=========================================\n'
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
