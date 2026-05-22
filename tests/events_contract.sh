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

echo
echo "=========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
