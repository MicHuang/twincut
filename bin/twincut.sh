#!/usr/bin/env bash
[ -z "${BASH_VERSION:-}" ] && exec /usr/bin/env bash "$0" "$@"
# twincut.sh — cross/self media de-dupe with optional video-fast heuristics
# Style: strict bash, consistent 2-space indents, compact helpers, no duplicated logic.
set -euo pipefail

# ------------------------------- Defaults ------------------------------------
SOURCE_DIR=""
BACKUP_DIRS=()

DEST_ACTION="move"              # move | delete | list
QUAR_DIR="./_QUARANTINE"

ALGO="md5"                      # md5 | sha1
MIN_SIZE="0k"                   # e.g. 300k / 1M
EXTS="jpg,jpeg,png,dng,mp4,mov,avi,mkv,webm,mp3,wav,flac,aac,ogg,m4a,heic,heif,rmvb"

USE_CACHE=true
CACHE_FILE=".backup_hashindex.txt"
DRY_RUN=false
PROG_STEP=${PROG_STEP:-200}
ASSUME_YES=false
REBUILD_CACHE=false
FORCE_USE_CACHE=false

SOURCE_CACHE_FILE=".source_hashindex.txt"
KEEP_SOURCE_CACHE=false

# Video fast/strict
VIDEO_FAST=true                 # default ON unless --exact
VIDEO_FAST_STRICT=false
EXACT=false
VIDEO_EXTS="mp4,mov,m4v,avi,mkv,webm,hevc,h265,3gp,mts,m2ts"

# Similar-video folders/logs
SIMILAR_SUBDIR="_similar_video"
SIMILAR_LOG="_similar_video_map.csv"
BACKUP_SIMILAR_SUBDIR="_similar_video_backup"
SOURCE_SIMILAR_SUBDIR="_similar_video_source"

# Mode flags (default: off; set by CLI parser)
DO_CROSS=false
DO_BACKUP_SELF=false
DO_SOURCE_SELF=false

# Counters and temp files
TMP_CACHE="$(mktemp)"
TOTAL=0
DUPES=0
MOVED=0
DELETED=0
BK_DUPE_CNT=0
BK_DUPE_NOTE=""
SRC_DUPE_CNT=0
SIMILAR_CNT=0
SOURCE_CACHE=""
SRC_HASH_RUN_FILE=""

# Fast thresholds (percentages are in % units, not fractions)
SIZE_PCT=${SIZE_PCT:-0.5}      # file size window ±0.5%
DUR_SEC=${DUR_SEC:-0.3}        # duration bucket step in seconds

# Bad/sidecar handling
BAD_VIDEO_DETECT=true
BAD_VIDEO_ACTION="move"         # move | list | delete | ignore
BAD_VIDEO_SUBDIR="_bad_video"

IGNORE_APPLEDOUBLE=true
APPLEDOUBLE_ACTION="move"       # move | list | delete | ignore
APPLEDOUBLE_SUBDIR="_appledouble"

# Extra tunables for strict mode
FPS_PCT=${FPS_PCT:-0.5}        # fps tolerance in %
FPS_ABS_MIN=${FPS_ABS_MIN:-0.05}
BPS_PCT=${BPS_PCT:-0.5}        # bitrate tolerance in %

# Force rebuild of video meta index
REBUILD_VMETA=false

# vid_eq helper
SELF_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
if [[ -z "${V_EQ_BIN:-}" ]]; then
  if   [[ -x "$SELF_DIR/vid_eq"      ]]; then V_EQ_BIN="$SELF_DIR/vid_eq"
  elif [[ -x "$SELF_DIR/vid_eq.sh"   ]]; then V_EQ_BIN="$SELF_DIR/vid_eq.sh"
  elif command -v vid_eq >/dev/null 2>&1; then V_EQ_BIN="$(command -v vid_eq)"
  else
    echo "ERROR: vid_eq helper not found. Re-run installers/install.sh or set V_EQ_BIN." >&2; exit 1
  fi
fi

# In strict mode, tighten defaults further (user minimums: size±0.3%, dur±0.15s)
if ${VIDEO_FAST_STRICT:-false}; then
  SIZE_PCT=0.2   # stricter than requested min (0.3)
  DUR_SEC=0.15
fi

# Video meta index (TSV content, filename kept as .csv for compatibility)
VIDEO_META_NAME=".video_meta_index.csv"

# Intra-backup/source duplicate control
REPORT_BACKUP_DUPES=false; FIX_BACKUP_DUPES=false
BACKUP_DUPE_SUBDIR="_backup_dupes"; BACKUP_DUPE_LOG="_backup_dupes_map.csv"

REPORT_SOURCE_DUPES=false; FIX_SOURCE_DUPES=false
SOURCE_DUPE_SUBDIR="_source_dupes"; SOURCE_DUPE_LOG="_source_dupes_map.csv"

# ------------------------------ Small helpers --------------------------------
die(){ echo "ERROR: $*" >&2; exit 2; }
mtime(){ stat -f %m "$1" 2>/dev/null || stat -c %Y "$1" 2>/dev/null || echo 0; }
fsize(){ stat -f %z "$1" 2>/dev/null || stat -c %s "$1" 2>/dev/null || echo 0; }

move_unique(){ # move_unique SRC DIR
  local src="$1" dir="$2" base dest i=1
  mkdir -p "$dir"
  base="$(basename -- "$src")"
  dest="$dir/$base"
  while [[ -e "$dest" ]]; do dest="$dir/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
  if $DRY_RUN; then echo "[DRY] mv \"$src\" \"$dest\""; else mv -- "$src" "$dest"; fi
}

# Hashers
hash_file(){
  case "$ALGO" in
    md5)
      if command -v md5 >/dev/null 2>&1;     then md5 -q "$1"
      elif command -v md5sum >/dev/null 2>&1; then md5sum "$1" | awk '{print $1}'
      else echo "NO_MD5_TOOL"; return 1; fi;;
    sha1)
      if command -v shasum >/dev/null 2>&1;   then shasum "$1" | awk '{print $1}'
      elif command -v sha1sum >/dev/null 2>&1; then sha1sum "$1" | awk '{print $1}'
      else echo "NO_SHA1_TOOL"; return 1; fi;;
    *) echo "BAD_ALGO"; return 1;;
  esac
}

# EXTS → find predicate (sanitised)
build_name_predicate(){
  NAME_PREDICATE=()
  IFS=',' read -r -a _exts <<< "${EXTS//[[:space:]]/}"
  local first=true e
  if [[ ${#_exts[@]} -eq 0 || -z "${_exts[*]}" ]]; then NAME_PREDICATE=( -name '*' ); return; fi
  for e in "${_exts[@]}"; do
    [[ -z "$e" ]] && continue
    if $first; then NAME_PREDICATE+=( -iname "*.$e" ); first=false; else NAME_PREDICATE+=( -o -iname "*.$e" ); fi
  done
}

# Video extension check
is_video_ext(){
  local ext="${1##*.}"; ext="$(printf '%s' "$ext" | tr '[:upper:]' '[:lower:]')"
  IFS=',' read -r -a _vexts <<< "$VIDEO_EXTS"
  for e in "${_vexts[@]}"; do [[ "$ext" == "$e" ]] && return 0; done
  return 1
}

# Cache meta
read_cache_meta(){ [[ -f "$1" ]] && grep -m1 '^# meta:' "$1" 2>/dev/null || true; }
write_cache_meta(){ local ts; ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"; echo "# meta: algo=$ALGO; min_size=$MIN_SIZE; exts=$EXTS; created=$ts" > "$1"; }
should_rebuild_cache(){
  local cache_file="$1" meta; meta="$(read_cache_meta "$cache_file")"
  $REBUILD_CACHE && return 0
  $FORCE_USE_CACHE && return 1
  [[ ! -f "$cache_file" ]] && return 0
  if [[ -z "$meta" ]]; then
    if [[ -t 0 && "$ASSUME_YES" == false ]]; then
      read -r -p "[?] Cache exists without metadata. Rebuild now? [Y/n] " ans
      [[ -z "$ans" || "$ans" =~ ^[Yy]$ ]] && return 0 || return 1
    else return 1; fi
  fi
  local m_algo m_min m_ext
  m_algo="$(echo "$meta" | sed -n 's/.* algo=\([^;]*\).*/\1/p')"
  m_min="$( echo "$meta" | sed -n 's/.* min_size=\([^;]*\).*/\1/p')"
  m_ext="$( echo "$meta" | sed -n 's/.* exts=\([^;]*\).*/\1/p')"
  if [[ "$m_algo" != "$ALGO" || "$m_min" != "$MIN_SIZE" || "$m_ext" != "$EXTS" ]]; then
    if [[ -t 0 && "$ASSUME_YES" == false ]]; then
      echo "[!] Cache params differ: cache(algo=$m_algo,min=$m_min,exts=$m_ext) vs run(algo=$ALGO,min=$MIN_SIZE,exts=$EXTS)"
      read -r -p "[?] Rebuild cache to match current params? [Y/n] " ans
      [[ -z "$ans" || "$ans" =~ ^[Yy]$ ]] && return 0 || return 1
    else return 0; fi
  fi
  return 1
}
prune_cache_missing(){ # keep header + live files only
  local cache_file="$1" before after tmp
  [[ -f "$cache_file" ]] || return 0
  before=$(grep -v '^#' "$cache_file" 2>/dev/null | wc -l | tr -d ' ')
  tmp="$(mktemp)"; grep '^#' "$cache_file" 2>/dev/null > "$tmp" || true
  grep -v '^#' "$cache_file" 2>/dev/null | awk -F '\t' 'NF>=2 {print $1"\t"$2}' | while IFS=$'\t' read -r hh pp; do [[ -e "$pp" ]] && printf "%s\t%s\n" "$hh" "$pp" >> "$tmp"; done
  mv "$tmp" "$cache_file"
  after=$(grep -v '^#' "$cache_file" 2>/dev/null | wc -l | tr -d ' ')
  local removed=$(( before - after )); (( removed > 0 )) && echo "[*] Pruned cache: $cache_file (removed $removed stale entries)" || true
}

# ------------------------------ Video meta TSV -------------------------------
duration_bucket(){ awk -v d="$1" -v step="$DUR_SEC" 'BEGIN{ if(step<=0){step=0.3} print int((d + step/2.0)/step) }'; }
append_video_meta(){
  local csv="$1" f="$2"; [[ -f "$f" ]] || return 0

  local ssize vline codec width height fps_str fps_val bitrate d_stream d_fmt sdur mtime dbuck isbad=0 bname
  ssize="$(fsize "$f")"

  vline="$(ffprobe -v error -hide_banner -select_streams v:0 -show_entries stream=codec_name,width,height -of csv=p=0:s=, "$f" 2>/dev/null || true)"
  codec="$( printf '%s\n' "$vline" | awk -F',' '{print $1}')" ; width="$( printf '%s\n' "$vline" | awk -F',' '{print $2}')" ; height="$(printf '%s\n' "$vline" | awk -F',' '{print $3}')"

  fps_str="$(ffprobe -v error -hide_banner -select_streams v:0 -show_entries stream=avg_frame_rate -of default=nk=1:nw=1 "$f" 2>/dev/null | head -n1 || true)"
  if [[ -z "$fps_str" || "$fps_str" == "N/A" ]]; then
    fps_str="$(ffprobe -v error -hide_banner -select_streams v:0 -show_entries stream=r_frame_rate -of default=nk=1:nw=1 "$f" 2>/dev/null | head -n1 || true)"
  fi
  if [[ "$fps_str" =~ ^[0-9]+/[0-9]+$ ]]; then
    fps_val="$(awk -v r="$fps_str" 'BEGIN{ split(r,a,"/"); if(a[2]==0) printf("0"); else printf("%.6f", a[1]/a[2]); }')"
  else
    fps_val="$fps_str"
  fi

  bitrate="$(ffprobe -v error -hide_banner -show_entries format=bit_rate -of default=nk=1:nw=1 "$f" 2>/dev/null | head -n1 || true)"

  d_stream="$(ffprobe -v error -hide_banner -select_streams v:0 -show_entries stream=duration -of default=nw=1:nk=1 "$f" 2>/dev/null | head -n1 || true)"
  d_fmt="$(   ffprobe -v error -hide_banner -show_entries format=duration -of default=nw=1:nk=1 "$f" 2>/dev/null | head -n1 || true)"
  if [[ -n "$d_stream" && "$d_stream" != "N/A" && "$d_stream" != "0" ]]; then sdur="$d_stream"
  elif [[ -n "$d_fmt" && "$d_fmt" != "N/A" ]]; then sdur="$d_fmt"
  else sdur="0"; fi

  # Heuristic bad video (also catches AppleDouble & *_ftyp)
  bname="$(basename -- "$f")"
  if [[ -z "${codec:-}" || -z "${width:-}" || -z "${height:-}" || "${width:-0}" -eq 0 || "${height:-0}" -eq 0 ]]; then isbad=1; fi
  if [[ -z "${sdur:-}" || "${sdur:-0}" == "0" || "${sdur:-}" == "N/A" ]]; then isbad=1; fi
  if [[ -z "${fps_val:-}" || "${fps_val:-0}" == "0" || "${fps_val:-}" == "N/A" ]]; then isbad=1; fi
  if [[ "$bname" == "._"* || "$bname" == *"_ftyp" ]]; then isbad=1; fi

  mtime="$(mtime "$f")"; dbuck="$(duration_bucket "${sdur:-0}")"

  # TSV row: path size duration codec width height mtime dur_bucket fps bitrate bad
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$f" "${ssize:-0}" "${sdur:-0}" "${codec:-}" "${width:-}" "${height:-}" "$mtime" "${dbuck:-0}" "${fps_val:-}" "${bitrate:-}" "${isbad:-0}" >> "$csv"
}

ensure_video_meta_index(){
  local dir="$1" vcsv="$1/$VIDEO_META_NAME" meta tmp kept tmp2
  meta="# vmeta: size_pct=$SIZE_PCT; dur_sec=$DUR_SEC; created=$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  $REBUILD_VMETA && [[ -f "$vcsv" ]] && rm -f "$vcsv"

  if [[ ! -f "$vcsv" ]]; then
    printf '%s\n' "$meta" > "$vcsv"
    echo -e "path\tsize\tduration\tcodec\twidth\theight\tmtime\tdur_bucket\tfps\tbitrate\tbad" >> "$vcsv"
  fi
  if ! head -n1 "$vcsv" | grep -q '^# vmeta:'; then
    tmp="$(mktemp)"; printf '%s\n' "$meta" > "$tmp"; awk '1' "$vcsv" >> "$tmp"; mv "$tmp" "$vcsv"
  fi

  # Retain only live rows
  kept="$(mktemp)"; tmp2="$(mktemp)"
  awk -F'\t' 'NR<=2{next} {print $1}' "$vcsv" | while IFS= read -r p; do [[ -e "$p" ]] && echo "$p" >> "$kept"; done
  { head -n2 "$vcsv"; awk -F'\t' 'NR<=2{next} {print $0}' "$vcsv" | while IFS= read -r line; do
      p="$(printf "%s" "$line" | awk -F'\t' '{print $1}')"
      grep -Fqx -- "$p" "$kept" && printf '%s\n' "$line"
    done; } > "$tmp2"
  mv "$tmp2" "$vcsv"; rm -f "$kept" 2>/dev/null || true

  # Append new files
  IFS=',' read -r -a _vexts <<< "$VIDEO_EXTS"
  set --; for e in "${_vexts[@]}"; do set -- "$@" -iname "*.$e" -o; done; [[ "$#" -gt 0 ]] && set -- "${@:1:$#-1}"
  while IFS= read -r -d '' vf; do
    awk -F'\t' -v p="$vf" 'NR>2 && $1==p{found=1;exit} END{exit found?0:1}' "$vcsv" || append_video_meta "$vcsv" "$vf"
  done < <(find -L "$dir" -type f \( "$@" \) -size +"$MIN_SIZE" -print0)
  echo "$vcsv"
}

# One place to interpret BAD_VIDEO action
handle_bad_video(){
  local f="$1"
  case "$BAD_VIDEO_ACTION" in
    list)   echo "[BAD-VIDEO] $f" ;;
    delete) $DRY_RUN && echo "[DRY][BAD-VIDEO] rm \"$f\"" || rm -f -- "$f" ;;
    move)   move_unique "$f" "$QUAR_DIR/$BAD_VIDEO_SUBDIR" ;;
    ignore) : ;;
  esac
}

is_bad_video_row(){ # echo 1 if bad; else 0
  local meta="$1" f="$2" bad
  bad="$(awk -F'\t' -v p="$f" 'NR>2 && $1==p {print $11; exit}' "$meta" 2>/dev/null || true)"
  [[ -z "$bad" ]] && echo 0 || echo "$bad"
}

# ------------------------------ CLI / Usage ----------------------------------
usage(){
  cat <<EOF
Usage:
  $(basename "$0") --source <DIR> --backup <DIR> [--backup <DIR2> ...]
                   [--action move|delete|list] [--quarantine <DIR>]
                   [--algo md5|sha1] [--min-size 300k]
                   [--ext "jpg,png,mp4,..."] [--rebuild-cache|--use-cache]
                   [--cache-file <PATH>] [--prog-step N] [--dry-run] [--assume-yes]
                   [--source-cache-file <NAME>] [--keep-source-cache]
                   [--video-fast] [--video-fast-strict] [--exact]
                   [--size-pct N] [--dur-sec SEC]
                   [--vid-eq </path/to/vid_eq.sh>] [--similar-dir <NAME>]
                   [--report-backup-dupes] [--fix-backup-dupes]
                   [--report-source-dupes] [--fix-source-dupes]
                   [--bad-video-detect|--no-bad-video] [--bad-video-action move|list|delete|ignore]
                   [--ignore-appledouble|--no-ignore-appledouble] [--appledouble-action move|list|delete|ignore]
                   [--fps-pct N] [--fps-abs-min N] [--bps-pct N]
                   [--rebuild-video-meta]
Notes:
  - During --dry-run, a source-side cache (<source>/.source_hashindex.txt) is created/updated.
  - After a successful non-dry run, the source cache is removed unless --keep-source-cache.
  - Default video-fast is enabled (size±0.5%, dur±0.3s). Use --exact for hash-only.
  - In --video-fast-strict, join also compares fps/bitrate (if present) and does a full vid_eq check.
  - Modes:
    * Self-check: --report/--fix-*-dupes limit the run to that side and SKIP cross/similar.
    * Cross-check runs only when neither self-check flag is present.
EOF
  exit 1
# End of usage()
}

# ------------------------------ Parse CLI ----------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --source) SOURCE_DIR="${2:-}"; shift 2;;
    --backup) BACKUP_DIRS+=("${2:-}"); shift 2;;
    --action) DEST_ACTION="${2:-}"; shift 2;;
    --quarantine) QUAR_DIR="${2:-}"; shift 2;;
    --algo) ALGO="${2:-}"; shift 2;;
    --min-size) MIN_SIZE="${2:-}"; shift 2;;
    --ext) EXTS="${2:-}"; shift 2;;

    --use-cache) USE_CACHE=true; REBUILD_CACHE=false; shift;;
    --rebuild-cache) REBUILD_CACHE=true; shift;;
    --force-use-cache) FORCE_USE_CACHE=true; shift;;
    --cache-file) CACHE_FILE="${2:-}"; shift 2;;

    --prog-step) PROG_STEP="${2:-}"; shift 2;;
    --dry-run) DRY_RUN=true; shift;;
    --assume-yes) ASSUME_YES=true; shift;;

    --source-cache-file) SOURCE_CACHE_FILE="${2:-}"; shift 2;;
    --keep-source-cache) KEEP_SOURCE_CACHE=true; shift;;

    --video-fast) VIDEO_FAST=true; shift;;
    --no-video-fast) VIDEO_FAST=false; shift;;
    --video-fast-strict) VIDEO_FAST_STRICT=true; VIDEO_FAST=true; shift;;
    --exact) EXACT=true; VIDEO_FAST=false; shift;;

    --size-pct) SIZE_PCT="${2:-}"; shift 2;;
    --dur-sec) DUR_SEC="${2:-}"; shift 2;;
    --vid-eq) V_EQ_BIN="${2:-}"; shift 2;;
    --similar-dir) SIMILAR_SUBDIR="${2:-}"; shift 2;;

    --report-backup-dupes) REPORT_BACKUP_DUPES=true; DO_BACKUP_SELF=true; shift;;
    --fix-backup-dupes) FIX_BACKUP_DUPES=true; DO_BACKUP_SELF=true; shift;;
    --report-source-dupes) REPORT_SOURCE_DUPES=true; DO_SOURCE_SELF=true; shift;;
    --fix-source-dupes) FIX_SOURCE_DUPES=true; DO_SOURCE_SELF=true; shift;;

    --bad-video-detect) BAD_VIDEO_DETECT=true; shift;;
    --no-bad-video) BAD_VIDEO_DETECT=false; shift;;
    --bad-video-action) BAD_VIDEO_ACTION="${2:-}"; shift 2;;

    --ignore-appledouble) IGNORE_APPLEDOUBLE=true; shift;;
    --no-ignore-appledouble) IGNORE_APPLEDOUBLE=false; shift;;
    --appledouble-action) APPLEDOUBLE_ACTION="${2:-}"; shift 2;;

    --fps-pct) FPS_PCT="${2:-}"; shift 2;;
    --fps-abs-min) FPS_ABS_MIN="${2:-}"; shift 2;;
    --bps-pct) BPS_PCT="${2:-}"; shift 2;;

    --rebuild-video-meta) REBUILD_VMETA=true; shift;;

    -h|--help) usage;;
    *) echo "Unknown option: $1"; usage;;
  esac
done

# -------------------------- Mode resolution/guards -------------------------
if ! $DO_BACKUP_SELF && ! $DO_SOURCE_SELF; then
  if [[ -n "$SOURCE_DIR" && ${#BACKUP_DIRS[@]} -gt 0 ]]; then DO_CROSS=true; fi
fi

# Required paths for selected modes
if $DO_SOURCE_SELF && [[ -z "$SOURCE_DIR" ]]; then die "--source required for source self-check"; fi
if $DO_BACKUP_SELF && [[ ${#BACKUP_DIRS[@]} -eq 0 ]]; then die "--backup <DIR> required for backup self-check"; fi
if $DO_CROSS; then
  [[ -z "$SOURCE_DIR" ]] && die "--source required for cross-check"
  [[ ${#BACKUP_DIRS[@]} -eq 0 ]] && die "--backup <DIR> required for cross-check"
fi

# Re-apply strict thresholds after CLI is parsed (parser runs after initial defaults)
if $VIDEO_FAST_STRICT; then SIZE_PCT=0.2; DUR_SEC=0.15; fi

# Early rebuild of video-meta if requested (non-destructive; honored even in dry-run)
if $REBUILD_VMETA; then
  if [[ -n "$SOURCE_DIR" ]]; then ensure_video_meta_index "$SOURCE_DIR" >/dev/null; fi
  for d in ${BACKUP_DIRS[@]+"${BACKUP_DIRS[@]}"}; do ensure_video_meta_index "$d" >/dev/null; done
fi

mkdir -p "$QUAR_DIR"
# 为每个备份目录建立/复用 hash 索引（带进度&抗中断）
if $DO_CROSS || $DO_BACKUP_SELF; then
for BDIR in ${BACKUP_DIRS[@]+"${BACKUP_DIRS[@]}"}; do
  LOCAL_CACHE="$BDIR/$CACHE_FILE"
  build_name_predicate
  TOTAL_B=$(find -L "$BDIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" | wc -l | tr -d ' ')
  [[ -z "$TOTAL_B" ]] && TOTAL_B=0
  echo "[*] Candidates in backup: $TOTAL_B"

  if should_rebuild_cache "$LOCAL_CACHE"; then
    echo "[*] Rebuilding cache: $LOCAL_CACHE"; write_cache_meta "$LOCAL_CACHE"
  else
    echo "[*] Using cache: $LOCAL_CACHE"
  fi
  if $FORCE_USE_CACHE && [[ ! -f "$LOCAL_CACHE" ]]; then
    echo "[*] Initializing cache (metadata): $LOCAL_CACHE"
    write_cache_meta "$LOCAL_CACHE"
  fi
  [[ -f "$LOCAL_CACHE" ]] && cat "$LOCAL_CACHE" >> "$TMP_CACHE"

  ALREADY_INDEXED_SET="$(mktemp)"
  grep -v '^#' "$LOCAL_CACHE" 2>/dev/null | awk -F '\t' 'NF>=2 {print $2}' > "$ALREADY_INDEXED_SET" || true

  CNT_B=0; ADDED_B=0
  while IFS= read -r -d '' f; do
    CNT_B=$((CNT_B+1))
    if grep -Fqx -- "$f" "$ALREADY_INDEXED_SET" 2>/dev/null; then
      (( CNT_B % PROG_STEP == 0 )) && printf "\r[=] Caching %-7d / %s (reused)" "$CNT_B" "$TOTAL_B"
      continue
    fi
    H=$(hash_file "$f") || continue
    printf "%s\t%s\n" "$H" "$f" >> "$LOCAL_CACHE"
    printf "%s\t%s\n" "$H" "$f" >> "$TMP_CACHE"
    ADDED_B=$((ADDED_B+1))
    (( CNT_B % PROG_STEP == 0 )) && printf "\r[+] Caching %-7d / %s" "$CNT_B" "$TOTAL_B"
  done < <(find -L "$BDIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" -print0)
  echo; rm -f "$ALREADY_INDEXED_SET" 2>/dev/null || true
  echo "[+] Cache ready: $LOCAL_CACHE (added $ADDED_B records)"

  # --- Build/refresh backup video meta index for fast join (cross or self-check) ---
  if $VIDEO_FAST && ! $EXACT; then
    VMETA_FILE="$(ensure_video_meta_index "$BDIR")"
  fi

  if $REPORT_BACKUP_DUPES || $FIX_BACKUP_DUPES; then
    BK_DUPE_DIR="$QUAR_DIR/$BACKUP_DUPE_SUBDIR"
    mkdir -p "$BK_DUPE_DIR"
    BK_DUPE_CSV="$BK_DUPE_DIR/$BACKUP_DUPE_LOG"
    [[ -f "$BK_DUPE_CSV" ]] || echo "hash,keep_path,dupe_path" > "$BK_DUPE_CSV"
  fi
  # --- Intra-backup duplicate detection (exact hash match within backup set) ---
    DUP_HASHES_FILE="$(mktemp)"
    grep -v '^#' "$TMP_CACHE" | awk -F '\t' 'NF>=2{print $1}' | sort | uniq -d > "$DUP_HASHES_FILE"
    DUP_COUNT=$(wc -l < "$DUP_HASHES_FILE" | tr -d ' ')
    echo "[*] Backup-internal duplicate hashes: $DUP_COUNT"
    if [[ "$DUP_COUNT" -gt 0 ]]; then
      while IFS= read -r h; do
        MAP_FILE="$(mktemp)"
        grep -v '^#' "$TMP_CACHE" | awk -F '\t' -v hh="$h" '$1==hh{print $2}' > "$MAP_FILE"
        # Keep policy: prefer oldest mtime
        KEEP_PATH=""; KEEP_MT=""
        while IFS= read -r p; do
          [[ -z "$p" ]] && continue
          mt="$(stat -f %m "$p" 2>/dev/null || stat -c %Y "$p" 2>/dev/null || echo 0)"
          if [[ -z "$KEEP_PATH" || "$mt" -lt "$KEEP_MT" ]]; then
            KEEP_PATH="$p"; KEEP_MT="$mt"
          fi
        done < "$MAP_FILE"
        # Move/report all duplicates except the chosen KEEP_PATH
        while IFS= read -r dp; do
          [[ -z "$dp" || "$dp" == "$KEEP_PATH" ]] && continue
          $REPORT_BACKUP_DUPES && echo "[BACKUP-DUPE] hash=$h keep='$KEEP_PATH' dupe='$dp'"
          BK_DUPE_CNT=$((BK_DUPE_CNT+1))
          if $FIX_BACKUP_DUPES; then
            base="$(basename "$dp")"; dest="$BK_DUPE_DIR/$base"; i=1
            DUPES=$((DUPES+1)); MOVED=$((MOVED+1))
            while [[ -e "$dest" ]]; do dest="$BK_DUPE_DIR/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
            if $DRY_RUN; then echo "[DRY][BACKUP-DUPE] mv \"$dp\" \"$dest\" (keep: $KEEP_PATH)"
            else mv -- "$dp" "$dest"; fi
            printf '"%s","%s","%s"\n' "$h" "$KEEP_PATH" "$dp" >> "$BK_DUPE_CSV"
          fi
        done < "$MAP_FILE"
        rm -f "$MAP_FILE" 2>/dev/null || true
      done < "$DUP_HASHES_FILE"
    fi
    rm -f "$DUP_HASHES_FILE" 2>/dev/null || true
  [[ $BK_DUPE_CNT -gt 0 ]] && echo "[*] Backup-internal duplicates (files): $BK_DUPE_CNT"
  if ! $DRY_RUN && $FIX_BACKUP_DUPES; then prune_cache_missing "$LOCAL_CACHE"; fi

  # --- Intra-backup similar-video detection (video-fast) ---
  if ( $REPORT_BACKUP_DUPES || $FIX_BACKUP_DUPES ) && $VIDEO_FAST && ! $EXACT; then
    B_SIM_DIR="$QUAR_DIR/$BACKUP_SIMILAR_SUBDIR"; mkdir -p "$B_SIM_DIR"
    B_SIM_CSV="$B_SIM_DIR/$SIMILAR_LOG"
    [[ -f "$B_SIM_CSV" ]] || echo "path,similar_to" > "$B_SIM_CSV"

    build_name_predicate
    while IFS= read -r -d '' bf; do

      if ${BAD_VIDEO_DETECT:-true}; then
        read s_bad < <( awk -F'\t' -v p="$bf" 'NR>2 && $1==p {print $11; exit}' "${VMETA_FILE:-/dev/null}" )
        if [[ -z "${s_bad:-}" ]]; then
          # fallback quick check using direct ffprobe fields if meta missing
          read bcod bw bh bdur < <( awk -F'\t' -v p="$bf" 'NR>2 && $1==p {print $4,$5,$6,$3; exit}' "${VMETA_FILE:-/dev/null}" )
          if [[ -z "${bcod:-}" || -z "${bw:-}" || -z "${bh:-}" || "${bw:-0}" -eq 0 || "${bh:-0}" -eq 0 || "${bdur:-0}" == "0" ]]; then
            s_bad=1
          else
            s_bad=0
          fi
        fi
        if [[ "$s_bad" == "1" ]]; then
          case "$BAD_VIDEO_ACTION" in
            list)   echo "[BAD-VIDEO] $bf" ;;
            delete) $DRY_RUN && echo "[DRY][BAD-VIDEO] rm \"$bf\"" || rm -f -- "$bf" ;;
            move)
              bdir="$QUAR_DIR/$BAD_VIDEO_SUBDIR"; mkdir -p "$bdir"
              base_b="$(basename -- "$bf")"; dest="$bdir/$base_b"; i=1
              while [[ -e "$dest" ]]; do dest="$bdir/${base_b%.*}_$i.${base_b##*.}"; i=$((i+1)); done
              $DRY_RUN && echo "[DRY][BAD-VIDEO] mv \"$bf\" \"$dest\"" || mv -- "$bf" "$dest"
              ;;
            ignore) : ;;
          esac
          continue
        fi
      fi

      is_video_ext "$bf" || continue
      # Load/append meta row for bf
      read bsz bdur bcod bw bh bmt bdb < <( awk -F'\t' -v p="$bf" 'NR>2 && $1==p {print $2,$3,$4,$5,$6,$7,$8; exit}' "${VMETA_FILE:-/dev/null}" )
      if [[ -z "${bsz:-}" ]]; then
        append_video_meta "$VMETA_FILE" "$bf"
        read bsz bdur bcod bw bh bmt bdb < <( awk -F'\t' -v p="$bf" 'NR>2 && $1==p {print $2,$3,$4,$5,$6,$7,$8; exit}' "$VMETA_FILE" )
      fi
      # read fps/bitrate for strict checks (columns 9/10)
      read bfps bbps < <( awk -F'\t' -v p="$bf" 'NR>2 && $1==p {print $9,$10; exit}' "${VMETA_FILE:-/dev/null}" )
      # Scan candidates in the same BDIR meta, exclude self
      while IFS= read -r cand; do
        [[ -z "$cand" || "$cand" == "$bf" ]] && continue
        out="$("$V_EQ_BIN" --fast "$bf" "$cand" 2>/dev/null || true)"
        if echo "$out" | grep -q "CANDIDATE:yes"; then
          # strict 模式再跑一次 full
          if $VIDEO_FAST_STRICT; then
            out2="$("$V_EQ_BIN" "$bf" "$cand" 2>/dev/null || true)"
            echo "$out2" | grep -q "EQUAL:yes" || { continue; }
          fi
          # Keep policy: prefer oldest mtime
          mt_bf=$(stat -f %m "$bf" 2>/dev/null || stat -c %Y "$bf" 2>/dev/null || echo 0)
          mt_cd=$(stat -f %m "$cand" 2>/dev/null || stat -c %Y "$cand" 2>/dev/null || echo 0)
          if (( mt_bf <= mt_cd )); then KEEP="$bf"; MOVE="$cand"; else KEEP="$cand"; MOVE="$bf"; fi
          if $REPORT_BACKUP_DUPES; then
            echo "[BACKUP-SIMILAR(video-fast)] keep='$KEEP' move='$MOVE'"
          fi
          if $FIX_BACKUP_DUPES; then
            base="$(basename "$MOVE")"; dest="$B_SIM_DIR/$base"; i=1
            while [[ -e "$dest" ]]; do dest="$B_SIM_DIR/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
            if $DRY_RUN; then echo "[DRY][BACKUP-SIMILAR] mv \"$MOVE\" \"$dest\" (keep: $KEEP)"
            else mv -- "$MOVE" "$dest"; echo "\"$MOVE\",\"$KEEP\"" >> "$B_SIM_CSV"; fi
            SIMILAR_CNT=$((SIMILAR_CNT+1)); MOVED=$((MOVED+1))
          fi
          break
        fi
      done < <(
        awk -F'\t' -v cc="$bcod" -v w="$bw" -v h="$bh" -v db="$bdb" -v spct="$SIZE_PCT" -v ssz="$bsz" -v strict="$VIDEO_FAST_STRICT" -v sfps="$bfps" -v sbps="$bbps" -v fps_pct="$FPS_PCT" -v fps_min="$FPS_ABS_MIN" -v bps_pct="$BPS_PCT" '
          NR<=2{next}
          $4==cc && $5==w && $6==h && $8==db {
            bsz=$2+0; diff=(bsz>ssz?bsz-ssz:ssz-bsz)
            ok=(diff*100 <= spct*ssz)
            if (ok && strict=="true") {
              cfps=$9+0;
              if (sfps!="" && cfps>0) {
                fps_tol = (sfps>0 ? sfps*(fps_pct/100.0) : fps_min);
                if (fps_tol < fps_min) fps_tol=fps_min;
                d=cfps-sfps; if (d<0) d=-d;
                if (d > fps_tol) ok=0;
              }
              cbps=$10+0;
              if (sbps!="" && cbps>0 && sbps+0>0) {
                rel = (cbps>sbps ? cbps-sbps : sbps-cbps) / sbps;
                if (rel > (bps_pct/100.0)) ok=0;
              }
            }
            if (ok) print $1
          }' "$VMETA_FILE"
      )
    done < <(find -L "$BDIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" -print0)
  fi
done
fi

# If user didn't request backup-internal dupe report/fix, still compute a count-only summary
if ( $DO_CROSS || $DO_BACKUP_SELF ) && ! $REPORT_BACKUP_DUPES && ! $FIX_BACKUP_DUPES; then
  BK_DUPE_CNT=$(grep -v '^#' "$TMP_CACHE" 2>/dev/null | awk -F '\t' 'NF>=2{c[$1]++} END{ s=0; for (h in c) if (c[h]>1) s+=c[h]-1; print s+0 }')
  BK_DUPE_NOTE=" (count-only)"
  [[ $BK_DUPE_CNT -gt 0 ]] && echo "[i] Backup appears to contain duplicate files: $BK_DUPE_CNT. Use --report-backup-dupes to list or --fix-backup-dupes to move them."
fi

# --- Source-side cache setup (for dry-run speed & resume) ---
if $DO_CROSS || $DO_SOURCE_SELF; then
SOURCE_CACHE="$SOURCE_DIR/$SOURCE_CACHE_FILE"; USE_SRC_CACHE=false
if $DRY_RUN || [[ -f "$SOURCE_CACHE" ]]; then
  USE_SRC_CACHE=true
  if should_rebuild_cache "$SOURCE_CACHE"; then
    echo "[*] (source) Rebuilding cache: $SOURCE_CACHE"; write_cache_meta "$SOURCE_CACHE"
  else
    echo "[*] (source) Using cache: $SOURCE_CACHE"
  fi
  SRC_ALREADY_INDEXED_SET="$(mktemp)"
  grep -v '^#' "$SOURCE_CACHE" 2>/dev/null | awk -F '\t' 'NF>=2 {print $2}' > "$SRC_ALREADY_INDEXED_SET" || true
fi

# Prepare source video meta index when video-fast is active (cross or source self-check)
if ( $DO_CROSS || $DO_SOURCE_SELF ) && $VIDEO_FAST && ! $EXACT; then
  SVMETA_FILE="$(ensure_video_meta_index "$SOURCE_DIR")"
fi

echo "[*] Scanning source for dupes..."
build_name_predicate
mkdir -p "$QUAR_DIR"

# Similar-video quarantine (only in cross mode)
if $DO_CROSS && $VIDEO_FAST && ! $EXACT; then
  SIMILAR_DIR="$QUAR_DIR/$SIMILAR_SUBDIR"; mkdir -p "$SIMILAR_DIR"
  SIMILAR_CSV="$SIMILAR_DIR/$SIMILAR_LOG"
  [[ -f "$SIMILAR_CSV" ]] || echo "source_path,backup_match" > "$SIMILAR_CSV"
fi

# Source dupe quarantine setup
if $REPORT_SOURCE_DUPES || $FIX_SOURCE_DUPES; then
  SRC_DUPE_DIR="$QUAR_DIR/$SOURCE_DUPE_SUBDIR"; mkdir -p "$SRC_DUPE_DIR"
  SRC_DUPE_CSV="$SRC_DUPE_DIR/$SOURCE_DUPE_LOG"
  [[ -f "$SRC_DUPE_CSV" ]] || echo "hash,keep_path,dupe_path" > "$SRC_DUPE_CSV"
fi

TOTAL_SRC=$(find -L "$SOURCE_DIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" | wc -l | tr -d ' ')
[[ -z "$TOTAL_SRC" ]] && TOTAL_SRC=0
echo "[*] Candidates in source: $TOTAL_SRC"

if ( $REPORT_SOURCE_DUPES || $FIX_SOURCE_DUPES ) && ! $USE_SRC_CACHE; then
  SRC_HASH_RUN_FILE="$(mktemp)"
else
  SRC_HASH_RUN_FILE=""
fi

while IFS= read -r -d '' f; do
  TOTAL=$((TOTAL+1))

  if ${IGNORE_APPLEDOUBLE:-true}; then
    base_f="$(basename -- "$f")"
    if [[ "$base_f" == "._"* ]]; then
      case "$APPLEDOUBLE_ACTION" in
        list) echo "[APPLEDOUBLE] $f" ;;
        delete) $DRY_RUN && echo "[DRY][APPLEDOUBLE] rm \"$f\"" || rm -f -- "$f" ;;
        move)
          adir="$QUAR_DIR/$APPLEDOUBLE_SUBDIR"; mkdir -p "$adir"
          dest="$adir/$base_f"; i=1; while [[ -e "$dest" ]]; do dest="$adir/${base_f%.*}_$i.${base_f##*.}"; i=$((i+1)); done
          $DRY_RUN && echo "[DRY][APPLEDOUBLE] mv \"$f\" \"$dest\"" || mv -- "$f" "$dest"
          ;;
        ignore) : ;;
      esac
      continue
    fi
  fi

  # --- get/append source hash (with source cache reuse) ---
  if $USE_SRC_CACHE; then
    if grep -Fqx -- "$f" "${SRC_ALREADY_INDEXED_SET:-/dev/null}" 2>/dev/null; then
      H="$(awk -F '\t' -v p="$f" '$2==p{print $1; exit}' "$SOURCE_CACHE" 2>/dev/null)"
      if [[ -z "$H" ]]; then
        H=$(hash_file "$f") || continue
        if $DRY_RUN || [[ -f "$SOURCE_CACHE" ]]; then
          printf "%s\t%s\n" "$H" "$f" >> "$SOURCE_CACHE"; echo "$f" >> "$SRC_ALREADY_INDEXED_SET"
        fi
      fi
    else
      H=$(hash_file "$f") || continue
      if $DRY_RUN || [[ -f "$SOURCE_CACHE" ]]; then
        printf "%s\t%s\n" "$H" "$f" >> "$SOURCE_CACHE"; echo "$f" >> "$SRC_ALREADY_INDEXED_SET"
      fi
    fi
  else
    H=$(hash_file "$f") || continue
  fi
  [[ -n "${SRC_HASH_RUN_FILE:-}" ]] && printf "%s\t%s\n" "$H" "$f" >> "$SRC_HASH_RUN_FILE"

  # --- MD5 exact cross-dup against backup TMP_CACHE (only in cross mode) ---
  if $DO_CROSS; then
    if grep -q "^${H}[[:space:]]" "$TMP_CACHE"; then
      DUPES=$((DUPES+1))
      case "$DEST_ACTION" in
        list) echo "[DUPE] $f" ;;
        delete)
          if $DRY_RUN; then echo "[DRY] rm \"$f\""
          else rm -f -- "$f"; DELETED=$((DELETED+1)); fi ;;
        move)
          base=$(basename "$f"); dest="$QUAR_DIR/$base"; i=1
          while [[ -e "$dest" ]]; do dest="$QUAR_DIR/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
          if $DRY_RUN; then echo "[DRY] mv \"$f\" \"$dest\""
          else mv -- "$f" "$dest"; MOVED=$((MOVED+1)); fi ;;
        *) echo "Unknown action: $DEST_ACTION"; exit 3;;
      esac
    fi
  fi

  # --- “video-fast” semantic join (cross or source self-check; not exact; videos only; and MD5 not matched) ---
  if ( $DO_CROSS || $DO_SOURCE_SELF ) && $VIDEO_FAST && ! $EXACT && is_video_ext "$f"; then
    # Load source meta from CSV (append if missing)
    read s_size s_dur s_codec s_w s_h s_mtime s_dbuck < <(
      awk -F'\t' -v p="$f" 'NR>2 && $1==p {print $2,$3,$4,$5,$6,$7,$8; exit}' "${SVMETA_FILE:-/dev/null}"
    )
    if [[ -z "${s_size:-}" ]]; then
      SVMETA_FILE="${SVMETA_FILE:-"$SOURCE_DIR/$VIDEO_META_NAME"}"
      if [[ ! -f "$SVMETA_FILE" ]]; then
        printf '# vmeta: size_pct=%s; dur_sec=%s; created=%s\n' "$SIZE_PCT" "$DUR_SEC" "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" > "$SVMETA_FILE"
        echo -e "path\tsize\tduration\tcodec\twidth\theight\tmtime\tdur_bucket\tfps\tbitrate\tbad" >> "$SVMETA_FILE"
      fi
      append_video_meta "$SVMETA_FILE" "$f"
      read s_size s_dur s_codec s_w s_h s_mtime s_dbuck < <(
        awk -F'\t' -v p="$f" 'NR>2 && $1==p {print $2,$3,$4,$5,$6,$7,$8; exit}' "$SVMETA_FILE"
      )
    fi
    # source fps/bitrate (columns 9/10)
    read s_fps s_bps < <( awk -F'\t' -v p="$f" 'NR>2 && $1==p {print $9,$10; exit}' "${SVMETA_FILE:-/dev/null}" )

      if ${BAD_VIDEO_DETECT:-true}; then
        read s_bad < <( awk -F'\t' -v p="$f" 'NR>2 && $1==p {print $11; exit}' "${SVMETA_FILE:-/dev/null}" )
        if [[ -z "${s_bad:-}" ]]; then
          # Use the already-loaded source meta fields as fallback
          if [[ -z "${s_codec:-}" || -z "${s_w:-}" || -z "${s_h:-}" || "${s_w:-0}" -eq 0 || "${s_h:-0}" -eq 0 || "${s_dur:-0}" == "0" ]]; then
            s_bad=1
          else
            s_bad=0
          fi
        fi
        if [[ "$s_bad" == "1" ]]; then
          case "$BAD_VIDEO_ACTION" in
            list)   echo "[BAD-VIDEO] $f" ;;
            delete) $DRY_RUN && echo "[DRY][BAD-VIDEO] rm \"$f\"" || rm -f -- "$f" ;;
            move)
              bdir="$QUAR_DIR/$BAD_VIDEO_SUBDIR"; mkdir -p "$bdir"
              base_f="$(basename -- "$f")"; dest="$bdir/$base_f"; i=1
              while [[ -e "$dest" ]]; do dest="$bdir/${base_f%.*}_$i.${base_f##*.}"; i=$((i+1)); done
              $DRY_RUN && echo "[DRY][BAD-VIDEO] mv \"$f\" \"$dest\"" || mv -- "$f" "$dest"
              ;;
            ignore) : ;;
          esac
          continue
        fi
      fi

    # Decide candidate meta and similar dir by mode
    if $DO_CROSS; then
      CAND_META_FILE="${VMETA_FILE:-}"
      SIM_DIR="$SIMILAR_DIR"
    else
      CAND_META_FILE="$SVMETA_FILE"
      SIM_DIR="$QUAR_DIR/$SOURCE_SIMILAR_SUBDIR"; mkdir -p "$SIM_DIR"
      SIMILAR_CSV="$SIM_DIR/$SIMILAR_LOG"; [[ -f "$SIMILAR_CSV" ]] || echo "path,similar_to" > "$SIMILAR_CSV"
    fi
    if [[ -n "$CAND_META_FILE" && -f "$CAND_META_FILE" ]]; then
      while IFS= read -r b; do
        [[ -z "$b" ]] && continue
        [[ "$b" == "$f" ]] && continue
        out="$("$V_EQ_BIN" --fast "$f" "$b" 2>/dev/null || true)"
        if echo "$out" | grep -q "CANDIDATE:yes"; then
          # strict 模式再跑一次 full
          if $VIDEO_FAST_STRICT; then
            out2="$("$V_EQ_BIN" "$f" "$b" 2>/dev/null || true)"
            echo "$out2" | grep -q "EQUAL:yes" || { continue; }
          fi
          if $DO_CROSS; then
            base="$(basename "$f")"; dest="$SIM_DIR/$base"; i=1
            while [[ -e "$dest" ]]; do dest="$SIM_DIR/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
            if $DRY_RUN; then
              echo "[DRY][SIMILAR(video-fast)] $f  ~~  $b"
              echo "[DRY] mv \"$f\" \"$dest\""
            else
              mv -- "$f" "$dest"; echo "\"$f\",\"$b\"" >> "$SIMILAR_CSV"
              echo "[SIMILAR(video-fast)] $f  ~~  $b"
            fi
            SIMILAR_CNT=$((SIMILAR_CNT+1)); MOVED=$((MOVED+1))
          else
            # Source self-check: prefer oldest keep
            mt_src=$(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null || echo 0)
            mt_b=$(stat -f %m "$b" 2>/dev/null || stat -c %Y "$b" 2>/dev/null || echo 0)
            if (( mt_src <= mt_b )); then KEEP="$f"; MOVE="$b"; else KEEP="$b"; MOVE="$f"; fi
            if $REPORT_SOURCE_DUPES; then
              echo "[SOURCE-SIMILAR(video-fast)] keep='$KEEP' move='$MOVE'"
            fi
            if $FIX_SOURCE_DUPES; then
              base="$(basename "$MOVE")"; dest="$SIM_DIR/$base"; i=1
              while [[ -e "$dest" ]]; do dest="$SIM_DIR/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
              if $DRY_RUN; then echo "[DRY][SOURCE-SIMILAR] mv \"$MOVE\" \"$dest\" (keep: $KEEP)"
              else mv -- "$MOVE" "$dest"; echo "\"$MOVE\",\"$KEEP\"" >> "$SIMILAR_CSV"; fi
              SIMILAR_CNT=$((SIMILAR_CNT+1)); MOVED=$((MOVED+1))
            fi
          fi
          break
        fi
      done < <(
        awk -F'\t' -v cc="$s_codec" -v w="$s_w" -v h="$s_h" -v db="$s_dbuck" -v spct="$SIZE_PCT" -v ssz="$s_size" -v strict="$VIDEO_FAST_STRICT" -v sfps="$s_fps" -v sbps="$s_bps" -v sp="$f" -v fps_pct="$FPS_PCT" -v fps_min="$FPS_ABS_MIN" -v bps_pct="$BPS_PCT" '
          NR<=2{next}
          ($1!=sp) && $4==cc && $5==w && $6==h && $8==db {
            bsz=$2+0; diff=(bsz>ssz?bsz-ssz:ssz-bsz)
            ok=(diff*100 <= spct*ssz)
            if (ok && strict=="true") {
              cfps=$9+0;
              if (sfps!="" && cfps>0) {
                fps_tol = (sfps>0 ? sfps*(fps_pct/100.0) : fps_min);
                if (fps_tol < fps_min) fps_tol=fps_min;
                d=cfps-sfps; if (d<0) d=-d;
                if (d > fps_tol) ok=0;
              }
              cbps=$10+0;
              if (sbps!="" && cbps>0 && sbps+0>0) {
                rel = (cbps>sbps ? cbps-sbps : sbps-cbps) / sbps;
                if (rel > (bps_pct/100.0)) ok=0;
              }
            }
            if (ok) print $1
          }' "$CAND_META_FILE"
      )
    fi
  fi

  (( TOTAL % PROG_STEP == 0 )) && printf "\r[*] Scanned %-7d / %s | dupes: %-7d" "$TOTAL" "$TOTAL_SRC" "$DUPES"
done < <(find -L "$SOURCE_DIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" -print0)
echo
fi

# --- Intra-source duplicate detection (exact hash match within source set) ---
if $REPORT_SOURCE_DUPES || $FIX_SOURCE_DUPES; then
  if $USE_SRC_CACHE; then SRC_HASHLIST_FILE="$SOURCE_CACHE"; else SRC_HASHLIST_FILE="$SRC_HASH_RUN_FILE"; fi
  if [[ -n "$SRC_HASHLIST_FILE" && -f "$SRC_HASHLIST_FILE" ]]; then
    # Filter the hash list to only rows whose path resides under the current SOURCE_DIR
    # and still exists on disk. This prevents stale or external paths (e.g., old TOSHIBA volume)
    # from being treated as "source-internal".
    SRC_FILTERED="$(mktemp)"
    SRC_DIR_PREFIX="${SOURCE_DIR%/}/"
    # Keep header lines if present (none expected here), then only pass live rows under SOURCE_DIR
    grep -v '^#' "$SRC_HASHLIST_FILE" 2>/dev/null \
      | awk -F '\t' -v dir="$SRC_DIR_PREFIX" 'NF>=2 && index($2,dir)==1 {print $0}' \
      | while IFS=$'\t' read -r h p; do
          [[ -n "$p" && -e "$p" ]] && printf "%s\t%s\n" "$h" "$p"
        done > "$SRC_FILTERED"

    SDUP_HASHES_FILE="$(mktemp)"
    awk -F '\t' 'NF>=2{print $1}' "$SRC_FILTERED" | sort | uniq -d > "$SDUP_HASHES_FILE"
    SDUP_COUNT=$(wc -l < "$SDUP_HASHES_FILE" | tr -d ' ')
    echo "[*] Source-internal duplicate hashes: $SDUP_COUNT"

    if [[ "$SDUP_COUNT" -gt 0 ]]; then
      while IFS= read -r sh; do
        SMAP_FILE="$(mktemp)"
        awk -F '\t' -v hh="$sh" '$1==hh{print $2}' "$SRC_FILTERED" > "$SMAP_FILE"

        # Keep policy: prefer oldest mtime within SOURCE_DIR
        KEEP_SPATH=""; KEEP_SMT=""
        while IFS= read -r sp; do
          [[ -z "$sp" ]] && continue
          smt="$(stat -f %m "$sp" 2>/dev/null || stat -c %Y "$sp" 2>/dev/null || echo 0)"
          if [[ -z "$KEEP_SPATH" || "$smt" -lt "$KEEP_SMT" ]]; then
            KEEP_SPATH="$sp"; KEEP_SMT="$smt"
          fi
        done < "$SMAP_FILE"

        # Process all except KEEP_SPATH
        while IFS= read -r sdp; do
          [[ -z "$sdp" || "$sdp" == "$KEEP_SPATH" ]] && continue
          $REPORT_SOURCE_DUPES && echo "[SOURCE-DUPE] hash=$sh keep='$KEEP_SPATH' dupe='$sdp'"
          SRC_DUPE_CNT=$((SRC_DUPE_CNT+1))
          if $FIX_SOURCE_DUPES; then
            base="$(basename "$sdp")"; sdest="$SRC_DUPE_DIR/$base"; i=1
            while [[ -e "$sdest" ]]; do sdest="$SRC_DUPE_DIR/${base%.*}_$i.${base##*.}"; i=$((i+1)); done
            if $DRY_RUN; then
              echo "[DRY][SOURCE-DUPE] mv \"$sdp\" \"$sdest\" (keep: $KEEP_SPATH)"
            else
              mv -- "$sdp" "$sdest"
            fi
            printf '"%s","%s","%s"\n' "$sh" "$KEEP_SPATH" "$sdp" >> "$SRC_DUPE_CSV"
            DUPES=$((DUPES+1)); MOVED=$((MOVED+1))
          fi
        done < "$SMAP_FILE"
        rm -f "$SMAP_FILE" 2>/dev/null || true
      done < "$SDUP_HASHES_FILE"
    fi
    rm -f "$SDUP_HASHES_FILE" "$SRC_FILTERED" 2>/dev/null || true
  fi
fi

# Prune caches after real ops
if ! $DRY_RUN && [[ -n "${SOURCE_CACHE:-}" && -f "$SOURCE_CACHE" ]]; then prune_cache_missing "$SOURCE_CACHE"; fi
# Also prune backup caches after real ops to drop stale paths
if ! $DRY_RUN && ( $DO_CROSS || $DO_BACKUP_SELF ); then
  for d in ${BACKUP_DIRS[@]+"${BACKUP_DIRS[@]}"}; do
    [[ -f "$d/$CACHE_FILE" ]] && prune_cache_missing "$d/$CACHE_FILE"
  done
fi
# Keep video meta fresh too (best-effort)
if ! $DRY_RUN; then
  if $DO_CROSS || $DO_BACKUP_SELF; then
    for d in ${BACKUP_DIRS[@]+"${BACKUP_DIRS[@]}"}; do [[ -f "$d/$VIDEO_META_NAME" ]] && ensure_video_meta_index "$d" >/dev/null; done
  fi
  [[ -f "$SOURCE_DIR/$VIDEO_META_NAME" ]] && ensure_video_meta_index "$SOURCE_DIR" >/dev/null
fi
# Remove source cache after a successful non-dry run if it was used, unless user keeps it
if ! $DRY_RUN && ${USE_SRC_CACHE:-false} && [[ -n "${SOURCE_CACHE:-}" && -f "$SOURCE_CACHE" ]] && [[ "$KEEP_SOURCE_CACHE" == false ]]; then
  rm -f "$SOURCE_CACHE" 2>/dev/null || true
  echo "[*] Removed source cache: $SOURCE_CACHE"
fi

echo "===== SUMMARY ====="
echo "Checked: $TOTAL files  | Duplicates: $DUPES"
[[ "$DEST_ACTION" == "move" ]] && echo "Moved to quarantine: $MOVED -> $QUAR_DIR"
[[ "$DEST_ACTION" == "delete" ]] && echo "Deleted: $DELETED"
[[ "$DEST_ACTION" == "list" ]] && echo "Listed only."
echo "Backup-internal dupes: $BK_DUPE_CNT$BK_DUPE_NOTE"
[[ $SRC_DUPE_CNT -gt 0 ]] && echo "Source-internal dupes: $SRC_DUPE_CNT" || true
if $VIDEO_FAST && ! $EXACT; then echo "Similar (video-fast) flagged: $SIMILAR_CNT"; fi
if ${BAD_VIDEO_DETECT:-true}; then echo "Bad videos auto-handled: ON (${BAD_VIDEO_ACTION})"; fi
if ${IGNORE_APPLEDOUBLE:-true}; then echo "AppleDouble sidecars (._*) handled: ${APPLEDOUBLE_ACTION}"; fi
echo "===================="