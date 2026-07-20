#!/usr/bin/env bash
# tests/keep_policy_smoke.sh — KEEP-policy determinism: equal-mtime ties must
# be broken by LC_ALL=C path byte order, never by find(1) scan order
# (directory enumeration is filesystem-dependent: ext4 htree vs APFS).
# K1 pins the Wave-3 hash-dupe (mtime, path) sort; K2/K3 pin the
# similar-video tie-break on the source and backup paths.
#
# NOTE: the equal-mtime pin has most discriminating power on ext4 (CI
# ubuntu). On APFS, find(1) enumeration order happens to coincide with
# path byte order, so these checks can pass locally even if the sort
# regresses to scan order — a local green here is weaker evidence than CI.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TC="$ROOT/bin/twincut.sh"
HI="$ROOT/tests/fixtures/video/clip_high.mp4"
LO="$ROOT/tests/fixtures/video/clip_low.mp4"
fail(){ echo "FAIL: $*" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# --- K1. hash-dupe equal-mtime tie-break: keep = byte-smaller path ---------
# Create the byte-LARGER name first: on creation-ordered filesystems the old
# first-wins scan-order policy would have kept it, so this also guards
# against a regression back to scan order.
bk1="$work/k1"; mkdir -p "$bk1"
printf 'DUPDUP' > "$bk1/zz_dupe.jpg"
printf 'DUPDUP' > "$bk1/aa_dupe.jpg"
touch -t 202601011200 "$bk1/zz_dupe.jpg" "$bk1/aa_dupe.jpg"

ev1="$work/k1.ndjson"
TWINCUT_RUN_ID=r_keep_k1 "$TC" --backup "$bk1" --report-backup-dupes \
  --quarantine "$work/q1" --json-events >"$ev1" 2>"$work/k1.log" \
  || fail "K1: backup self-check exited nonzero"
grep -q "keep='$bk1/aa_dupe.jpg'" "$work/k1.log" \
  || fail "K1: equal-mtime hash-dupe keep is not the byte-smaller path"

# --- similar-video sections need ffprobe --------------------------------
if ! command -v ffprobe >/dev/null 2>&1; then
  if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]]; then
    echo "FAIL: ffprobe required but missing" >&2; exit 1
  fi
  echo "keep_policy_smoke: K1 ok (SKIP K2/K3: ffprobe not installed)"; exit 0
fi

# --- K2. source similar-video equal-mtime tie-break ----------------------
# clip_high/clip_low differ in bytes (no hash dupe) but are similar-video
# candidates at SIZE_PCT=5. Byte-larger name created first (see K1 note).
src2="$work/k2"; mkdir -p "$src2"
cp "$HI" "$src2/zz_vid.mp4"
cp "$LO" "$src2/aa_vid.mp4"
touch -t 202601011200 "$src2/zz_vid.mp4" "$src2/aa_vid.mp4"

ev2="$work/k2.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_keep_k2 \
  "$TC" --self-check "$src2" --include-similar-video --dry-run --json-events \
  >"$ev2" 2>"$work/k2.log" || fail "K2: self-check exited nonzero"
grep -q '"type":"dup_group"' "$ev2" || fail "K2: no dup_group emitted"
grep -q '"type":"dup_group".*"keep_path":"'"$src2"'/aa_vid.mp4"' "$ev2" \
  || fail "K2: equal-mtime similar-video keep is not the byte-smaller path"

# --- K3. backup similar-video equal-mtime tie-break ----------------------
bk3="$work/k3"; mkdir -p "$bk3"
cp "$HI" "$bk3/zz_vid.mp4"
cp "$LO" "$bk3/aa_vid.mp4"
touch -t 202601011200 "$bk3/zz_vid.mp4" "$bk3/aa_vid.mp4"

ev3="$work/k3.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_keep_k3 \
  "$TC" --backup "$bk3" --report-backup-dupes --quarantine "$work/q3" \
  --json-events >"$ev3" 2>"$work/k3.log" \
  || fail "K3: backup self-check exited nonzero"
grep -q "BACKUP-SIMILAR" "$work/k3.log" || fail "K3: no similar-video pair found"
grep -q "keep='$bk3/aa_vid.mp4'" "$work/k3.log" \
  || fail "K3: equal-mtime similar-video keep is not the byte-smaller path"
grep -q '"type":"dup_group"' "$ev3" || fail "K3: no dup_group emitted"
grep -q '"type":"dup_group".*"keep_path":"'"$bk3"'/aa_vid.mp4"' "$ev3" \
  || fail "K3: dup_group keep_path is not the byte-smaller path"
k3_event_count="$(grep -c '"type":"dup_group"' "$ev3")"
[[ "$k3_event_count" -eq 1 ]] \
  || fail "K3: expected exactly one dup_group for the pair, got $k3_event_count"
k3_report_count="$(grep -c 'BACKUP-SIMILAR' "$work/k3.log")"
[[ "$k3_report_count" -eq 1 ]] \
  || fail "K3: expected exactly one BACKUP-SIMILAR line for the pair, got $k3_report_count"

# K3b. Three mutually-similar files form three undirected pairs. Once a
# reverse pair is seen, the candidate loop must continue to later unseen
# candidates rather than break and drop a valid pair.
bk3b="$work/k3b"; mkdir -p "$bk3b"
cp "$HI" "$bk3b/aa_vid.mp4"
cp "$LO" "$bk3b/mm_vid.mp4"
cp "$HI" "$bk3b/zz_vid.mp4"
touch -t 202601011200 "$bk3b/aa_vid.mp4" "$bk3b/mm_vid.mp4" "$bk3b/zz_vid.mp4"

ev3b="$work/k3b.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_keep_k3b \
  "$TC" --backup "$bk3b" --report-backup-dupes --quarantine "$work/q3b" \
  --json-events >"$ev3b" 2>"$work/k3b.log" \
  || fail "K3b: backup self-check exited nonzero"
k3b_event_count="$(grep -c '"type":"dup_group"' "$ev3b")"
[[ "$k3b_event_count" -eq 3 ]] \
  || fail "K3b: expected all three unique pairs, got $k3b_event_count dup_group events"
k3b_report_count="$(grep -c 'BACKUP-SIMILAR' "$work/k3b.log")"
[[ "$k3b_report_count" -eq 3 ]] \
  || fail "K3b: expected all three unique report lines, got $k3b_report_count"

echo "keep_policy_smoke: all ok"
