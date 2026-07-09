#!/usr/bin/env bash
# tests/vid_eq_smoke.sh — contract test for bin/vid_eq.sh fast/full modes.
# Guards against the 2026-07 field-swap regression (duration/size read into
# swapped vars => similar-video only matched byte-identical files).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VE="$ROOT/bin/vid_eq.sh"
TC="$ROOT/bin/twincut.sh"
HI="$ROOT/tests/fixtures/video/clip_high.mp4"   # 37555 B, 3.0s, h264 320x240
LO="$ROOT/tests/fixtures/video/clip_low.mp4"    # 35913 B, 3.0s, h264 320x240

fail(){ echo "FAIL: $*" >&2; exit 1; }

if ! command -v ffprobe >/dev/null 2>&1; then
  if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]]; then
    echo "FAIL: ffprobe required but missing" >&2; exit 1
  fi
  echo "SKIP: ffprobe not installed"; exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# 1. Same file twice → fast candidate.
out="$("$VE" --fast "$HI" "$HI")" || fail "self-compare exited nonzero"
[[ "$out" == "CANDIDATE:yes" ]] || fail "self-compare: got '$out'"

# 2. 4.37% size delta → rejected at default 0.5%, accepted at 5%.
out="$("$VE" --fast "$HI" "$LO" || true)"
[[ "$out" == "CANDIDATE:no" ]] || fail "default SIZE_PCT should reject 4.37% delta: got '$out'"
out="$(SIZE_PCT=5 "$VE" --fast "$HI" "$LO")" || fail "SIZE_PCT=5 exited nonzero"
[[ "$out" == "CANDIDATE:yes" ]] || fail "SIZE_PCT=5 should accept: got '$out'"

# 3. Resolution mismatch → rejected even with a huge size window.
scaled="$work/scaled.mp4"
if command -v ffmpeg >/dev/null 2>&1; then
  ffmpeg -y -loglevel error -i "$HI" -vf scale=160:120 -t 3 "$scaled"
  out="$(SIZE_PCT=99 "$VE" --fast "$HI" "$scaled" || true)"
  [[ "$out" == "CANDIDATE:no" ]] || fail "resolution mismatch should reject: got '$out'"
fi

# 4. Full mode (bare call): EQUAL label, honors env.
out="$(SIZE_PCT=5 "$VE" "$HI" "$LO")" || fail "full mode exited nonzero"
[[ "$out" == "EQUAL:yes" ]] || fail "full SIZE_PCT=5: got '$out'"
out="$("$VE" "$HI" "$LO" || true)"
[[ "$out" == "EQUAL:no" ]] || fail "full default should reject: got '$out'"

# 5. End-to-end: twincut similar-video actually detects a non-byte-identical
#    pair (this is the assertion that fails on the pre-fix field swap).
src="$work/src"; mkdir -p "$src"
cp "$HI" "$src/a.mp4"; cp "$LO" "$src/b.mp4"
events="$work/events.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_videq_e2e \
  "$TC" --self-check "$src" --include-similar-video --dry-run --json-events \
  >"$events" 2>"$work/stderr.log" || fail "twincut e2e exited nonzero (see $work/stderr.log)"
grep -q '"type":"dup_group"' "$events" || fail "e2e: no dup_group emitted"
grep -q '"match_reason":"video_fast"' "$events" || fail "e2e: no video_fast match"

echo "vid_eq_smoke: all ok"
