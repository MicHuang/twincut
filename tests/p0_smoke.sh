#!/usr/bin/env bash
# tests/p0_smoke.sh — end-to-end smoke test for the P0 changes.
#
# Covers:
#   1. cross-dupe move + manifest row
#   2. case-insensitive extension match (.JPG)
#   3. hardlink detection (skipped, not moved)
#   4. symlink not followed by default
#   5. dry-run writes .dryrun manifest, source untouched
#   6. --restore moves quarantined files back; conflict skipped
#   7. --exit-code-on-dupes returns 1 when dupes were processed
#   8. AppleDouble (._*) sidecar handled and recorded
#
# This test only uses image extensions and --exact (no ffprobe path required).

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
TWINCUT="$ROOT/bin/twincut.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

SRC="$TMP/src"
BK="$TMP/backup"
QUAR="$TMP/quarantine"
mkdir -p "$SRC" "$BK"

PASS=0
FAIL=0
note(){ printf '\n=== %s ===\n' "$*"; }
ok(){   printf '  ok   %s\n' "$*"; PASS=$((PASS+1)); }
bad(){  printf '  FAIL %s\n' "$*"; FAIL=$((FAIL+1)); }
assert_eq(){ [[ "$1" == "$2" ]] && ok "$3 ($1)" || bad "$3 (got '$1', want '$2')"; }
assert_file(){     [[ -e "$1" ]] && ok "exists: $1"     || bad "missing: $1"; }
assert_not_file(){ [[ ! -e "$1" ]] && ok "absent: $1"   || bad "still there: $1"; }

# ---- fixtures ----
# 1: identical content in both src and backup → cross-dupe
echo "alpha-content" > "$BK/a.jpg"
cp "$BK/a.jpg" "$SRC/a.jpg"

# 2: case-insensitive extension
echo "beta-content" > "$BK/b.JPG"
cp "$BK/b.JPG" "$SRC/b.JPG"

# 3: hardlink — same inode in src and backup
echo "gamma-content" > "$BK/c.jpg"
ln "$BK/c.jpg" "$SRC/c.jpg"

# 4: symlink — src/d.jpg is a symlink to backup/d.jpg
echo "delta-content" > "$BK/d.jpg"
ln -s "$BK/d.jpg" "$SRC/d.jpg"

# 5: unique source file (no match) — must be left alone
echo "unique-zeta" > "$SRC/zeta.jpg"

# 6: AppleDouble sidecar
printf '\x00\x05\x16\x07AppleDouble' > "$SRC/._a.jpg"

# Snapshot src for later diff
SNAP="$TMP/snap"; cp -R "$SRC" "$SNAP"

# ----------------------------------------------------------------------------
note "1. dry-run leaves source untouched and writes .dryrun manifest"
"$TWINCUT" --source "$SRC" --backup "$BK" --quarantine "$QUAR" \
  --ext "jpg" --exact --dry-run --assume-yes \
  --no-bad-video --appledouble-action move >/tmp/twincut_dryrun.log 2>&1 || true

# src files must equal snapshot (ignore the source cache file that dry-run intentionally writes)
if diff -rq --exclude='.source_hashindex.txt' "$SNAP" "$SRC" >/dev/null; then ok "src files unchanged after dry-run"
else bad "src files changed in dry-run"; diff -rq --exclude='.source_hashindex.txt' "$SNAP" "$SRC" || true; fi

DRY_MF=$(ls "$QUAR"/_manifest-*.dryrun.tsv 2>/dev/null | head -n1 || true)
[[ -n "$DRY_MF" ]] && ok "dry-run manifest exists: $(basename "$DRY_MF")" || bad "no dry-run manifest"

# ----------------------------------------------------------------------------
note "2. real run: cross-dupe + case-insensitive + hardlink-skip + symlink-skip"
set +e
"$TWINCUT" --source "$SRC" --backup "$BK" --quarantine "$QUAR" \
  --ext "jpg" --exact --assume-yes \
  --no-bad-video --appledouble-action move \
  --exit-code-on-dupes >/tmp/twincut_run.log 2>&1
RC=$?
set -e

assert_eq "$RC" "1" "exit code 1 with --exit-code-on-dupes"

# a.jpg + b.JPG should be moved
assert_not_file "$SRC/a.jpg"
assert_not_file "$SRC/b.JPG"
assert_file "$QUAR/a.jpg"
assert_file "$QUAR/b.JPG"

# c.jpg is a hardlink → must be skipped (still present in src)
assert_file "$SRC/c.jpg"
grep -q "hardlink-skip" /tmp/twincut_run.log && ok "hardlink-skip logged" || bad "no hardlink-skip log"

# d.jpg is a symlink → not followed → must still be present
assert_file "$SRC/d.jpg"

# zeta unique → still present
assert_file "$SRC/zeta.jpg"

# AppleDouble moved
assert_not_file "$SRC/._a.jpg"
assert_file "$QUAR/_appledouble/._a.jpg"

# Real manifest exists and contains expected rows
REAL_MF=""
for _mf in "$QUAR"/_manifest-*.tsv; do
  [[ -e "$_mf" ]] || continue
  [[ "$_mf" == *dryrun* ]] && continue
  REAL_MF="$_mf"
  break
done
[[ -n "$REAL_MF" ]] && ok "real manifest exists: $(basename "$REAL_MF")" || bad "no real manifest"
grep -q $'\tcross_hash\t' "$REAL_MF"  && ok "manifest has cross_hash row"  || bad "no cross_hash row"
grep -q $'\tappledouble\t' "$REAL_MF" && ok "manifest has appledouble row" || bad "no appledouble row"

# ----------------------------------------------------------------------------
note "3. --restore puts files back"
"$TWINCUT" --restore "$REAL_MF" --assume-yes --json-events >/tmp/twincut_restore_events.ndjson 2>/dev/null
assert_file "$SRC/a.jpg"
assert_file "$SRC/b.JPG"
assert_file "$SRC/._a.jpg"
assert_not_file "$QUAR/a.jpg"
grep -q '"type":"run_start".*"mode":"restore"' /tmp/twincut_restore_events.ndjson \
  && ok "restore emits typed run_start" || bad "restore missing typed run_start"
grep -q '"type":"run_end".*"restored":' /tmp/twincut_restore_events.ndjson \
  && ok "restore emits typed run_end with restored count" || bad "restore missing typed run_end"

# ----------------------------------------------------------------------------
note "4. --restore conflict: original already exists → skip, no overwrite"
# Create a conflicting file at the original path, then re-run twincut → restore again
cp "$BK/a.jpg" "$SRC/a.jpg.keep"  # different name; ensure restore safe path
# First, run again to populate quarantine
"$TWINCUT" --source "$SRC" --backup "$BK" --quarantine "$QUAR" \
  --ext "jpg" --exact --assume-yes --no-bad-video --appledouble-action ignore \
  >/tmp/twincut_run2.log 2>&1 || true
# shellcheck disable=SC2010  # mtime-sorted selection; a portable non-ls-|-grep
# rewrite would need a full mtime-sort helper, which is out of scope here
REAL_MF2=$(ls -t "$QUAR"/_manifest-*.tsv 2>/dev/null | grep -v dryrun | head -n1)
# Put a blocker at original path
echo "blocker" > "$SRC/a.jpg"
"$TWINCUT" --restore "$REAL_MF2" --assume-yes >/tmp/twincut_restore2.log 2>&1 || true
grep -q "conflict" /tmp/twincut_restore2.log && ok "conflict reported" || bad "no conflict reported"
[[ "$(cat "$SRC/a.jpg")" == "blocker" ]] && ok "blocker preserved" || bad "blocker overwritten"

# ----------------------------------------------------------------------------
note "5. exit-code without --exit-code-on-dupes is 0 even with dupes"
# Reset
rm -rf "$SRC" "$QUAR"
mkdir -p "$SRC"
echo "x" > "$BK/x.jpg"; cp "$BK/x.jpg" "$SRC/x.jpg"
set +e
"$TWINCUT" --source "$SRC" --backup "$BK" --quarantine "$QUAR" \
  --ext "jpg" --exact --assume-yes --no-bad-video --appledouble-action ignore \
  >/tmp/twincut_run3.log 2>&1
RC=$?
set -e
assert_eq "$RC" "0" "exit code 0 without --exit-code-on-dupes"

# ----------------------------------------------------------------------------
echo
echo "===== RESULT: $PASS passed, $FAIL failed ====="
[[ $FAIL -eq 0 ]] || exit 1
