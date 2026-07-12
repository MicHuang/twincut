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
# (SIZE_PCT=0.5 pinned explicitly so this assertion is isolated from any
# SIZE_PCT the caller's environment may have exported — e.g. a spot-check
# run as `SIZE_PCT=5 bash tests/vid_eq_smoke.sh` must not leak in here.)
out="$(SIZE_PCT=0.5 "$VE" --fast "$HI" "$LO" || true)"
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
out="$(SIZE_PCT=0.5 "$VE" "$HI" "$LO" || true)"
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

# 6. Strict cross-check must evaluate an accepted candidate pair with one
#    vid_eq metadata pass. --fast and bare/full currently perform identical
#    checks under the same strict SIZE_PCT/DUR_SEC environment.
strict_src="$work/strict-src"; strict_bk="$work/strict-bk"
mkdir -p "$strict_src" "$strict_bk"
cp "$HI" "$strict_src/a.mp4"
cp "$HI" "$strict_bk/b.mp4"
printf x >> "$strict_bk/b.mp4"  # distinct hash, 1-byte size delta, valid video

counting_ve="$work/counting-vid-eq.sh"
cat > "$counting_ve" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$V_EQ_CALL_LOG"
exec "$REAL_V_EQ" "$@"
EOF
chmod +x "$counting_ve"

strict_calls="$work/strict-vid-eq.calls"
strict_events="$work/strict.ndjson"
REAL_V_EQ="$VE" V_EQ_CALL_LOG="$strict_calls" V_EQ_BIN="$counting_ve" \
  TWINCUT_RUN_ID=r_videq_strict "$TC" --source "$strict_src" \
  --backup "$strict_bk" --video-fast-strict --dry-run --json-events \
  >"$strict_events" 2>"$work/strict.log" \
  || fail "strict e2e exited nonzero (see $work/strict.log)"
grep -q '"match_reason":"video_strict"' "$strict_events" \
  || fail "strict e2e: no video_strict match"
strict_call_count="$(wc -l < "$strict_calls" | tr -d ' ')"
[[ "$strict_call_count" == "1" ]] \
  || fail "strict candidate should call vid_eq once, got $strict_call_count"
grep -q '^--fast ' "$strict_calls" \
  || fail "strict candidate should use the labeled --fast contract"

# 7. --size-pct / --dur-sec as the last argument must print usage and exit 2,
#    not crash on an unbound $2 under `set -u`.
rc=0; err="$("$VE" --size-pct 2>&1)" || rc=$?
[[ $rc -eq 2 ]] || fail "--size-pct with no value should exit 2: got rc=$rc"
[[ "$err" == Usage:* ]] || fail "--size-pct with no value should print usage: got '$err'"
rc=0; err="$("$VE" --dur-sec 2>&1)" || rc=$?
[[ $rc -eq 2 ]] || fail "--dur-sec with no value should exit 2: got rc=$rc"
[[ "$err" == Usage:* ]] || fail "--dur-sec with no value should print usage: got '$err'"

echo "vid_eq_smoke: all ok"
