# P1 Wave 2 — L1 Perceptual Hash — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a perceptual-hash pairing signal to L1 thumbnail suspects so each matched suspect carries a `keeper` and `group_id` in its `thumb_candidate` event; the Web UI then renders matched L1 suspects as proper groups instead of dumping them all into the synthetic `l1-suspects` bucket.

**Architecture:** Bash-driven leaf primitive (`bin/phash.py`) computes dhash via Pillow + imagehash; `lib/thumb.sh` adds a new phase `thumb_run_l1_phash` that maintains a persistent `<source>/.thumb_phash_index.tsv` cache, pipes paths to `phash.py` in batch, and pairs suspects to keepers in-memory via three bash assoc arrays. `thumb_write_review` consults those arrays at emit time. Go gets one new optional field (`PhashDistance`) and one routing change (matched L1 → its own group via `group_id`; unmatched still → synthetic `l1-suspects`).

**Tech Stack:** bash, Python 3 (Pillow, imagehash), Go (`encoding/json`), NDJSON, TSV

**Spec:** [docs/superpowers/specs/2026-05-21-twincut-p1-wave2-l1-perceptual-hash-design.md](../specs/2026-05-21-twincut-p1-wave2-l1-perceptual-hash-design.md)

**Branch:** `feature/p1-wave2-l1-perceptual-hash` (already created off `main`)

---

## Implementation deviation from spec

The spec §3 says `$THUMB_INDEX_FILE` evolves from 4 → 7 columns to carry pairing results between phases. The plan **rejects** that path because reading a TSV with possibly-empty middle fields via bash `IFS=$'\t' read` is unreliable (the Stage 8.5 spec called this out for the same reason in `thumb_confirm_review`). Instead, this plan uses **three bash associative arrays** (`THUMB_PHASH_KEEPER`, `THUMB_PHASH_GROUP_ID`, `THUMB_PHASH_DISTANCE`) keyed by suspect path. `$THUMB_INDEX_FILE` stays at its current 4-column shape. The persistent disk cache (`<source>/.thumb_phash_index.tsv`) is unchanged from spec §2. User-visible behavior is identical to what the spec describes.

---

## File map

| File | Touched in tasks | Purpose |
|---|---|---|
| `bin/phash.py` | T1 | New leaf primitive: stdin paths → stdout `path\thash_hex` |
| `lib/thumb.sh` | T2, T3, T4, T7 | New `thumb_run_l1_phash`; matched-aware `thumb_write_review`; env-knob skip |
| `bin/twincut.sh` | T7 | Init env defaults (`THUMB_PHASH_ENABLED`, `THUMB_PHASH_HAMMING`, etc.) |
| `ui/server/events.go` | T5 | `ThumbCandidate.PhashDistance` field |
| `ui/server/results.go` | T6 | `EventThumbCandidate` matched-vs-unmatched routing; `ResultMember.PhashDistance` |
| `ui/server/events_test.go` | T5 | Parse `phash_distance` from event |
| `ui/server/results_test.go` | T6 | Matched L1 → own group; unmatched L1 → synthetic group; merge on shared `group_id` |
| `tests/p1_thumb_phash_smoke.sh` | T1, T2, T3, T4, T7 | New smoke covering all 12 sections from spec §4 |
| `installers/install.sh` | T8 | Symlink `phash` and pip install Pillow + imagehash |
| `installers/uninstall.sh` | T8 | Remove `phash` symlink (do not pip-uninstall) |
| `CLAUDE.md` | T8 | Note optional Python deps |
| `.gitignore` | T8 | Ignore `.thumb_phash_index.tsv` and `.thumb_phash_index.tsv.tmp` |

---

## Task 1: `bin/phash.py` leaf primitive

**Files:**
- Create: `bin/phash.py`
- Create: `tests/p1_thumb_phash_smoke.sh` (initial skeleton + sections 0–1)

The helper is a stdin/stdout filter. It reads one absolute path per line, writes one `path\thash_hex\n` per successful path to stdout, and one `path\tERROR\t<reason>\n` per failed path to stderr. Exit 0 on completion (per-file errors don't fail the run), exit 2 on usage error, exit 3 when `imagehash` or `Pillow` can't be imported.

- [ ] **Step 1: Write the new `bin/phash.py`**

```python
#!/usr/bin/env python3
"""bin/phash.py — perceptual-hash leaf primitive for twincut.

Protocol:
  stdin:  one absolute path per line (or NUL-separated with --null-in).
  stdout: `path\\thash_hex` per successful path (input order preserved).
  stderr: `path\\tERROR\\t<reason>` per failed path.
  exit:   0 ran to completion; 2 usage error; 3 missing pillow/imagehash.
"""

import argparse
import sys


def parse_args(argv):
    p = argparse.ArgumentParser(prog="phash", description="perceptual hash filter")
    p.add_argument("--algo", choices=("dhash", "phash"), default="dhash")
    p.add_argument("--hash-size", type=int, default=8)
    p.add_argument("--null-in", action="store_true",
                   help="stdin paths are NUL-separated")
    return p.parse_args(argv)


def read_paths(null_in):
    if null_in:
        data = sys.stdin.buffer.read()
        for chunk in data.split(b"\x00"):
            if chunk:
                yield chunk.decode("utf-8", "surrogateescape")
    else:
        for line in sys.stdin:
            line = line.rstrip("\n")
            if line:
                yield line


def main(argv):
    args = parse_args(argv)
    try:
        from PIL import Image  # noqa: F401
        import imagehash
    except ImportError as e:
        sys.stderr.write(
            f"phash: missing dependency ({e.name}); "
            f"install with: pip3 install --user pillow imagehash\n"
        )
        return 3

    from PIL import Image as PILImage
    from PIL import UnidentifiedImageError

    hash_fn = imagehash.dhash if args.algo == "dhash" else imagehash.phash

    for path in read_paths(args.null_in):
        try:
            with PILImage.open(path) as im:
                im.load()
                h = hash_fn(im, hash_size=args.hash_size)
            sys.stdout.write(f"{path}\t{h}\n")
            sys.stdout.flush()
        except (UnidentifiedImageError, OSError, ValueError) as e:
            reason = type(e).__name__
            sys.stderr.write(f"{path}\tERROR\t{reason}\n")
            sys.stderr.flush()
        except PILImage.DecompressionBombError:
            sys.stderr.write(f"{path}\tERROR\tDecompressionBombError\n")
            sys.stderr.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
```

Mark executable:

```bash
chmod +x bin/phash.py
```

- [ ] **Step 2: Create the smoke test skeleton with section 0 (deps probe) + section 1 (fixture setup)**

Create `tests/p1_thumb_phash_smoke.sh`:

```bash
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
```

Mark executable:

```bash
chmod +x tests/p1_thumb_phash_smoke.sh
```

- [ ] **Step 3: Run the smoke test to verify section 1 passes**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: `PASS=2 FAIL=0` (section 0 reports availability, section 1 produces a valid dhash). If Pillow/imagehash isn't installed locally, section 1 skips and PASS=2 still.

- [ ] **Step 4: Verify error path manually (one failed file, exit 0)**

```bash
printf '/tmp/this-does-not-exist.png\n' | bin/phash.py 2>/tmp/err; echo "exit=$?"
cat /tmp/err
```

Expected: `exit=0`; stderr contains `/tmp/this-does-not-exist.png\tERROR\tFileNotFoundError`.

- [ ] **Step 5: Verify missing-deps exit 3**

```bash
PYTHONPATH=/dev/null printf '/tmp/x.png\n' | bin/phash.py 2>/tmp/err; echo "exit=$?"
cat /tmp/err
```

Expected: `exit=3`; stderr says `phash: missing dependency (PIL); install with: pip3 install --user pillow imagehash`.

(If your system actually has site-packages on PYTHONPATH, this manual check may pass instead — that's fine; the real test for this path is in T7's smoke section 11.)

- [ ] **Step 6: Commit**

```bash
git add bin/phash.py tests/p1_thumb_phash_smoke.sh
git commit -m "P1 wave 2 T1: bin/phash.py leaf primitive + smoke section 0-1"
```

---

## Task 2: `thumb_run_l1_phash` — index load, hash batch, persist

**Files:**
- Modify: `lib/thumb.sh` (add new function after `thumb_run_l3`, before `thumb_write_review`)
- Modify: `lib/thumb.sh` (wire call site in `thumb_detect_run`)
- Modify: `tests/p1_thumb_phash_smoke.sh` (sections 2–9)

This task adds the index-management half of `thumb_run_l1_phash`: load existing index, drift-check, prune missing, hash new/stale files in one batch via `phash.py`, write back. No pairing yet — that's T3. The function is gated on `THUMB_PHASH_ENABLED=true` (default), but the env init lives in T7; here we hardcode the gate to `true` until T7 adds the proper default.

- [ ] **Step 1: Add fixture-generation section 1.5 to smoke (Pillow gradients) and sections 2, 5–9**

We need fixtures with real pHash signal — solid colors all hash equal, so we use Pillow gradients. Append to `tests/p1_thumb_phash_smoke.sh` (replace the current trailing summary block first; the trailing summary moves to the bottom of the file once we're done adding sections):

```bash
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
# Unrelated big: monochrome noise via gradient on a different axis
im = Image.new("RGB", (2000, 1500), (40, 40, 40))
d = ImageDraw.Draw(im)
for y in range(1500):
    t = y / 1499
    c = (int(40+t*100), int(40+t*100), int(40+t*100))
    d.line([(0, y), (1999, y)], fill=c)
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
  # find the small.jpg event
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
  # Replace algo=dhash with algo=phash to force drift
  sed -i.bak 's/algo=dhash/algo=phash/' "$SRC/.thumb_phash_index.tsv"
  rm -f "$SRC/.thumb_phash_index.tsv.bak"
  LOG9="$TMP/run9.log"
  "$TWINCUT" --thumbnail-detect --dry-run --json-events \
    --source "$SRC" --assume-yes >"$LOG9" 2>&1 || true
  grep -q 'pHash index rebuild (meta drift)' "$LOG9" \
    && ok "section 9: meta drift triggered rebuild" \
    || bad "section 9: no rebuild log line for meta drift"
  # The new header must reflect what we asked for (algo=dhash since env stayed default)
  HEAD9="$(head -n1 "$SRC/.thumb_phash_index.tsv")"
  [[ "$HEAD9" =~ algo=dhash ]] \
    && ok "section 9: header restored to algo=dhash" \
    || bad "section 9: header not rebuilt: '$HEAD9'"
else
  ok "section 9: skipped (no pHash deps)"
fi

printf '\n=========================================\n'
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
```

- [ ] **Step 2: Run the smoke — sections 2/5/6/7/8/9 must FAIL (no code yet)**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: section 0 PASS, section 1 PASS, sections 2/5/6/7/8/9 FAIL (function `thumb_run_l1_phash` doesn't exist). PASS=2, FAIL≥6.

- [ ] **Step 3: Add `thumb_run_l1_phash` (index half) to `lib/thumb.sh`**

Append the following function in `lib/thumb.sh` immediately after `thumb_run_l3` (which ends around line 289) and before `thumb_write_review`:

```bash
# Build / refresh the persistent pHash index, then expose results via the
# three associative arrays declared below. Pairing logic lives in this
# same function (added in Task 3). Failure to compute pHash (missing
# python, missing imagehash, helper exit nonzero) prints a warning and
# returns 0 — thumb_write_review then falls back to today's behavior.
#
# Globals:
#   THUMB_PHASH_INDEX     path to <source>/.thumb_phash_index.tsv (override)
#   THUMB_PHASH_HAMMING   match threshold (default 5; used in T3)
#   THUMB_PHASH_ALGO      dhash|phash (default dhash)
#   THUMB_PHASH_ENABLED   bool (default true)
#   THUMB_INDEX_FILE      (input)  per-run TSV from thumb_build_l1_index
#   SOURCE_DIR            (input)
#   THUMB_PHASH_KEEPER    (output) assoc array: suspect_path → keeper_path
#   THUMB_PHASH_GROUP_ID  (output) assoc array: suspect_path → "l1ph:<sha1>"
#   THUMB_PHASH_DISTANCE  (output) assoc array: suspect_path → Hamming distance
declare -gA THUMB_PHASH_KEEPER 2>/dev/null || true
declare -gA THUMB_PHASH_GROUP_ID 2>/dev/null || true
declare -gA THUMB_PHASH_DISTANCE 2>/dev/null || true

thumb_run_l1_phash(){
  # Stage 8.5+: keep this enabled by default; T7 wires the env default.
  : "${THUMB_PHASH_ENABLED:=true}"
  : "${THUMB_PHASH_HAMMING:=5}"
  : "${THUMB_PHASH_ALGO:=dhash}"
  : "${THUMB_PHASH_HASH_SIZE:=8}"
  : "${THUMB_PHASH_INDEX:="$SOURCE_DIR/.thumb_phash_index.tsv"}"

  if [[ "$THUMB_PHASH_ENABLED" != "true" ]]; then
    echo "[*] L1 pHash disabled by env" >&2
    return 0
  fi

  # Locate phash.py (sibling of twincut.sh in the install)
  local script_dir phash_bin
  script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
  phash_bin="$script_dir/bin/phash.py"
  if [[ ! -x "$phash_bin" ]]; then
    # Fall back to PATH (for installed `phash` symlink)
    if command -v phash >/dev/null 2>&1; then
      phash_bin="$(command -v phash)"
    else
      echo "[!] L1 pHash skipped: bin/phash.py not found" >&2
      return 0
    fi
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    echo "[!] L1 pHash skipped: python3 not found" >&2
    return 0
  fi

  [[ -s "${THUMB_INDEX_FILE:-}" ]] || return 0

  # --- Step A: load existing index into in-memory maps ---
  # Assoc arrays keyed by absolute path.
  local -A INDEX_MTIME INDEX_SIZE INDEX_HASH
  local meta_ok=true
  local idx="$THUMB_PHASH_INDEX"
  if [[ -f "$idx" ]]; then
    local meta_line algo hsize ver
    meta_line="$(head -n1 "$idx" 2>/dev/null || echo "")"
    if [[ ! "$meta_line" =~ ^\#\ meta: ]]; then
      meta_ok=false
    else
      algo="$(printf '%s\n' "$meta_line" | sed -n 's/.*algo=\([^ ]*\).*/\1/p')"
      hsize="$(printf '%s\n' "$meta_line" | sed -n 's/.*hash_size=\([0-9]*\).*/\1/p')"
      ver="$(printf '%s\n' "$meta_line" | sed -n 's/.*version=\([0-9]*\).*/\1/p')"
      if [[ "$algo" != "$THUMB_PHASH_ALGO" \
         || "$hsize" != "$THUMB_PHASH_HASH_SIZE" \
         || "$ver" != "1" ]]; then
        meta_ok=false
      fi
    fi
    if $meta_ok; then
      # Load rows (skip meta line)
      local _p _mt _sz _h
      while IFS=$'\t' read -r _p _mt _sz _h; do
        [[ -z "$_p" || "$_p" == \#* ]] && continue
        [[ "$_mt" =~ ^[0-9]+$ ]] || continue
        [[ "$_sz" =~ ^[0-9]+$ ]] || continue
        [[ "$_h" =~ ^[0-9a-f]+$ ]] || continue
        INDEX_MTIME["$_p"]="$_mt"
        INDEX_SIZE["$_p"]="$_sz"
        INDEX_HASH["$_p"]="$_h"
      done < <(tail -n +2 "$idx")
    else
      echo "[*] pHash index rebuild (meta drift)" >&2
      INDEX_MTIME=(); INDEX_SIZE=(); INDEX_HASH=()
    fi
  fi

  # --- Step B: walk THUMB_INDEX_FILE; determine which files need rehash ---
  local to_hash_file; to_hash_file="$(mktemp)"
  local cache_hits=0 cold=0
  local _f _w _h _cls _live_mt _live_sz
  while IFS=$'\t' read -r _f _w _h _cls; do
    [[ -e "$_f" ]] || continue
    _live_mt="$(stat -f '%m' "$_f" 2>/dev/null || stat -c '%Y' "$_f" 2>/dev/null || echo "")"
    _live_sz="$(stat -f '%z' "$_f" 2>/dev/null || stat -c '%s' "$_f" 2>/dev/null || echo "")"
    [[ -z "$_live_mt" || -z "$_live_sz" ]] && continue
    if [[ -n "${INDEX_HASH[$_f]:-}" \
       && "${INDEX_MTIME[$_f]:-}" == "$_live_mt" \
       && "${INDEX_SIZE[$_f]:-}" == "$_live_sz" ]]; then
      cache_hits=$((cache_hits+1))
    else
      printf '%s\n' "$_f" >> "$to_hash_file"
      cold=$((cold+1))
    fi
  done < "$THUMB_INDEX_FILE"

  # --- Step C: hash the cold set in one batch ---
  if [[ -s "$to_hash_file" ]]; then
    echo "[*] thumbnail-detect L1-pHash: hashing $cold images …" >&2
    local hash_out; hash_out="$(mktemp)"
    if ! python3 "$phash_bin" \
        --algo "$THUMB_PHASH_ALGO" \
        --hash-size "$THUMB_PHASH_HASH_SIZE" \
        < "$to_hash_file" > "$hash_out" 2>/tmp/_phash_err.$$; then
      local rc=$?
      if [[ $rc -eq 3 ]]; then
        echo "[!] L1 pHash skipped: install pillow imagehash" >&2
        cat /tmp/_phash_err.$$ >&2 || true
      else
        echo "[!] L1 pHash skipped: bin/phash.py exited $rc" >&2
      fi
      rm -f /tmp/_phash_err.$$ "$to_hash_file" "$hash_out"
      return 0
    fi
    local _errs; _errs="$(wc -l < /tmp/_phash_err.$$ 2>/dev/null | tr -d ' ')" || _errs=0
    if [[ "${_errs:-0}" -gt 0 ]]; then
      echo "[*] pHash: $_errs files unreadable, see warnings above" >&2
      cat /tmp/_phash_err.$$ >&2 || true
    fi
    rm -f /tmp/_phash_err.$$

    # Ingest new hashes and update mtime/size from live stat
    local _p2 _h2
    while IFS=$'\t' read -r _p2 _h2; do
      [[ -z "$_p2" || -z "$_h2" ]] && continue
      local _mt2 _sz2
      _mt2="$(stat -f '%m' "$_p2" 2>/dev/null || stat -c '%Y' "$_p2" 2>/dev/null)"
      _sz2="$(stat -f '%z' "$_p2" 2>/dev/null || stat -c '%s' "$_p2" 2>/dev/null)"
      INDEX_HASH["$_p2"]="$_h2"
      INDEX_MTIME["$_p2"]="$_mt2"
      INDEX_SIZE["$_p2"]="$_sz2"
    done < "$hash_out"
    rm -f "$hash_out"
  fi
  rm -f "$to_hash_file"

  # --- Step D: prune entries whose files no longer exist ---
  local _k
  for _k in "${!INDEX_HASH[@]}"; do
    [[ ! -e "$_k" ]] && unset 'INDEX_HASH['"$_k"']' 'INDEX_MTIME['"$_k"']' 'INDEX_SIZE['"$_k"']'
  done

  # --- Step E: write index back (atomic via tempfile + mv) ---
  local idx_tmp="$idx.tmp"
  if ! ( printf '# meta: algo=%s hash_size=%s version=1 created=%s\n' \
           "$THUMB_PHASH_ALGO" "$THUMB_PHASH_HASH_SIZE" \
           "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
         && for _k in "${!INDEX_HASH[@]}"; do
              printf '%s\t%s\t%s\t%s\n' \
                "$_k" "${INDEX_MTIME[$_k]}" "${INDEX_SIZE[$_k]}" "${INDEX_HASH[$_k]}"
            done
       ) > "$idx_tmp" 2>/dev/null; then
    echo "[!] cannot write $idx (read-only?); skipping cache" >&2
    rm -f "$idx_tmp"
  else
    mv -f "$idx_tmp" "$idx"
  fi

  echo "[*] thumbnail-detect L1-pHash: cache hits $cache_hits, recomputed $cold (cold or modified)" >&2

  # T3 will append the pairing pass here, populating the THUMB_PHASH_* globals.
  # INDEX_HASH (local to this function) is the in-memory map T3 needs.
}
```

- [ ] **Step 4: Wire the call into `thumb_detect_run`**

In `lib/thumb.sh`, locate `thumb_detect_run` (around line 341). Find the block that calls L1/L2/L3 in sequence:

```bash
  thumb_build_l1_index
  thumb_build_l2_index    # no-op without exiftool
  thumb_run_l2            # no-op without exiftool
  thumb_run_l3            # no-op without exiftool
  thumb_write_review
```

Insert `thumb_run_l1_phash` between `thumb_run_l3` and `thumb_write_review`:

```bash
  thumb_build_l1_index
  thumb_build_l2_index    # no-op without exiftool
  thumb_run_l2            # no-op without exiftool
  thumb_run_l3            # no-op without exiftool
  thumb_run_l1_phash      # adds keeper/group_id metadata to L1 suspects
  thumb_write_review
```

- [ ] **Step 5: Run smoke — sections 5, 6, 7, 8, 9 should now PASS**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: sections 0, 1, 5, 6, 7, 8, 9 PASS. Section 2 still FAILs because pairing isn't wired yet (T3) and `thumb_write_review` doesn't read the assoc arrays (T4).

- [ ] **Step 6: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_phash_smoke.sh
git commit -m "P1 wave 2 T2: thumb_run_l1_phash index load/refresh + smoke 2,5-9"
```

---

## Task 3: Pairing pass in `thumb_run_l1_phash`

**Files:**
- Modify: `lib/thumb.sh` (extend `thumb_run_l1_phash` with pairing logic)
- Modify: `tests/p1_thumb_phash_smoke.sh` (add section 3 — multi-thumb shared keeper, section 4 — orphan stays flat)

T2 left the index built but didn't populate `THUMB_PHASH_KEEPER` / `THUMB_PHASH_GROUP_ID` / `THUMB_PHASH_DISTANCE`. T3 adds the matching loop and tie-breaking rules. T4 makes `thumb_write_review` consume those arrays.

- [ ] **Step 1: Add sections 3 and 4 to smoke (still failing because no pairing emit yet)**

Append to `tests/p1_thumb_phash_smoke.sh` (insert these blocks before the final summary block; or replace the summary block and re-add it at the bottom):

```bash
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
```

- [ ] **Step 2: Run smoke — sections 2, 3, 4 FAIL; section 4 may partially PASS today**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: section 4 has mixed result (event exists from today's unmatched path, but reason/keeper checks may already pass since T1's events are unchanged for unmatched). Sections 2 and 3 FAIL — no `keeper` field appears in any event yet.

- [ ] **Step 3: Add pairing logic to `thumb_run_l1_phash`**

In `lib/thumb.sh`, find the bottom of `thumb_run_l1_phash` (the `T3 will append` comment from T2 step 3) and replace those final lines (from `# T3 will append the pairing pass here…` through the function's closing `}`) with the pairing implementation:

```bash
  # --- Step F: pairing pass ---
  # For each L1=thumb|maybe suspect still on disk, find the nearest L1=ok keeper
  # within Hamming distance THUMB_PHASH_HAMMING. Lexicographically-smallest path
  # wins ties.
  local hamming_max="$THUMB_PHASH_HAMMING"
  if ! [[ "$hamming_max" =~ ^[0-9]+$ ]]; then hamming_max=5; fi
  if (( hamming_max > 64 )); then
    echo "[!] THUMB_PHASH_HAMMING > 64, clipped to 64" >&2
    hamming_max=64
  fi

  # Build keeper bucket = paths with L1=ok in $THUMB_INDEX_FILE that have a hash
  local -a KEEPER_PATHS=()
  local -a KEEPER_HASHES=()
  local _kf _kw _kh _kc
  while IFS=$'\t' read -r _kf _kw _kh _kc; do
    [[ "$_kc" == "ok" ]] || continue
    [[ -n "${INDEX_HASH[$_kf]:-}" ]] || continue
    KEEPER_PATHS+=("$_kf")
    KEEPER_HASHES+=("${INDEX_HASH[$_kf]}")
  done < "$THUMB_INDEX_FILE"

  # Hamming via popcount(XOR) using bash. We unpack hex to two halves of 8 hex
  # chars each = 32 bits per half, fits in bash's 64-bit ints.
  _hamming_hex(){
    local a="$1" b="$2"
    # Pad/truncate to 16 chars
    a="${a:0:16}"
    b="${b:0:16}"
    local a_hi=$((0x${a:0:8})) a_lo=$((0x${a:8:8}))
    local b_hi=$((0x${b:0:8})) b_lo=$((0x${b:8:8}))
    local x_hi=$(( a_hi ^ b_hi )) x_lo=$(( a_lo ^ b_lo ))
    local n=0 v
    for v in "$x_hi" "$x_lo"; do
      while (( v )); do n=$(( n + (v & 1) )); v=$(( v >> 1 )); done
    done
    echo "$n"
  }

  # Walk suspects
  local _sf _sw _sh _sc
  local paired=0 total_suspects=0
  while IFS=$'\t' read -r _sf _sw _sh _sc; do
    [[ "$_sc" == "ok" ]] && continue
    [[ -e "$_sf" ]] || continue
    total_suspects=$(( total_suspects + 1 ))
    local _shash="${INDEX_HASH[$_sf]:-}"
    [[ -z "$_shash" ]] && continue
    local best_keeper="" best_dist=999 best_path=""
    local i
    for (( i=0; i<${#KEEPER_PATHS[@]}; i++ )); do
      local kp="${KEEPER_PATHS[$i]}"
      local kh="${KEEPER_HASHES[$i]}"
      local d
      d=$(_hamming_hex "$_shash" "$kh")
      if (( d <= hamming_max )); then
        if (( d < best_dist )) \
           || { (( d == best_dist )) && [[ -z "$best_path" || "$kp" < "$best_path" ]]; }; then
          best_dist=$d
          best_keeper="$kp"
          best_path="$kp"
        fi
      fi
    done
    if [[ -n "$best_keeper" ]]; then
      local kpath_sha
      kpath_sha="$(printf '%s' "$best_keeper" | (shasum 2>/dev/null || sha1sum) \
                   | awk '{print $1}')"
      THUMB_PHASH_KEEPER["$_sf"]="$best_keeper"
      THUMB_PHASH_GROUP_ID["$_sf"]="l1ph:${kpath_sha:0:16}"
      THUMB_PHASH_DISTANCE["$_sf"]="$best_dist"
      paired=$(( paired + 1 ))
    fi
  done < "$THUMB_INDEX_FILE"

  echo "[*] thumbnail-detect L1-pHash: $paired/$total_suspects suspects paired with keeper (Hamming ≤ $hamming_max)" >&2
}
```

- [ ] **Step 4: Run smoke — sections 2 (matched event) and 3 (shared group_id) still FAIL**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

The pairing populates the assoc arrays but `thumb_write_review` doesn't read them yet → sections 2 and 3 still FAIL. That's the T4 job.

- [ ] **Step 5: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_phash_smoke.sh
git commit -m "P1 wave 2 T3: thumb_run_l1_phash pairing pass + smoke 3-4"
```

---

## Task 4: `thumb_write_review` emits matched L1 events

**Files:**
- Modify: `lib/thumb.sh` (extend events branch of `thumb_write_review`)
- Tests already in place (sections 2, 3, 4 from T2/T3)

The current `thumb_write_review` events branch always emits `reason=l1_only_${cls}` and never includes `keeper`/`group_id`/`phash_distance`. T4 makes it look up the assoc arrays populated by T3 and emit matched-shape events when there's a hit.

- [ ] **Step 1: Modify `thumb_write_review` events branch**

In `lib/thumb.sh`, locate `thumb_write_review` (around line 296). Replace the `$JSON_EVENTS` branch body (currently from `if $JSON_EVENTS; then` through the `return 0` just before the legacy CSV write):

```bash
  if $JSON_EVENTS; then
    local f w h cls _sz
    while IFS=$'\t' read -r f w h cls; do
      [[ "$cls" == "ok" ]] && continue
      [[ ! -e "$f" ]] && continue
      _sz="$(wc -c < "$f" 2>/dev/null | tr -d ' ')" || _sz=0

      local keeper="${THUMB_PHASH_KEEPER[$f]:-}"
      local group_id="${THUMB_PHASH_GROUP_ID[$f]:-}"
      local distance="${THUMB_PHASH_DISTANCE[$f]:-}"

      if [[ -n "$keeper" ]]; then
        emit_event "thumb_candidate" \
          "decision=thumb_l1_review" \
          "path=$f" \
          "keeper=$keeper" \
          "group_id=$group_id" \
          "reason=l1_phash_match" \
          "width=@${w:-0}" \
          "height=@${h:-0}" \
          "size_bytes=@${_sz:-0}" \
          "phash_distance=@${distance:-0}"
      else
        emit_event "thumb_candidate" \
          "decision=thumb_l1_review" \
          "path=$f" \
          "reason=l1_only_${cls}" \
          "width=@${w:-0}" \
          "height=@${h:-0}" \
          "size_bytes=@${_sz:-0}"
      fi
      THUMB_REVIEW_CNT=$((THUMB_REVIEW_CNT+1))
    done < "$THUMB_INDEX_FILE"

    if (( THUMB_REVIEW_CNT > 0 )); then
      echo "[*] L1-only suspects emitted as events: $THUMB_REVIEW_CNT"
    fi
    return 0
  fi
```

(The legacy non-events branch below this is unchanged — the `_review.csv` path doesn't carry keeper info; that's spec out-of-scope.)

- [ ] **Step 2: Run smoke — sections 2 and 3 must now PASS**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: sections 0–9 all PASS. PASS≥13, FAIL=0.

- [ ] **Step 3: Spot-check raw events**

Eyeball one matched event manually to make sure the wire format is right:

```bash
TMP_SRC="/tmp/twincut-phash-eyeball"
rm -rf "$TMP_SRC"; mkdir -p "$TMP_SRC"
python3 - "$TMP_SRC" <<'PY'
import sys, os
from PIL import Image, ImageDraw
src = sys.argv[1]
for name, w, h in [("big.jpg", 2000, 1500), ("small.jpg", 200, 150)]:
    im = Image.new("RGB", (w, h))
    d = ImageDraw.Draw(im)
    for x in range(w):
        c = (int(255*x/w), 30, 200-int(180*x/w))
        d.line([(x, 0), (x, h)], fill=c)
    im.save(os.path.join(src, name), "JPEG", quality=88)
PY
bin/twincut.sh --thumbnail-detect --dry-run --json-events \
  --source "$TMP_SRC" --assume-yes 2>/dev/null \
  | grep '"decision":"thumb_l1_review"'
```

Expected: a single matched event with all of `keeper`, `group_id` (`l1ph:` prefix), `reason=l1_phash_match`, and `phash_distance` as integer.

- [ ] **Step 4: Commit**

```bash
git add lib/thumb.sh
git commit -m "P1 wave 2 T4: thumb_write_review emits keeper/group_id on pHash match"
```

---

## Task 5: Go-side `ThumbCandidate.PhashDistance`

**Files:**
- Modify: `ui/server/events.go` (`ThumbCandidate` struct + doc comment)
- Modify: `ui/server/events_test.go` (parse `phash_distance`)

The Go event parser needs to surface the new `phash_distance` field so downstream code (T6, UI templates) can read it. `omitempty` keeps existing unmatched events parsing cleanly with zero value.

- [ ] **Step 1: Write the failing test**

Add to `ui/server/events_test.go` (append to end of file):

```go
func TestUnmarshalThumbCandidate_L1WithPhashFields(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000020,"run_id":"r1","decision":"thumb_l1_review","path":"/src/small.jpg","keeper":"/src/big.jpg","group_id":"l1ph:abcdef0123456789","reason":"l1_phash_match","width":200,"height":150,"size_bytes":4096,"phash_distance":3}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if ev.Type != EventThumbCandidate {
		t.Fatalf("Type = %q, want %q", ev.Type, EventThumbCandidate)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
		t.Fatalf("UnmarshalThumbCandidate: %v", err)
	}
	if tc.Decision != "thumb_l1_review" {
		t.Errorf("Decision = %q, want thumb_l1_review", tc.Decision)
	}
	if tc.Keeper != "/src/big.jpg" {
		t.Errorf("Keeper = %q, want /src/big.jpg", tc.Keeper)
	}
	if tc.GroupID != "l1ph:abcdef0123456789" {
		t.Errorf("GroupID = %q, want l1ph:abcdef0123456789", tc.GroupID)
	}
	if tc.Reason != "l1_phash_match" {
		t.Errorf("Reason = %q, want l1_phash_match", tc.Reason)
	}
	if tc.PhashDistance != 3 {
		t.Errorf("PhashDistance = %d, want 3", tc.PhashDistance)
	}
}
```

- [ ] **Step 2: Run test — expect compile error (PhashDistance field doesn't exist)**

```bash
cd ui && go test ./server/ -run TestUnmarshalThumbCandidate_L1WithPhashFields -v
```

Expected: build error `unknown field 'PhashDistance' in struct literal of type ThumbCandidate`. Or, if accessed only on the parsed value, the test compiles but fails because the field is always 0.

- [ ] **Step 3: Add `PhashDistance` to `ThumbCandidate`**

Modify `ui/server/events.go`. Find the `ThumbCandidate` struct (around line 79) and add a new field after `SizeBytes`:

```go
// ThumbCandidate is the parsed payload of a "thumb_candidate" event emitted
// by lib/thumb.sh during --dry-run --json-events. One event per candidate file.
type ThumbCandidate struct {
	Decision      string `json:"decision"`        // thumb_l2_exif | thumb_l3_embed | thumb_l1_review
	Path          string `json:"path"`            // absolute path of the candidate thumbnail
	Keeper        string `json:"keeper"`          // absolute path of the file being kept (L2/L3 always; L1 only when pHash matched)
	GroupID       string `json:"group_id"`        // L2: EXIF SHA1; L3: "l3:<sha1>"; L1 matched: "l1ph:<sha1>"; absent for L1 unmatched
	Reason        string `json:"reason"`          // L1 unmatched: "l1_only_thumb"|"l1_only_maybe"; L1 matched: "l1_phash_match"; empty for L2/L3
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	SizeBytes     int64  `json:"size_bytes"`
	PhashDistance int    `json:"phash_distance,omitempty"` // L1 matched only: Hamming distance to keeper (0..64 for hash_size=8)
}
```

- [ ] **Step 4: Run the test — must PASS**

```bash
cd ui && go test ./server/ -run TestUnmarshalThumbCandidate_L1WithPhashFields -v
```

Expected: PASS.

- [ ] **Step 5: Make sure the existing events tests still pass**

```bash
cd ui && go test ./server/ -run TestUnmarshalThumbCandidate -v
```

Expected: all four (L2, L3, L1-old-shape, and new) PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/server/events.go ui/server/events_test.go
git commit -m "P1 wave 2 T5: ThumbCandidate.PhashDistance field"
```

---

## Task 6: Go-side matched-vs-unmatched L1 routing

**Files:**
- Modify: `ui/server/results.go` (`EventThumbCandidate` branch + `ResultMember`)
- Modify: `ui/server/results_test.go` (matched-L1 own-group, multi-suspect merge, unmatched fallback)

Currently `results.go` routes every `thumb_l1_review` event into the synthetic `l1-suspects` group. Wave 2 splits the decision: empty `group_id` → synthetic group (old behavior); non-empty `group_id` → find-or-create that group (mirror L2/L3).

- [ ] **Step 1: Write the failing tests**

Append to `ui/server/results_test.go`:

```go
func TestBuildResults_L1Phash_MatchedGoesToOwnGroup(t *testing.T) {
	srcDir := t.TempDir()
	runID := "20260521T180000Z-wave2t6a"
	stateDir := t.TempDir()
	journal := writeJournal(t, stateDir, runID, []string{
		`{"type":"run_start","ts":1,"run_id":"` + runID + `","mode":"thumbnail_detect_preview","args":["bin/twincut.sh"]}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"` + srcDir + `/small.jpg","keeper":"` + srcDir + `/big.jpg","group_id":"l1ph:deadbeefcafef00d","reason":"l1_phash_match","width":200,"height":150,"size_bytes":4096,"phash_distance":2}`,
		`{"type":"run_end","ts":3,"run_id":"` + runID + `","status":"succeeded"}`,
	})
	view, err := BuildResults(journal)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	var matched *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1ph:deadbeefcafef00d" {
			matched = &view.Groups[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("expected group l1ph:deadbeefcafef00d, groups=%+v", view.Groups)
	}
	if len(matched.Members) != 1 {
		t.Fatalf("Members len = %d, want 1", len(matched.Members))
	}
	if matched.Members[0].Keeper != srcDir+"/big.jpg" {
		t.Errorf("Member.Keeper = %q, want big.jpg path", matched.Members[0].Keeper)
	}
	if matched.Members[0].PhashDistance != 2 {
		t.Errorf("Member.PhashDistance = %d, want 2", matched.Members[0].PhashDistance)
	}
	// The synthetic l1-suspects group should NOT have been created
	for _, g := range view.Groups {
		if g.StringGroupID == "l1-suspects" {
			t.Errorf("matched L1 unexpectedly created l1-suspects group: %+v", g)
		}
	}
}

func TestBuildResults_L1Phash_UnmatchedStaysInSyntheticGroup(t *testing.T) {
	srcDir := t.TempDir()
	runID := "20260521T180000Z-wave2t6b"
	stateDir := t.TempDir()
	journal := writeJournal(t, stateDir, runID, []string{
		`{"type":"run_start","ts":1,"run_id":"` + runID + `","mode":"thumbnail_detect_preview","args":["bin/twincut.sh"]}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"` + srcDir + `/orphan.png","reason":"l1_only_thumb","width":100,"height":100,"size_bytes":2048}`,
		`{"type":"run_end","ts":3,"run_id":"` + runID + `","status":"succeeded"}`,
	})
	view, err := BuildResults(journal)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	var synthetic *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1-suspects" {
			synthetic = &view.Groups[i]
		}
	}
	if synthetic == nil {
		t.Fatalf("expected synthetic l1-suspects group; groups=%+v", view.Groups)
	}
	if len(synthetic.Members) != 1 || synthetic.Members[0].Path != srcDir+"/orphan.png" {
		t.Errorf("synthetic Members = %+v, want one orphan", synthetic.Members)
	}
}

func TestBuildResults_L1Phash_MultipleSuspectsShareKeeper(t *testing.T) {
	srcDir := t.TempDir()
	runID := "20260521T180000Z-wave2t6c"
	stateDir := t.TempDir()
	journal := writeJournal(t, stateDir, runID, []string{
		`{"type":"run_start","ts":1,"run_id":"` + runID + `","mode":"thumbnail_detect_preview","args":["bin/twincut.sh"]}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"` + srcDir + `/thumb1.jpg","keeper":"` + srcDir + `/big.jpg","group_id":"l1ph:aaaa1111bbbb2222","reason":"l1_phash_match","width":300,"height":225,"size_bytes":3000,"phash_distance":1}`,
		`{"type":"thumb_candidate","ts":3,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"` + srcDir + `/thumb2.jpg","keeper":"` + srcDir + `/big.jpg","group_id":"l1ph:aaaa1111bbbb2222","reason":"l1_phash_match","width":150,"height":113,"size_bytes":1500,"phash_distance":2}`,
		`{"type":"run_end","ts":4,"run_id":"` + runID + `","status":"succeeded"}`,
	})
	view, err := BuildResults(journal)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	var g *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1ph:aaaa1111bbbb2222" {
			g = &view.Groups[i]
		}
	}
	if g == nil {
		t.Fatalf("expected merged group; groups=%+v", view.Groups)
	}
	if len(g.Members) != 2 {
		t.Fatalf("Members len = %d, want 2 (merged)", len(g.Members))
	}
}
```

(The `writeJournal` helper already exists in `ui/server/results_test.go` from Stage 8.5; reuse it.)

- [ ] **Step 2: Run tests — expect FAIL (matched L1 still routes to synthetic group)**

```bash
cd ui && go test ./server/ -run TestBuildResults_L1Phash -v
```

Expected: all three FAIL — matched suspects still get aggregated into `l1-suspects`.

- [ ] **Step 3: Add `PhashDistance` to `ResultMember`**

In `ui/server/results.go`, find the `ResultMember` struct definition. Add `PhashDistance` after `Keeper`:

```go
type ResultMember struct {
	Path          string `json:"path"`
	Size          int64  `json:"size,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	Note          string `json:"note,omitempty"`
	Keeper        string `json:"keeper,omitempty"`
	PhashDistance int    `json:"phash_distance,omitempty"`
}
```

(Use exact field set already present in the file; only `PhashDistance` is new.)

- [ ] **Step 4: Split the L1 routing in `EventThumbCandidate` branch**

In `ui/server/results.go::BuildResults`, locate the `case EventThumbCandidate:` block (around line 188). The current code force-merges all L1 events into the synthetic `l1-suspects` group. Replace the L1 branch so that empty `GroupID` still flows to the synthetic group, but non-empty `GroupID` flows through the same matched-by-id path as L2/L3.

Find this code (the existing L1 synthetic-group merge):

```go
case EventThumbCandidate:
    var tc ThumbCandidate
    if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
        view.Warnings = append(view.Warnings, ResultWarn{
            Code:   "bad_thumb_candidate",
            Detail: err.Error(),
        })
        continue
    }
    // L1 review events with no group_id are aggregated into a single
    // synthetic "l1-suspects" group; the apply path emits them as a single
    // batch.
    if tc.Decision == "thumb_l1_review" {
        var gi int = -1
        for i := range view.Groups {
            if view.Groups[i].StringGroupID == "l1-suspects" {
                gi = i
                break
            }
        }
        if gi == -1 {
            view.Groups = append(view.Groups, ResultGroup{StringGroupID: "l1-suspects"})
            gi = len(view.Groups) - 1
        }
        view.Groups[gi].Members = append(view.Groups[gi].Members, ResultMember{
            // existing member fields (path/size/width/height/note)
        })
        continue
    }
    // ... L2/L3 path follows
```

Replace the `if tc.Decision == "thumb_l1_review"` block with:

```go
    // L1 review with empty group_id → synthetic "l1-suspects" group.
    // L1 review with a group_id (pHash matched) → its own group, same path as L2/L3.
    if tc.Decision == "thumb_l1_review" && tc.GroupID == "" {
        var gi int = -1
        for i := range view.Groups {
            if view.Groups[i].StringGroupID == "l1-suspects" {
                gi = i
                break
            }
        }
        if gi == -1 {
            view.Groups = append(view.Groups, ResultGroup{StringGroupID: "l1-suspects"})
            gi = len(view.Groups) - 1
        }
        view.Groups[gi].Members = append(view.Groups[gi].Members, ResultMember{
            Path:   tc.Path,
            Size:   tc.SizeBytes,
            Width:  tc.Width,
            Height: tc.Height,
            Note:   tc.Reason,
        })
        continue
    }
    // L2/L3, or matched L1: find-or-create by GroupID. Same code path
    // for all three because the wire shape is identical.
    var gi int = -1
    for i := range view.Groups {
        if view.Groups[i].StringGroupID == tc.GroupID {
            gi = i
            break
        }
    }
    if gi == -1 {
        view.Groups = append(view.Groups, ResultGroup{StringGroupID: tc.GroupID})
        gi = len(view.Groups) - 1
    }
    view.Groups[gi].Members = append(view.Groups[gi].Members, ResultMember{
        Path:          tc.Path,
        Size:          tc.SizeBytes,
        Width:         tc.Width,
        Height:        tc.Height,
        Note:          tc.Reason,
        Keeper:        tc.Keeper,
        PhashDistance: tc.PhashDistance,
    })
```

(If the existing matched-by-GroupID code is already implemented for L2/L3 elsewhere in this branch, then keep that — just make sure the new matched-L1 path lands in the same place. The above shows the merged form for clarity.)

- [ ] **Step 5: Run tests — must PASS**

```bash
cd ui && go test ./server/ -run TestBuildResults_L1Phash -v
```

Expected: all three PASS. Plus, make sure existing tests didn't regress:

```bash
cd ui && go test ./server/ -v
```

Expected: full suite green.

- [ ] **Step 6: Commit**

```bash
git add ui/server/results.go ui/server/results_test.go
git commit -m "P1 wave 2 T6: matched L1 → own group via group_id; unmatched → synthetic"
```

---

## Task 7: Failure modes — env knob + missing-deps graceful skip

**Files:**
- Modify: `bin/twincut.sh` (init defaults for new env vars)
- Modify: `tests/p1_thumb_phash_smoke.sh` (sections 10, 11, 12)

The function already has skip-on-missing-deps and `THUMB_PHASH_ENABLED=false` handling from T2. T7 adds the test coverage for those paths plus the legacy CLI section, and wires the env knob defaults at startup (so they're visible to bash arg parsing if we ever expose them as flags).

- [ ] **Step 1: Add env defaults to `bin/twincut.sh`**

Locate the defaults block at the top of `bin/twincut.sh` (around lines 7–100; per project convention, `THUMB_*` defaults are colocated there). Find the existing thumbnail-related defaults (`THUMB_MAX_EDGE`, `THUMB_MAYBE_MAX_EDGE`, `THUMB_ACTION`, etc.) and append below them:

```bash
# P1 wave 2: L1 perceptual-hash knobs (env-only, no CLI flag)
THUMB_PHASH_ENABLED="${THUMB_PHASH_ENABLED:-true}"
THUMB_PHASH_HAMMING="${THUMB_PHASH_HAMMING:-5}"
THUMB_PHASH_ALGO="${THUMB_PHASH_ALGO:-dhash}"
THUMB_PHASH_HASH_SIZE="${THUMB_PHASH_HASH_SIZE:-8}"
```

(Exact placement: after `THUMB_ACTION=...` or whatever the last `THUMB_*` default is. The `${VAR:-default}` form lets the caller override via environment.)

- [ ] **Step 2: Add sections 10, 11, 12 to smoke**

Append to `tests/p1_thumb_phash_smoke.sh` (before the final summary):

```bash
# ---------------------------------------------------------------------
# Section 10: THUMB_PHASH_ENABLED=false skips the phase silently
# ---------------------------------------------------------------------
note "10. THUMB_PHASH_ENABLED=false → no phase, no index, fallback to flat L1"
rm -rf "$SRC"; mkdir -p "$SRC"
if $HAVE_PHASH_DEPS; then gen_fixtures; else
  SEED="/System/Library/Desktop Pictures/Solid Colors/Black.png"
  [[ -f "$SEED" ]] || SEED="/System/Library/Desktop Pictures/Solid Colors/Stone.png"
  sips -z 2000 1500 "$SEED" --out "$SRC/big.png" >/dev/null
  sips -z 200 150 "$SEED" --out "$SRC/small.png" >/dev/null
fi
LOG10="$TMP/run10.log"
THUMB_PHASH_ENABLED=false "$TWINCUT" --thumbnail-detect --dry-run --json-events \
  --source "$SRC" --assume-yes >"$LOG10" 2>&1 || true
grep -q "L1 pHash disabled by env" "$LOG10" \
  && ok "section 10: env-disable message present" \
  || bad "section 10: env-disable message missing"
assert_not_file "$SRC/.thumb_phash_index.tsv"
# Suspects still emit (without keeper)
grep -q '"decision":"thumb_l1_review"' "$LOG10" \
  && ok "section 10: L1 suspects still emit when phash disabled" \
  || bad "section 10: no L1 events when phash disabled"
grep '"decision":"thumb_l1_review"' "$LOG10" | grep -q '"keeper":' \
  && bad "section 10: keeper field appeared while phash disabled" \
  || ok "section 10: no keeper field on events (phash disabled)"

# ---------------------------------------------------------------------
# Section 11: simulated deps failure (fake phash.py exits 3) → graceful
# ---------------------------------------------------------------------
note "11. simulated dependency failure exits 0 and skips gracefully"
FAKE_BIN="$TMP/fake_bin"; mkdir -p "$FAKE_BIN"
cat > "$FAKE_BIN/phash.py" <<'FAKE'
#!/usr/bin/env python3
import sys; sys.stderr.write("phash: missing dependency (PIL); install with: pip3 install --user pillow imagehash\n"); sys.exit(3)
FAKE
chmod +x "$FAKE_BIN/phash.py"
# Temporarily shadow the real phash.py
ORIG_PHASH="$ROOT/bin/phash.py"
mv "$ORIG_PHASH" "$ORIG_PHASH.real"
cp "$FAKE_BIN/phash.py" "$ORIG_PHASH"

rm -rf "$SRC"; mkdir -p "$SRC"
if $HAVE_PHASH_DEPS; then gen_fixtures; else
  SEED="/System/Library/Desktop Pictures/Solid Colors/Black.png"
  [[ -f "$SEED" ]] || SEED="/System/Library/Desktop Pictures/Solid Colors/Stone.png"
  sips -z 2000 1500 "$SEED" --out "$SRC/big.png" >/dev/null
  sips -z 200 150 "$SEED" --out "$SRC/small.png" >/dev/null
fi
LOG11="$TMP/run11.log"
"$TWINCUT" --thumbnail-detect --dry-run --json-events \
  --source "$SRC" --assume-yes >"$LOG11" 2>&1
RC=$?
# Restore real phash.py
mv "$ORIG_PHASH.real" "$ORIG_PHASH"

[[ "$RC" -eq 0 ]] \
  && ok "section 11: twincut exit 0 despite phash.py exit 3" \
  || bad "section 11: twincut exit $RC, expected 0"
grep -q "L1 pHash skipped" "$LOG11" \
  && ok "section 11: skip warning logged" \
  || bad "section 11: skip warning missing"
# L1 events still emit, without keeper
grep '"decision":"thumb_l1_review"' "$LOG11" | grep -q '"keeper":' \
  && bad "section 11: keeper appeared despite phash skip" \
  || ok "section 11: no keeper on events after phash skip"

# ---------------------------------------------------------------------
# Section 12: legacy CLI path (no --json-events) still writes _review.csv
# ---------------------------------------------------------------------
note "12. legacy CLI writes _review.csv as before"
rm -rf "$SRC"; mkdir -p "$SRC"
SEED="/System/Library/Desktop Pictures/Solid Colors/Black.png"
[[ -f "$SEED" ]] || SEED="/System/Library/Desktop Pictures/Solid Colors/Stone.png"
sips -z 200 200 "$SEED" --out "$SRC/orphan.png" >/dev/null
LOG12="$TMP/run12.log"
"$TWINCUT" --thumbnail-detect --dry-run \
  --source "$SRC" --assume-yes >"$LOG12" 2>&1 || true
# _review.csv must exist (legacy on-disk path)
assert_file "$SRC/_thumbnails/_review.csv"
# Index file: pHash phase may or may not run depending on deps; in both
# cases the legacy CSV path is the test target.
ok "section 12: legacy CSV path preserved"
```

- [ ] **Step 3: Run smoke — sections 10/11/12 must PASS**

```bash
bash tests/p1_thumb_phash_smoke.sh
```

Expected: all sections 0–12 PASS. PASS≥20, FAIL=0.

- [ ] **Step 4: Commit**

```bash
git add bin/twincut.sh tests/p1_thumb_phash_smoke.sh
git commit -m "P1 wave 2 T7: env defaults + smoke 10-12 (disabled/skip/legacy)"
```

---

## Task 8: Installer, gitignore, CLAUDE.md

**Files:**
- Modify: `installers/install.sh`
- Modify: `installers/uninstall.sh`
- Modify: `.gitignore`
- Modify: `CLAUDE.md`

Wraps up the user-facing surface: the install script symlinks `phash` and best-effort `pip install` the Python deps; the index file is gitignored; CLAUDE.md notes the new optional runtime deps.

- [ ] **Step 1: Modify `installers/install.sh`**

Read the current contents first to find the right insertion point:

```bash
cat installers/install.sh
```

Find the section that creates the `twincut` and `vid_eq` symlinks. After the `vid_eq` symlink line, add:

```bash
# P1 wave 2: phash leaf primitive
ln -sfn "$REPO/bin/phash.py" "$HOME/.local/bin/phash"
echo "→ symlinked phash"

# Best-effort: install python deps for L1 perceptual hash.
# Failure is non-fatal — runtime will warn and skip the pHash phase.
if command -v pip3 >/dev/null 2>&1; then
  if pip3 install --user --quiet pillow imagehash 2>/dev/null; then
    echo "→ pip3 installed pillow + imagehash (L1 pHash pairing enabled)"
  else
    echo "[!] pip3 install pillow imagehash failed; L1 pHash pairing will be skipped at runtime"
    echo "    you can retry manually: pip3 install --user pillow imagehash"
  fi
else
  echo "[!] pip3 not found; for L1 pHash pairing, install python3 then:"
  echo "    pip3 install --user pillow imagehash"
fi
```

(Locate `$REPO` and `$HOME/.local/bin` from how the existing twincut/vid_eq symlinks are done in this file; match the variable names already in use.)

- [ ] **Step 2: Modify `installers/uninstall.sh`**

Read the current contents:

```bash
cat installers/uninstall.sh
```

Find the section that removes the twincut/vid_eq symlinks. Add after them:

```bash
rm -f "$HOME/.local/bin/phash" && echo "→ removed phash symlink"
# Note: we do NOT pip-uninstall pillow / imagehash — user may rely on them.
```

- [ ] **Step 3: Modify `.gitignore`**

Append to `.gitignore`:

```
# P1 wave 2: per-source pHash cache
.thumb_phash_index.tsv
.thumb_phash_index.tsv.tmp
```

- [ ] **Step 4: Modify `CLAUDE.md`**

Find the "External runtime deps" line near the top of the "Repository overview" section. Currently:

```
External runtime deps: `bash`, `ffprobe`/`ffmpeg`, standard coreutils, `md5`/`sha1` tooling.
```

Replace with:

```
External runtime deps: `bash`, `ffprobe`/`ffmpeg`, standard coreutils, `md5`/`sha1` tooling. Optional for L1 perceptual-hash pairing (P1 wave 2): `python3 ≥ 3.8`, `Pillow ≥ 9.0`, `imagehash ≥ 4.3` — install via `pip3 install --user pillow imagehash`. Without them, L1 falls back to flat suspect-list behavior.
```

- [ ] **Step 5: Verify install/uninstall don't blow up (dry-run if possible)**

```bash
# Read both scripts and verify they still parse with bash -n
bash -n installers/install.sh && echo "install.sh syntax OK"
bash -n installers/uninstall.sh && echo "uninstall.sh syntax OK"
```

Expected: both syntax-OK lines printed.

- [ ] **Step 6: Run the full smoke once more to make sure nothing regressed**

```bash
bash tests/p1_thumb_phash_smoke.sh
cd ui && go test ./server/ -v
```

Expected: smoke PASS≥20 FAIL=0; Go test suite green.

- [ ] **Step 7: Commit**

```bash
git add installers/install.sh installers/uninstall.sh .gitignore CLAUDE.md
git commit -m "P1 wave 2 T8: installer + gitignore + CLAUDE.md notes"
```

---

## Spec coverage check

| Spec section | Where implemented |
|---|---|
| §1 `bin/phash.py` contract (stdin/stdout, exits, error handling) | T1 |
| §2 Persistent index schema + drift handling | T2 (steps 3, smoke 5/6/7/8/9) |
| §2 Env knobs (`THUMB_PHASH_ENABLED`, `_HAMMING`, `_ALGO`, `_INDEX`) | T2 defaults; T7 startup init |
| §3 Pairing algorithm + tie-breaking | T3 |
| §3 `group_id` derivation (`l1ph:<sha1>`) | T3 |
| §3 Multi-suspect → one group | T3 + T6 + smoke section 3 |
| §3 Unmatched suspect events unchanged | T4 (preserves else-branch) + smoke section 4 |
| §3 `phash_distance` event field | T4 (emit) + T5 (Go parse) |
| §3 `reason=l1_phash_match` | T4 + smoke section 2 |
| §3 Go `EventThumbCandidate` routing split | T6 |
| §3 `ResultMember.PhashDistance` | T6 |
| §4 Installer changes | T8 |
| §4 Failure mode table (python missing, helper missing, deps missing, corrupt index, etc.) | T2 (skip paths) + T7 (smoke 10/11) |
| §4 Test sections 1–12 | smoke section numbering matches: T1→0–1, T2→2/5–9, T3→3–4, T7→10–12 |
| §4 Go test additions | T5 (events) + T6 (results) |

## Out of scope (deferred — do NOT add to this plan)

- No `--phash-*` CLI flags. Env-only.
- No auto-move on high-confidence pHash match. L1 stays review tier.
- No HEIF special-casing — Pillow either reads it or emits ERROR; failure mode table covers both.
- No numpy / BK-tree / multiprocessing.
- No removal of legacy on-disk `_review.csv` path.
- No UI visual differentiation between L1-pHash groups and L2/L3 groups beyond what `PhashDistance` makes possible incidentally.
- No cross-source (source↔backup) pHash. Single-source only.
