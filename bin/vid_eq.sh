#!/usr/bin/env bash

set -euo pipefail
export LC_ALL=C

# Fast mode only (default). The compare_with_backup tool calls this with --fast,
# but we also default to fast here to avoid accidental deep-decoding work.
FAST_MODE=true
# Tunables (can be overridden by env or CLI): size percentage window & duration slack
SIZE_PCT=${SIZE_PCT:-1}      # percent window for file size (±1%)
DUR_SEC=${DUR_SEC:-0.3}      # absolute seconds window for duration

vid_eq() {
  local A="$1" B="$2"
  [[ -f "$A" && -f "$B" ]] || { echo "file missing"; return 2; }
  # 先快筛（避免白解码）
  local sa sb da db vcA vcB whA whB
  read sa da < <(ffprobe -v error -show_entries format=size,duration -of default=nw=1:nk=1 "$A")
  read sb db < <(ffprobe -v error -show_entries format=size,duration -of default=nw=1:nk=1 "$B")
  read vcA whA < <(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -of default=nw=1:nk=1 "$A")
  read vcB whB < <(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -of default=nw=1:nk=1 "$B")
  awk -v sa="$sa" -v sb="$sb" -v da="$da" -v db="$db" \
      -v vcA="$vcA" -v vcB="$vcB" -v whA="$whA" -v whB="$whB" \
      -v pct="$SIZE_PCT" -v dslack="$DUR_SEC" '
    function abs(x){return x<0?-x:x}
    BEGIN{
      ok = (abs(sa-sb) <= (pct/100.0)*sa) && (abs(da-db) <= dslack) && (vcA==vcB) && (whA==whB);
      print ok?"CANDIDATE:yes":"CANDIDATE:no";
      exit ok?0:1;
    }'
  return $?
}
# 用法：
# vid_eq "/path/A.mkv" "/path/B.mkv"

# Allow running as a standalone script: ./vid_eq.sh [--fast] [--size-pct N] [--dur-sec SEC] A B
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  ARGS=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --fast) FAST_MODE=true; shift;;
      --size-pct) SIZE_PCT="$2"; shift 2;;
      --dur-sec) DUR_SEC="$2"; shift 2;;
      *) ARGS+=("$1"); shift;;
    esac
  done
  if [[ ${#ARGS[@]} -ne 2 ]]; then
    echo "Usage: $(basename "$0") [--fast] [--size-pct N] [--dur-sec SEC] <fileA> <fileB>" >&2
    exit 1
  fi
  vid_eq "${ARGS[0]}" "${ARGS[1]}"
fi