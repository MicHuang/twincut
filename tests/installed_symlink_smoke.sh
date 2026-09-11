#!/usr/bin/env bash
# tests/installed_symlink_smoke.sh — the INSTALLED entrypoint path.
#
# installers/install.sh symlinks bin/twincut.sh to ~/.local/bin/twincut, and
# ui/server/twincut.go resolves the binary with exec.LookPath("twincut") — so
# the Web UI's default path runs twincut through that symlink. Every other
# suite invokes the repo script directly. That gap is why a SELF_DIR which
# resolved the containing directory but never a symlink at the FINAL path
# component survived unnoticed: LIB_DIR came out empty, lib/events.sh was
# never sourced, and an installed user's Web UI had no event channel at all —
# silently, because the plain-text report still worked.
#
# This suite therefore invokes twincut ONLY through symlinks. Three shapes,
# because the resolution loop has three distinct branches to get wrong:
# a single hop, a chain, and a RELATIVE link target.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
PASS=0; FAIL=0
assert(){
  local what="$1" cond="$2"
  if eval "$cond"; then echo "  ok   $what"; PASS=$((PASS+1));
  else echo "  FAIL $what (cond: $cond)"; FAIL=$((FAIL+1)); fi
}

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# --- mirror installers/install.sh into a scratch prefix -----------------------
PREFIX="$TMP/prefix"; mkdir -p "$PREFIX"
ln -sf "$ROOT/bin/twincut.sh" "$PREFIX/twincut"
ln -sf "$ROOT/bin/vid_eq.sh"  "$PREFIX/vid_eq"
if [ -f "$ROOT/bin/phash.py" ]; then ln -sf "$ROOT/bin/phash.py" "$PREFIX/phash"; fi

# A second hop: symlink -> symlink -> real file.
HOP="$TMP/hop"; mkdir -p "$HOP"
ln -sf "$PREFIX/twincut" "$HOP/twincut"

# A RELATIVE link target (readlink returns "../prefix/twincut", not a path
# the caller can use as-is). Resolving this wrong yields a nonexistent path.
REL="$TMP/rel"; mkdir -p "$REL"
ln -sf "../prefix/twincut" "$REL/twincut"

# --- run one entrypoint against a fixed two-duplicate corpus ------------------
# Writes <label>.out / <label>.err into $TMP and echoes the captured rc.
run_selfcheck(){
  local label="$1" entry="$2" dir rc
  dir="$TMP/scan-$label"; mkdir -p "$dir"
  printf 'dup-content' > "$dir/a.jpg"
  printf 'dup-content' > "$dir/b.jpg"
  rc=0
  "$entry" --self-check "$dir" --dry-run --json-events \
    >"$TMP/$label.out" 2>"$TMP/$label.err" || rc=$?
  echo "$rc"
}

# The three symlink shapes plus the repo script as a control. The control is
# not ceremony: it is what proves a failure below is about symlink resolution
# and not about the corpus or the flags.
for case_spec in \
  "control:$ROOT/bin/twincut.sh" \
  "symlink:$PREFIX/twincut" \
  "chain:$HOP/twincut" \
  "relative:$REL/twincut"
do
  label="${case_spec%%:*}"; entry="${case_spec#*:}"
  rc="$(run_selfcheck "$label" "$entry")"
  out="$TMP/$label.out"; err="$TMP/$label.err"

  assert "$label: exits 0" "[ '$rc' = '0' ]"
  assert "$label: emits run_start" \
    "grep -q '\"type\":\"run_start\"' '$out'"
  assert "$label: emits dup_group" \
    "grep -q '\"type\":\"dup_group\"' '$out'"
  assert "$label: emits run_end status=succeeded" \
    "grep -q '\"type\":\"run_end\".*\"status\":\"succeeded\"' '$out'"
  # The bug's signature: lib/events.sh unsourced turns every emitter into a
  # missing command, which bash reports and then steps over.
  assert "$label: no missing-command fallout on stderr" \
    "! grep -q 'command not found' '$err'"
  # Resolving SELF_DIR correctly moves V_EQ_BIN from the prefix's vid_eq
  # symlink to the repo's bin/vid_eq.sh. Both are the same script, but the
  # startup probe hard-exits if neither is found.
  assert "$label: vid_eq helper still resolves" \
    "! grep -q 'vid_eq helper not found' '$err'"
done

# --- thumbnail-detect: the second user-visible symptom ------------------------
# lib/thumb.sh loads from the same LIB_DIR, so the unfixed path breaks this
# flow too — and breaks it BEFORE the THUMB_LIB_LOADED guard can report why,
# because emit_error is itself one of the missing commands. Assert on the
# event stream rather than on that guard's message: the message is
# unreachable, so asserting its absence would assert nothing.
# Detection itself needs sips/ffmpeg and is covered by the thumbnail suites;
# an empty preview over one unreadable file is enough to prove the libs loaded.
TD="$TMP/td"; mkdir -p "$TD"; printf 'not-a-real-jpeg' > "$TD/a.jpg"
td_rc=0
"$PREFIX/twincut" --thumbnail-detect --source "$TD" --dry-run --json-events \
  >"$TMP/td.out" 2>"$TMP/td.err" || td_rc=$?
assert "thumbnail-detect: exits 0" "[ '$td_rc' = '0' ]"
assert "thumbnail-detect: emits run_start mode=thumbnail_detect_preview" \
  "grep -q '\"type\":\"run_start\".*\"mode\":\"thumbnail_detect_preview\"' '$TMP/td.out'"
assert "thumbnail-detect: emits run_end status=succeeded" \
  "grep -q '\"type\":\"run_end\".*\"status\":\"succeeded\"' '$TMP/td.out'"
assert "thumbnail-detect: no missing-command fallout on stderr" \
  "! grep -q 'command not found' '$TMP/td.err'"

echo
echo "=========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
