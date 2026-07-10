#!/usr/bin/env bash
# vid_eq.sh — metadata-level video equivalence check for twincut.
#
# Modes:
#   --fast (fast prefilter)  → prints CANDIDATE:yes|no, exit 0|1
#   default (full check)     → prints EQUAL:yes|no,     exit 0|1
#
# Fast and full run the same METADATA checks (size window, duration window,
# codec, WxH); "strictness" comes from the SIZE_PCT/DUR_SEC values the caller
# exports (twincut tightens them under --video-fast-strict). Never decodes frames.
# twincut.sh --video-fast-strict calls the bare form and greps EQUAL:yes,
# so the default mode MUST stay full/EQUAL.
#
# Env knobs (twincut.sh exports these so --size-pct/--dur-sec propagate):
#   SIZE_PCT  size window in percent   (default 0.5, matches twincut)
#   DUR_SEC   duration slack in seconds (default 0.3, matches twincut)
set -euo pipefail
export LC_ALL=C

SIZE_PCT=${SIZE_PCT:-0.5}
DUR_SEC=${DUR_SEC:-0.3}
MODE="full"

_fsize(){ stat -c %s -- "$1" 2>/dev/null || stat -f %z -- "$1" 2>/dev/null || echo 0; }
# One value per call — avoids depending on ffprobe's canonical field order,
# which bit us before (duration prints before size regardless of request order).
# || true: ffprobe failing on an existing-but-unreadable file must yield an
# empty value (→ compare fails → ":no"), not abort under set -e/pipefail
# before the contract line is printed.
_probe_dur(){ ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 -- "$1" 2>/dev/null | head -n1 || true; }
# codec,width,height on ONE line via csv (a 3-line default=nw=1 output would
# need three reads; one read used to silently drop width/height).
_probe_cwh(){ ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -of csv=p=0:s=, -- "$1" 2>/dev/null | head -n1 || true; }

vid_eq(){
  local A="$1" B="$2"
  [[ -f "$A" && -f "$B" ]] || { echo "file missing" >&2; return 2; }
  local sa sb da db ca cb label
  sa="$(_fsize "$A")"; sb="$(_fsize "$B")"
  da="$(_probe_dur "$A")"; db="$(_probe_dur "$B")"
  ca="$(_probe_cwh "$A")"; cb="$(_probe_cwh "$B")"
  [[ "$da" =~ ^[0-9]+(\.[0-9]+)?$ ]] || da=0
  [[ "$db" =~ ^[0-9]+(\.[0-9]+)?$ ]] || db=0
  label="EQUAL"; [[ "$MODE" == "fast" ]] && label="CANDIDATE"
  awk -v sa="$sa" -v sb="$sb" -v da="$da" -v db="$db" \
      -v ca="$ca" -v cb="$cb" -v pct="$SIZE_PCT" -v dslack="$DUR_SEC" -v label="$label" '
    function abs(x){return x<0?-x:x}
    BEGIN{
      ok = (sa+0 > 0) \
        && (abs(sa-sb) <= (pct/100.0)*sa) \
        && (abs(da-db) <= dslack) \
        && (ca != "") && (ca == cb);
      print label (ok ? ":yes" : ":no");
      exit ok ? 0 : 1;
    }'
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  ARGS=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --fast) MODE="fast"; shift;;
      --size-pct)
        if [[ $# -lt 2 ]]; then
          echo "Usage: $(basename "$0") [--fast] [--size-pct N] [--dur-sec SEC] <fileA> <fileB>" >&2
          exit 2
        fi
        SIZE_PCT="$2"; shift 2;;
      --dur-sec)
        if [[ $# -lt 2 ]]; then
          echo "Usage: $(basename "$0") [--fast] [--size-pct N] [--dur-sec SEC] <fileA> <fileB>" >&2
          exit 2
        fi
        DUR_SEC="$2"; shift 2;;
      *) ARGS+=("$1"); shift;;
    esac
  done
  if [[ ${#ARGS[@]} -ne 2 ]]; then
    echo "Usage: $(basename "$0") [--fast] [--size-pct N] [--dur-sec SEC] <fileA> <fileB>" >&2
    exit 2
  fi
  vid_eq "${ARGS[0]}" "${ARGS[1]}"
fi
