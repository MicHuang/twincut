#!/usr/bin/env bash
# tests/p1_stage11_smoke.sh — Stage 11 contract smoke for cross/self flows.
#
# Asserts the migrated dup_group / run_start / run_end shapes on REAL runs
# (the events_contract.sh + roundtrip tests cover the helpers/fixtures; this
# guards that the live call sites in twincut.sh emit the canonical shapes).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TWINCUT="$ROOT/bin/twincut.sh"
PASS=0; FAIL=0
assert(){
  local what="$1" cond="$2"
  if eval "$cond"; then echo "  ok   $what"; PASS=$((PASS+1));
  else echo "  FAIL $what (cond: $cond)"; FAIL=$((FAIL+1)); fi
}

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# === self-check dry-run ===
SC="$TMP/sc"; mkdir -p "$SC"
printf 'dup-content' > "$SC/a.jpg"
printf 'dup-content' > "$SC/b.jpg"
printf 'dup-content' > "$SC/c.jpg"
SELF="$TMP/self.ndjson"
"$TWINCUT" --self-check "$SC" --dry-run --json-events >"$SELF" 2>/dev/null || true

assert "self: run_start has dry_run=true" \
  'grep -q "\"type\":\"run_start\".*\"dry_run\":true" "$SELF"'
assert "self: dup_group remove is an array carrying size" \
  'python3 -c "import json,sys; gs=[json.loads(l) for l in open(\"$SELF\") if l.strip() and json.loads(l)[\"type\"]==\"dup_group\"]; sys.exit(0 if gs and isinstance(gs[0][\"remove\"],list) and \"size\" in gs[0][\"remove\"][0] else 1)"'
assert "self: run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$SELF"'
assert "self: every emitted line is valid JSON" \
  'python3 -c "import json; [json.loads(l) for l in open(\"$SELF\") if l.strip()]"'

# === cross-check dry-run ===
CSRC="$TMP/src"; CBK="$TMP/bk"; mkdir -p "$CSRC" "$CBK"
printf 'x-content' > "$CSRC/a.jpg"
printf 'x-content' > "$CBK/a.jpg"
CROSS="$TMP/cross.ndjson"
"$TWINCUT" --source "$CSRC" --backup "$CBK" --dry-run --json-events >"$CROSS" 2>/dev/null || true

assert "cross: dup_group has hash and single-entry array remove" \
  'python3 -c "import json,sys; gs=[json.loads(l) for l in open(\"$CROSS\") if l.strip() and json.loads(l)[\"type\"]==\"dup_group\"]; sys.exit(0 if gs and \"hash\" in gs[0] and len(gs[0][\"remove\"])==1 else 1)"'
assert "cross: no legacy emit_event leakage (dup_group has no algo)" \
  '! grep -q "\"type\":\"dup_group\".*\"algo\"" "$CROSS"'

echo
echo "=========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
