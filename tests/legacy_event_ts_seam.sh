#!/usr/bin/env bash
# tests/legacy_event_ts_seam.sh — verify that the legacy emit_event helper
# in bin/twincut.sh honors the TWINCUT_TEST_TS seam (P1 #4 from Stage 9
# reviewer-gemini). Runs a cross-check against two empty tempdirs; the
# cross-check entry path fires a legacy emit_event run_start before any I/O.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TWINCUT="$ROOT/bin/twincut.sh"

src=$(mktemp -d)
bk=$(mktemp -d)
out=$(mktemp)
trap 'rm -rf "$src" "$bk" "$out"' EXIT

TWINCUT_TEST_TS=1747934400 RUN_ID=r_legacy_ts \
  "$TWINCUT" --source "$src" --backup "$bk" --dry-run --json-events \
  >"$out" 2>/dev/null || true

first_line=$(head -1 "$out")

if ! grep -q '"type":"run_start"' <<<"$first_line"; then
  echo "FAIL: first emitted line is not run_start"
  echo "got: $first_line"
  exit 1
fi

if ! grep -q '"ts":1747934400' <<<"$first_line"; then
  echo "FAIL: ts seam not honored by legacy emit_event"
  echo "got: $first_line"
  exit 1
fi

echo "ok: legacy emit_event honors TWINCUT_TEST_TS"
