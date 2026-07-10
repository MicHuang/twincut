#!/usr/bin/env bash
# tests/legacy_event_ts_seam.sh — end-to-end check that the real twincut.sh
# honors the TWINCUT_TEST_TS seam on its first emitted event (run_start).
# Complements tests/events_contract.sh, which exercises the lib/events.sh
# emitters in isolation rather than through the full CLI entry path.
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

echo "ok: twincut.sh honors TWINCUT_TEST_TS on run_start"
