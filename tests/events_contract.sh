#!/usr/bin/env bash
# tests/events_contract.sh — per-helper unit tests for lib/events.sh.
#
# Runs each emit_* helper with canned input, compares stdout byte-for-byte
# against tests/fixtures/events/<event_type>__<case>.ndjson.
#
# Fixtures stay stable because helpers honor TWINCUT_TEST_TS and RUN_ID
# env vars.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="$ROOT/tests/fixtures/events"
PASS=0
FAIL=0

# shellcheck source=../lib/events.sh
source "$ROOT/lib/events.sh"

JSON_EVENTS=true    # required: helpers gate on $JSON_EVENTS
RUN_ID="r_test"
export TWINCUT_TEST_TS=1747934400

run_case(){
  local name="$1" fixture="$2"
  shift 2
  local actual
  actual="$("$@")"
  if diff -u "$FIXTURE_DIR/$fixture" <(printf '%s\n' "$actual") >/dev/null; then
    echo "  ok    $name"
    PASS=$((PASS+1))
  else
    echo "  FAIL  $name"
    diff -u "$FIXTURE_DIR/$fixture" <(printf '%s\n' "$actual") >&2 || true
    FAIL=$((FAIL+1))
  fi
}

# === run_start ===
run_case "run_start basic" "run_start__basic.ndjson" \
  emit_run_start --mode thumbnail_detect_preview --source /img

# === run_end ===
run_case "run_end succeeded" "run_end__succeeded.ndjson" \
  emit_run_end --status succeeded --duration-ms 1234 --total 42 --applied 30 --skipped 12

# === warn ===
run_case "warn io_error" "warn__io_error.ndjson" \
  emit_warn --code io_error --path /img/IMG.JPG --detail "mv failed"

# === error ===
run_case "error usage" "error__usage.ndjson" \
  emit_error --code usage_error --detail "missing --source"

# === progress ===
run_case "progress scan" "progress__scan.ndjson" \
  emit_progress --phase scan --done 10 --total 100 --current-path /img/IMG.JPG

# === thumb_candidate ===
run_case "thumb_candidate l2_exif" "thumb_candidate__l2_exif.ndjson" \
  emit_thumb_candidate --decision thumb_l2_exif \
    --path /img/IMG_0010.JPG --keeper /img/IMG_0010.HEIC \
    --group-id "2025-04-01T12:00:00_3024x4032" \
    --width 320 --height 240 --size-bytes 18432

run_case "thumb_candidate l3_embed" "thumb_candidate__l3_embed.ndjson" \
  emit_thumb_candidate --decision thumb_l3_embed \
    --path /img/IMG_0011.JPG --keeper /img/IMG_0011.HEIC \
    --group-id "l3:abc123" \
    --width 160 --height 120 --size-bytes 9216

run_case "thumb_candidate l1_phash" "thumb_candidate__l1_phash.ndjson" \
  emit_thumb_candidate --decision thumb_l1_review \
    --path /img/IMG_0012.JPG --keeper /img/IMG_0012.HEIC \
    --group-id "l1ph:abcd1234deadbeef" \
    --width 320 --height 240 --size-bytes 18432 \
    --phash-distance 3 --reason l1_phash_match

echo
echo "=========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
