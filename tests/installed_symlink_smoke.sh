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
# This suite therefore invokes twincut through symlinks, in the shapes the
# resolution loop has distinct branches for: a single hop, a chain, a
# RELATIVE link target, a relative *invocation* (the loop's first iteration
# sees a relative path), and `bash <symlink>`.
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

# A RELATIVE link target: readlink returns "../prefix/twincut", not a path the
# caller can use as-is. Resolving it wrong yields a nonexistent path.
REL="$TMP/rel"; mkdir -p "$REL"
ln -sf "../prefix/twincut" "$REL/twincut"

# --- one entrypoint, one fixed two-duplicate corpus ---------------------------
# check_case <label> <cwd> <cmd...>
check_case(){
  local label="$1" cwd="$2"; shift 2
  local dir out err rc
  dir="$TMP/scan-$label"; mkdir -p "$dir"
  printf 'dup-content' > "$dir/a.jpg"
  printf 'dup-content' > "$dir/b.jpg"
  out="$TMP/$label.out"; err="$TMP/$label.err"
  rc=0
  ( cd "$cwd" && "$@" --self-check "$dir" --dry-run --json-events ) \
    >"$out" 2>"$err" || rc=$?

  # An unsourced lib/events.sh turns every emitter into a missing command,
  # which bash reports and steps over, leaving the run to exit 127.
  assert "$label: exits 0" "[ '$rc' = '0' ]"
  assert "$label: emits run_start" \
    "grep -q '\"type\":\"run_start\"' '$out'"
  assert "$label: emits dup_group" \
    "grep -q '\"type\":\"dup_group\"' '$out'"
  assert "$label: emits run_end status=succeeded" \
    "grep -q '\"type\":\"run_end\".*\"status\":\"succeeded\"' '$out'"
  # Match the emitter name, not a bare 'command not found': an unrelated
  # missing optional tool (ffmpeg, sips, phash) must not alias this bug.
  assert "$label: no missing emitter on stderr" \
    "! grep -q 'emit_[a-z_]*: command not found' '$err'"
}

# The repo script is the control. It is not ceremony: it is what makes a
# failure below attributable to symlink resolution rather than to the corpus
# or the flags.
check_case control    "$TMP"  "$ROOT/bin/twincut.sh"
check_case symlink    "$TMP"  "$PREFIX/twincut"
check_case chain      "$TMP"  "$HOP/twincut"
check_case relative   "$TMP"  "$REL/twincut"
check_case rel-invoke "$REL"  ./twincut
check_case bash-exec  "$TMP"  bash "$PREFIX/twincut"

# Note on what is NOT asserted here: that V_EQ_BIN moved from the prefix's
# vid_eq symlink to the repo's bin/vid_eq.sh. Grepping for the startup probe's
# "vid_eq helper not found" would pass against the unfixed script too — the
# prefix copy exists and would be found — so it would assert nothing. The
# probe's real failure mode is a hard exit at startup, which "exits 0" above
# already catches.

# --- thumbnail-detect: the second user-visible symptom ------------------------
# lib/thumb.sh loads from the same LIB_DIR, so the unfixed path breaks this
# flow too — and breaks it BEFORE the THUMB_LIB_LOADED guard can report why,
# because emit_error is itself one of the missing commands. Assert on the
# event stream rather than that guard's message: the message is unreachable,
# so asserting its absence would assert nothing. Detection itself needs
# sips/ffmpeg and is covered by the thumbnail suites; an empty preview over
# one unreadable file is enough to prove the libs loaded.
TD="$TMP/td"; mkdir -p "$TD"; printf 'not-a-real-jpeg' > "$TD/a.jpg"
td_rc=0
"$PREFIX/twincut" --thumbnail-detect --source "$TD" --dry-run --json-events \
  >"$TMP/td.out" 2>"$TMP/td.err" || td_rc=$?
assert "thumbnail-detect: exits 0" "[ '$td_rc' = '0' ]"
assert "thumbnail-detect: emits run_start mode=thumbnail_detect_preview" \
  "grep -q '\"type\":\"run_start\".*\"mode\":\"thumbnail_detect_preview\"' '$TMP/td.out'"
assert "thumbnail-detect: emits run_end status=succeeded" \
  "grep -q '\"type\":\"run_end\".*\"status\":\"succeeded\"' '$TMP/td.out'"
assert "thumbnail-detect: no missing emitter on stderr" \
  "! grep -q 'emit_[a-z_]*: command not found' '$TMP/td.err'"

echo
echo "=========================================="
echo "PASS=$PASS FAIL=$FAIL"
exit $(( FAIL > 0 ? 1 : 0 ))
