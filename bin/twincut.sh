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
SIM_GROUP_ID=0
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

# vid_eq helper / lib loading
SELF_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
LIB_DIR=""
if   [[ -d "$SELF_DIR/../lib" ]]; then LIB_DIR="$(cd -- "$SELF_DIR/../lib" && pwd -P)"
elif [[ -d "$SELF_DIR/lib"     ]]; then LIB_DIR="$(cd -- "$SELF_DIR/lib"     && pwd -P)"
fi
if [[ -n "$LIB_DIR" && -f "$LIB_DIR/events.sh" ]]; then
  # shellcheck source=../lib/events.sh
  source "$LIB_DIR/events.sh"
fi
THUMB_LIB_LOADED=false
if [[ -n "$LIB_DIR" && -f "$LIB_DIR/thumb.sh" ]]; then
  # shellcheck source=../lib/thumb.sh
  source "$LIB_DIR/thumb.sh"
  THUMB_LIB_LOADED=true
fi
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

# Self-check (single-folder mode; sugar over source self-check)
SELF_CHECK_MODE=false
SELF_CHECK_DIR=""
INCLUDE_SIMILAR_VIDEO=false

# P0: manifest / run-id
RUN_ID=""
MANIFEST_FILE=""
MANIFEST_INITED=false

# P0: skip counters
SKIPPED_HARDLINK=0
SKIPPED_SYMLINK=0

# P0: symlink policy (default: do NOT follow)
FOLLOW_SYMLINKS=false
FIND_FOLLOW=""   # set to "-L" when FOLLOW_SYMLINKS=true

# P0: exit code policy
EXIT_CODE_ON_DUPES=false

# P0: restore mode
RESTORE_MODE=false
RESTORE_MANIFEST=""
RESTORE_DRY_RUN=false

# P1: thumbnail detect
THUMB_DETECT=false
THUMB_DIR=""
THUMB_REVIEW_CSV=""
THUMB_ACTION="move"            # move | list | review
THUMB_MAX_EDGE=512
THUMB_MAYBE_MAX_EDGE=1024
THUMB_REQUIRE_EXIF_MATCH=false
# P1 wave 2: L1 perceptual-hash knobs (env-only, no CLI flag)
THUMB_PHASH_ENABLED="${THUMB_PHASH_ENABLED:-true}"
THUMB_PHASH_HAMMING="${THUMB_PHASH_HAMMING:-5}"
THUMB_PHASH_ALGO="${THUMB_PHASH_ALGO:-dhash}"
THUMB_PHASH_HASH_SIZE="${THUMB_PHASH_HASH_SIZE:-8}"

# Mode flag
DO_THUMB=false
THUMB_DETECT_APPLY=false   # --thumbnail-detect-apply: apply mode (no scan)

# Web UI integration: NDJSON event stream + per-file exclusion
JSON_EVENTS=false
JSON_IN=false              # --json-in: read ApplyCommand JSON-lines from stdin
EXCLUDE_PATHS=()

# Apply-list mode: when set, twincut skips scan/match and just executes
# the moves listed in the TSV (the Web UI uses this so the user-chosen
# keep/quarantine assignments are honored verbatim — see process_apply_list).
APPLY_LIST=""

# ------------------------------ Small helpers --------------------------------

# Look up a row from a .video_meta_index.csv. Echoes "dur w h fps bps" for the
# given path, or empty if not found. Numeric fields default to 0 in callers.
_video_meta_lookup(){
  local _csv="$1" _p="$2"
  [[ -z "$_csv" || ! -f "$_csv" ]] && return 0
  awk -F'\t' -v p="$_p" 'NR>2 && $1==p {print $3,$5,$6,$9,$10; exit}' "$_csv" 2>/dev/null
}

# Emit a dup_group event for a similar-video pair. Looks up per-side metadata
# from the supplied meta CSVs (one per side, since cross-check pairs span the
# backup VMETA_FILE and source SVMETA_FILE).
#   $1 reason (video_fast | video_strict)
#   $2 keep_path     $3 keep_meta_csv
#   $4 remove_path   $5 remove_meta_csv
emit_similar_video_group(){
  $JSON_EVENTS || return 0
  local _reason="$1" _keep="$2" _kcsv="$3" _rm="$4" _rcsv="$5"
  SIM_GROUP_ID=$((SIM_GROUP_ID+1))
  local _kdur _kw _kh _kfps _kbps _ddur _dw _dh _dfps _dbps
  read -r _kdur _kw _kh _kfps _kbps < <(_video_meta_lookup "$_kcsv" "$_keep")
  read -r _ddur _dw _dh _dfps _dbps < <(_video_meta_lookup "$_rcsv" "$_rm")
  local _ksz _kmt _rsz _rmt
  _ksz="$(fsize "$_keep")"; _kmt="$(mtime "$_keep")"
  _rsz="$(fsize "$_rm")";   _rmt="$(mtime "$_rm")"
  emit_dup_group --group-id "$SIM_GROUP_ID" --match-reason "$_reason" \
    --keep-path "$_keep" --keep-size "${_ksz:-0}" --keep-mtime "${_kmt:-0}" \
    --keep-duration "${_kdur:-0}" --keep-width "${_kw:-0}" --keep-height "${_kh:-0}" \
    --keep-fps "${_kfps:-0}" --keep-bitrate "${_kbps:-0}" \
    --remove-json "$(dup_remove_json "$_rm" "${_rsz:-0}" "${_rmt:-0}" "${_ddur:-0}" "${_dw:-0}" "${_dh:-0}" "${_dfps:-0}" "${_dbps:-0}")"
}

# True if path was passed via --exclude-path. Compared by exact string match
# after trailing-slash normalization.
is_excluded(){
  local p="${1%/}" e
  for e in ${EXCLUDE_PATHS[@]+"${EXCLUDE_PATHS[@]}"}; do
    [[ "${e%/}" == "$p" ]] && return 0
  done
  return 1
}

die(){
  emit_error --code usage_error --detail "$*"
  echo "ERROR: $*" >&2; exit 2;
}
die3(){
  emit_error --code runtime_error --detail "$*"
  echo "ERROR: $*" >&2; exit 3;
}
mtime(){ stat -c %Y -- "$1" 2>/dev/null || stat -f %m -- "$1" 2>/dev/null || echo 0; }
fsize(){ stat -c %s -- "$1" 2>/dev/null || stat -f %z -- "$1" 2>/dev/null || echo 0; }

# (device,inode) — true if a and b are the same physical file (hardlink / same path)
same_inode(){
  [[ -e "$1" && -e "$2" ]] || return 1
  local a b
  # GNU (-c) first, BSD (-f) fallback. On Linux `stat -f` means --file-system,
  # so 'stat -f %d:%i' there prints the (filename-bearing) fs status to stdout
  # AND exits non-zero — the old -f-first order then concatenated that garbage
  # with the -c result, making two hardlinks compare unequal. -c-first succeeds
  # cleanly on Linux; on macOS -c errors with no stdout, falling through to -f.
  a="$(stat -c '%d:%i' -- "$1" 2>/dev/null || stat -f '%d:%i' -- "$1" 2>/dev/null || echo "")"
  b="$(stat -c '%d:%i' -- "$2" 2>/dev/null || stat -f '%d:%i' -- "$2" 2>/dev/null || echo "")"
  [[ -n "$a" && "$a" == "$b" ]]
}

# ------------------------------ Manifest -------------------------------------
init_manifest(){
  $MANIFEST_INITED && return 0
  RUN_ID="${TWINCUT_RUN_ID:-${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}}"
  mkdir -p "$QUAR_DIR" || die3 "cannot create quarantine dir: $QUAR_DIR"
  local suffix=""
  $DRY_RUN && suffix=".dryrun"
  MANIFEST_FILE="$QUAR_DIR/_manifest-${RUN_ID}${suffix}.tsv"
  {
    printf '# twincut manifest v1  run_id=%s  started=%s  dry_run=%s  source=%s\n' \
      "$RUN_ID" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$DRY_RUN" "${SOURCE_DIR:-}"
    printf 'run_id\ttimestamp\toriginal_path\tquarantine_path\tmatched\talgo\thash\tdecision\tsize\tmtime\n'
  } > "$MANIFEST_FILE" || die3 "cannot write manifest: $MANIFEST_FILE"
  MANIFEST_INITED=true
  echo "[*] Manifest: $MANIFEST_FILE"
}

# manifest_append ORIG QUAR_PATH MATCHED HASH DECISION
manifest_append(){
  init_manifest
  local orig="$1" quar="$2" matched="$3" hh="$4" dec="$5"
  local sz="" mt=""
  if [[ -n "$quar" && -e "$quar" ]]; then sz="$(fsize "$quar")"; mt="$(mtime "$quar")"
  elif [[ -e "$orig" ]]; then sz="$(fsize "$orig")"; mt="$(mtime "$orig")"
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$RUN_ID" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$orig" "$quar" "$matched" "$ALGO" "$hh" "$dec" "$sz" "$mt" \
    >> "$MANIFEST_FILE"
}

# Process an apply-list TSV: each row is "move<TAB>keep<TAB>group<TAB>reason<TAB>hash".
# Skips scan entirely and just executes the moves through qmove so we get
# manifest writes, hardlink safety, and dry-run handling for free.
# The destination subdir is derived from the match reason so md5 clusters
# end up in _self_dupes/ and similar-video clusters in _similar_video_source/,
# matching the scan-mode layout.
process_apply_list(){
  [[ -f "$APPLY_LIST" ]] || die3 "--apply-list file not found: $APPLY_LIST"
  init_manifest
  local _row=0 _md5_dir="$QUAR_DIR/${SOURCE_DUPE_SUBDIR:-_self_dupes}"
  local _sim_dir="$QUAR_DIR/${SOURCE_SIMILAR_SUBDIR:-_similar_video_source}"
  local _move _keep _gid _reason _hash _sub _dec
  while IFS=$'\t' read -r _move _keep _gid _reason _hash; do
    # Strip trailing \r in case the apply-list was written with CRLF line
    # endings. Without this, cross_video_strict\r would fall through the
    # case arm to the md5 default and route to the wrong subdir.
    _move="${_move%$'\r'}"; _keep="${_keep%$'\r'}"
    _reason="${_reason%$'\r'}"; _hash="${_hash%$'\r'}"
    [[ -z "$_move" ]] && continue
    case "$_move" in '#'*) continue ;; esac
    _row=$((_row+1))
    if [[ ! -e "$_move" ]]; then
      emit_warn --code missing_file --path "$_move" --detail "apply-list source not found"
      continue
    fi
    case "$_reason" in
      cross_hash|cross_video_fast|cross_video_strict)
        _sub="$QUAR_DIR"; _dec="apply_list_${_reason}" ;;
      video_fast|video_strict)
        _sub="$_sim_dir"; _dec="apply_list_${_reason}" ;;
      *)
        _sub="$_md5_dir"; _dec="apply_list_${_reason:-md5}" ;;
    esac
    mkdir -p "$_sub"
    if qmove "$_move" "$_sub" "$_keep" "$_hash" "$_dec"; then
      $DRY_RUN || MOVED=$((MOVED+1))
      DUPES=$((DUPES+1))
      case "$_reason" in video_fast|video_strict) SIMILAR_CNT=$((SIMILAR_CNT+1)) ;; esac
    fi
  done < "$APPLY_LIST"
  TOTAL=$_row
}

# _resolve_abs PATH — resolve to canonical absolute path via python3.
# Returns empty string on failure (path non-existent or python3 absent).
# python3 is used because macOS `realpath` semantics vary across versions.
_resolve_abs(){
  local p="$1"
  [[ -z "$p" ]] && { printf ''; return; }
  python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$p" 2>/dev/null || printf ''
}

# _is_under CHILD PARENT — return 0 if CHILD is strictly under PARENT.
# Both arguments must be absolute paths (no trailing slash required).
# Returns 1 for empty arguments or paths outside the parent tree.
_is_under(){
  local child="$1" parent="$2"
  [[ -z "$child" || -z "$parent" ]] && return 1
  parent="${parent%/}"
  [[ "$child" == "$parent" ]] && return 0
  [[ "$child" == "$parent"/* ]] && return 0
  return 1
}

# _validate_decision DECISION SRC
#   Returns 0 if DECISION is in the canonical 5-value thumbnail allowlist.
#   Emits apply_failed and returns 1 otherwise.
_validate_decision(){
  local decision="$1" src="$2"
  case "$decision" in
    thumb_l1_review|thumb_l2_exif|thumb_l3_embed|thumb_confirmed|keep_user_override)
      return 0 ;;
    *)
      emit_error --code apply_failed --path "$src" \
        --detail "unknown decision '$decision'"
      return 1 ;;
  esac
}

# process_apply_list_jsonin — apply mode via --json-in.
# Reads ApplyCommand JSON-lines from stdin via jq, validates each command,
# and dispatches to qmove. Emits emit_run_end when done.
# Allowed ApplyCommand types: apply_move, apply_skip.
# Allowed decisions (both branches): thumb_l1_review, thumb_l2_exif,
#   thumb_l3_embed, thumb_confirmed, keep_user_override.
process_apply_list_jsonin(){
  if ! command -v jq >/dev/null 2>&1; then
    emit_error --code usage_error --detail "jq required for --json-in mode"
    die "jq required for --json-in mode"
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    emit_error --code usage_error --detail "python3 required for path validation in --json-in mode"
    die "python3 required for path validation in --json-in mode"
  fi

  # Buffer stdin once so we can pre-validate without losing the stream.
  # Apply lists for thumbnail_detect are bounded (typically <1MB).
  local stdin_input
  stdin_input=$(cat)

  # Zero-command apply is a no-op success (smoke gap D2).
  if [[ -z "$stdin_input" ]]; then
    emit_run_end --status succeeded --total 0 --applied 0 --skipped 0
    return 0
  fi

  # Pre-flight: every input line must be valid JSON (smoke gap D3).
  if ! printf '%s' "$stdin_input" | jq -c '.' >/dev/null 2>&1; then
    emit_error --code apply_failed \
      --detail "malformed apply input (not valid JSON)"
    emit_run_end --status failed --total 0 --applied 0 --skipped 0
    return 1
  fi

  local total=0 moved=0 skipped=0 apply_total=0
  local _type src dst_dir keeper decision
  local _enc_type _enc_src _enc_dst _enc_keeper _enc_dec
  local src_root abs_src abs_dst
  src_root="$(_resolve_abs "$SOURCE_DIR")"
  # Pre-count applicable records so emit_progress can report --total (Stage 10 T2).
  # Uses the already-buffered $stdin_input (line ~415); do not re-read stdin here.
  apply_total=$(printf '%s' "$stdin_input" | jq -c 'select(.type == "apply_move" or .type == "apply_skip")' | wc -l)
  apply_total=$((apply_total + 0))
  # NUL-safe field transport: jq emits each field as @base64, one per line.
  # base64 output contains only [A-Za-z0-9+/=] so newline is an unambiguous
  # record separator.  bash then decodes; tab/newline in paths are preserved.
  while IFS= read -r _enc_type   && \
        IFS= read -r _enc_src    && \
        IFS= read -r _enc_dst    && \
        IFS= read -r _enc_keeper && \
        IFS= read -r _enc_dec; do
    _type=$(printf '%s' "$_enc_type"   | base64 --decode)
    src=$(printf '%s' "$_enc_src"      | base64 --decode)
    dst_dir=$(printf '%s' "$_enc_dst"  | base64 --decode)
    keeper=$(printf '%s' "$_enc_keeper"| base64 --decode)
    decision=$(printf '%s' "$_enc_dec" | base64 --decode)
    total=$((total+1))
    emit_progress --phase apply --done "$total" --total "$apply_total" --current-path "$src"
    abs_src="$(_resolve_abs "$src")"
    abs_dst="$(_resolve_abs "$dst_dir")"
    case "$_type" in
      apply_move)
        if ! _is_under "$abs_src" "$src_root"; then
          emit_error --code apply_failed --path "$src" \
            --detail "src not under \$SOURCE_DIR ($src_root)"
          skipped=$((skipped+1)); continue
        fi
        if ! _is_under "$abs_dst" "$src_root"; then
          emit_error --code apply_failed --path "$src" \
            --detail "dst_dir not under \$SOURCE_DIR ($src_root): $dst_dir"
          skipped=$((skipped+1)); continue
        fi
        _validate_decision "$decision" "$src" || { skipped=$((skipped+1)); continue; }
        if [[ ! -e "$src" ]]; then
          emit_warn --code missing_file --path "$src" \
            --detail "apply src not on disk"
          skipped=$((skipped+1)); continue
        fi
        mkdir -p "$dst_dir" || { emit_warn --code io_error --path "$dst_dir" \
          --detail "mkdir failed"; skipped=$((skipped+1)); continue; }
        if qmove "$src" "$dst_dir" "$keeper" "" "$decision"; then
          moved=$((moved+1))
        else
          skipped=$((skipped+1))
        fi
        ;;
      apply_skip)
        if ! _is_under "$abs_src" "$src_root"; then
          emit_error --code apply_failed --path "$src" \
            --detail "src not under \$SOURCE_DIR ($src_root)"
          skipped=$((skipped+1)); continue
        fi
        _validate_decision "$decision" "$src" || { skipped=$((skipped+1)); continue; }
        emit_action_skip --src "$src" --decision "$decision" --reason user_override
        skipped=$((skipped+1))
        ;;
      *)
        emit_error --code apply_failed --path "$src" \
          --detail "unknown ApplyCommand type '$_type'"
        skipped=$((skipped+1))
        ;;
    esac
  done < <(printf '%s' "$stdin_input" | jq -rj 'select(.type == "apply_move" or .type == "apply_skip") |
                   (.type         | @base64), "\n",
                   (.src     // ""| @base64), "\n",
                   (.dst_dir // ""| @base64), "\n",
                   (.keeper  // ""| @base64), "\n",
                   (.decision// ""| @base64), "\n"')
  emit_run_end --status succeeded --total "$total" --applied "$moved" --skipped "$skipped"
}

# qmove SRC DEST_DIR MATCHED HASH DECISION
# Centralized "move into quarantine" with hardlink check + manifest write.
# Returns 0 on action taken, 1 on skip (e.g. hardlink), 2 on error.
qmove(){
  local src="$1" dir="$2" matched="$3" hh="$4" dec="$5"
  if is_excluded "$src"; then
    emit_action_skip --src "$src" --reason excluded --decision "$dec"
    return 1
  fi
  if [[ -n "$matched" ]] && same_inode "$src" "$matched"; then
    SKIPPED_HARDLINK=$((SKIPPED_HARDLINK+1))
    echo "[=] hardlink-skip: '$src' == '$matched'"
    emit_action_skip --src "$src" --matched "$matched" --reason hardlink --decision "$dec"
    return 1
  fi
  mkdir -p "$dir" || {
    echo "ERROR: mkdir $dir failed" >&2
    emit_warn --code io_error --path "$dir" --detail "mkdir failed"
    return 2
  }
  local base dest i=1
  base="$(basename -- "$src")"
  dest="$dir/$base"
  while [[ -e "$dest" ]]; do
    if [[ "$base" == *.* ]]; then dest="$dir/${base%.*}_$i.${base##*.}"
    else                          dest="$dir/${base}_$i"; fi
    i=$((i+1))
  done
  if $DRY_RUN; then
    echo "[DRY] mv \"$src\" \"$dest\""
    emit_action_move --src "$src" --dst "$dest" --dry-run true --matched "$matched" --decision "$dec"
  else
    mv -- "$src" "$dest" || {
      echo "ERROR: mv failed: $src -> $dest" >&2
      emit_warn --code io_error --path "$src" --detail "mv failed -> $dest"
      return 2
    }
    emit_action_move --src "$src" --dst "$dest" --dry-run false --matched "$matched" --decision "$dec"
  fi
  manifest_append "$src" "$dest" "$matched" "$hh" "$dec"
  return 0
}

# qdelete SRC MATCHED HASH DECISION
qdelete(){
  local src="$1" matched="$2" hh="$3" dec="$4"
  if is_excluded "$src"; then
    emit_action_skip --src "$src" --reason excluded --decision "$dec"
    return 1
  fi
  if [[ -n "$matched" ]] && same_inode "$src" "$matched"; then
    SKIPPED_HARDLINK=$((SKIPPED_HARDLINK+1))
    echo "[=] hardlink-skip: '$src' == '$matched'"
    emit_action_skip --src "$src" --matched "$matched" --reason hardlink --decision "$dec"
    return 1
  fi
  if $DRY_RUN; then
    echo "[DRY] rm \"$src\""
    emit_action_delete --src "$src" --dry-run true --matched "$matched" --decision "$dec"
  else
    rm -f -- "$src" || {
      echo "ERROR: rm failed: $src" >&2
      emit_warn --code io_error --path "$src" --detail "rm failed"
      return 2
    }
    emit_action_delete --src "$src" --dry-run false --matched "$matched" --decision "$dec"
  fi
  manifest_append "$src" "" "$matched" "$hh" "${dec}:deleted"
  return 0
}

# Backwards-compatible wrapper. Kept so older callers/tests still work.
move_unique(){ qmove "$1" "$2" "" "" "legacy_move" >/dev/null || true; }

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
  grep -v '^#' "$cache_file" 2>/dev/null | awk -F '\t' 'NF>=2 {print $1"\t"$2}' | while IFS=$'\t' read -r hh pp; do [[ -e "$pp" ]] && printf "%s\t%s\n" "$hh" "$pp" >> "$tmp" || true; done
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
  done < <(find $FIND_FOLLOW "$dir" -type f \( "$@" \) -size +"$MIN_SIZE" -print0)
  echo "$vcsv"
}

# One place to interpret BAD_VIDEO action
handle_bad_video(){
  local f="$1"
  emit_warn --code bad_video --path "$f" --detail "ffprobe failed or zero metadata"
  case "$BAD_VIDEO_ACTION" in
    list)   echo "[BAD-VIDEO] $f" ;;
    delete) qdelete "$f" "" "" "bad_video" ;;
    move)   qmove   "$f" "$QUAR_DIR/$BAD_VIDEO_SUBDIR" "" "" "bad_video" ;;
    ignore) : ;;
  esac
}

# AppleDouble dispatcher (centralized so we can manifest it)
handle_appledouble(){
  local f="$1"
  emit_warn --code appledouble --path "$f" --detail "AppleDouble sidecar"
  case "$APPLEDOUBLE_ACTION" in
    list)   echo "[APPLEDOUBLE] $f" ;;
    delete) qdelete "$f" "" "" "appledouble" ;;
    move)   qmove   "$f" "$QUAR_DIR/$APPLEDOUBLE_SUBDIR" "" "" "appledouble" ;;
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
  local rc="${1:-2}"
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
                   [--follow-symlinks|--no-follow-symlinks]
                   [--exit-code-on-dupes]

Thumbnail detection (L1 resolution + L2 EXIF cluster + L3 embedded thumb):
  $(basename "$0") --source <DIR> --thumbnail-detect
                   [--thumb-action move|list|review]
                   [--thumb-dir <DIR>]            (default: <source>/_thumbnails)
                   [--thumb-max-edge 512] [--thumb-maybe-max-edge 1024]
                   [--thumb-require-exif-match]
                   [--thumb-review-csv <PATH>]

Self-check (find duplicates within a single folder, role-agnostic):
  $(basename "$0") --self-check <DIR> [--dry-run] [--include-similar-video]
                   [--algo md5|sha1] [--min-size 300k] [--ext "jpg,png,..."]
                   [--quarantine <DIR>]
  Default action: move duplicates into <DIR>/_QUARANTINE/_self_dupes/ and
  write a manifest. Use --dry-run to preview without moving anything. The
  full restore command is printed at the end of the run; you can also call
  $(basename "$0") --restore <manifest.tsv> manually to roll back.

Restore mode:
  $(basename "$0") --restore <manifest.tsv> [--restore-dry-run]
                   [--quarantine <DIR>]   # only needed if manifest paths are relative

Web UI integration (intended for the twincut-ui Go server, but usable manually):
  --json-events           Emit one NDJSON event per line on stdout for the
                          run lifecycle (run_start, progress, dup_group,
                          action, warn, error, run_end). Existing human-
                          readable output is routed to stderr so stdout
                          stays a clean event stream. Set TWINCUT_RUN_ID
                          in the env to control the run_id.
  --exclude-path <PATH>   Skip this exact path when moving/deleting (the
                          UI uses this to honor per-file unchecks). Repeat
                          the flag for multiple paths.
  --apply-list <FILE>     Skip scan/match and execute the moves listed in
                          this TSV instead. Each row:
                            move_path<TAB>keep_path<TAB>group_id<TAB>match_reason<TAB>hash
                          Reuses qmove for manifest writes + hardlink safety
                          + dry-run support. Used by the Web UI's Apply step
                          so the user can override which file is the keeper.

Exit codes:
  0  normal completion
  1  normal completion AND duplicates were processed (only with --exit-code-on-dupes)
  2  usage / argument error
  3  runtime error (I/O, missing dep, mv/rm failure, etc.)

Notes:
  - During --dry-run, a source-side cache (<source>/.source_hashindex.txt) is created/updated.
  - After a successful non-dry run, the source cache is removed unless --keep-source-cache.
  - Default video-fast is enabled (size±0.5%, dur±0.3s). Use --exact for hash-only.
  - In --video-fast-strict, join also compares fps/bitrate (if present) and re-verifies via vid_eq's metadata-level EQUAL check.
  - Symlinks are NOT followed by default. Use --follow-symlinks to opt in.
  - Every action (move/delete) is recorded in <quarantine>/_manifest-<RUN_ID>.tsv.
    Use --restore <manifest> to roll back a previous run.
  - Modes:
    * Self-check (legacy): --report/--fix-*-dupes limit the run to that
      side. Source self-check still runs similar-video by default.
    * Self-check (recommended): --self-check <DIR> finds intra-folder
      duplicates without requiring source/backup roles. Hash-only by
      default; pass --include-similar-video to also flag similar videos.
    * Cross-check runs only when no self-check flag is present.
EOF
  exit "$rc"
}

# ------------------------------ Restore --------------------------------------
# Reads a manifest written by a prior run and moves files back to original_path.
# Conflict policy: if original_path already exists, SKIP and report; never overwrite.
# Delete-actions are unrecoverable and reported as such.
do_restore(){
  local mf="$1"
  [[ -f "$mf" ]] || die "manifest not found: $mf"

  local restored=0 skipped_exists=0 missing=0 unrecoverable=0 errors=0
  local done_marker="${mf}.restored"
  local already_done=""
  [[ -f "$done_marker" ]] && already_done="$(cat "$done_marker")"

  emit_run_start --mode restore --source "$mf"

  # Count restorable rows for the progress total. Cheap upfront walk.
  local total=0
  while IFS=$'\t' read -r run_id _rest; do
    [[ -z "${run_id:-}" ]] && continue
    [[ "$run_id" == "run_id" ]] && continue
    [[ "${run_id:0:1}" == "#" ]] && continue
    total=$((total+1))
  done < "$mf"

  echo "[*] Restoring from manifest: $mf"
  $RESTORE_DRY_RUN && echo "[*] (restore dry-run; no files will move)"

  local seen=0
  # IFS=$'\t' read -r -a collapses consecutive tabs (tab is IFS whitespace).
  # Work around by substituting tabs with ASCII FS (\034), a non-whitespace
  # IFS character that bash does NOT collapse. This preserves empty fields
  # (e.g. empty quar for :deleted rows) without spawning cut subprocesses.
  while IFS= read -r _raw_line; do
    IFS=$'\034' read -r -a F <<< "${_raw_line//$'\t'/$'\034'}"
    local run_id="${F[0]:-}"
    [[ -z "$run_id" ]] && continue
    [[ "$run_id" == "run_id" ]] && continue
    [[ "${run_id:0:1}" == "#" ]] && continue

    local orig="${F[2]:-}"
    local quar="${F[3]:-}"
    local dec="${F[7]:-}"

    if [[ -n "$already_done" ]] && grep -Fqx -- "$orig" <<<"$already_done"; then
      continue
    fi

    seen=$((seen+1))
    emit_progress --phase restore --done "$seen" --total "$total" --current-path "$orig"

    if [[ "$dec" == *":deleted" ]]; then
      echo "[unrecoverable] deleted: $orig"
      emit_action_restore --kind restore_unrecoverable --src "$orig" --dst "" --dry-run "$RESTORE_DRY_RUN"
      unrecoverable=$((unrecoverable+1))
      continue
    fi

    if [[ -z "$quar" || ! -e "$quar" ]]; then
      if [[ -z "$quar" ]]; then
        echo "[skip] no quarantine path recorded: $orig"
      else
        echo "[missing] quarantine file gone: $quar"
      fi
      emit_action_restore --kind restore_missing --src "$quar" --dst "$orig" --dry-run "$RESTORE_DRY_RUN"
      missing=$((missing+1))
      continue
    fi

    if [[ -e "$orig" ]]; then
      echo "[conflict] original exists, skipping: $orig"
      emit_action_restore --kind restore_conflict --src "$quar" --dst "$orig" --dry-run "$RESTORE_DRY_RUN"
      skipped_exists=$((skipped_exists+1))
      continue
    fi

    if $RESTORE_DRY_RUN; then
      echo "[DRY] mv \"$quar\" \"$orig\""
      emit_action_restore --kind restore --src "$quar" --dst "$orig" --dry-run true
      restored=$((restored+1))
      continue
    fi

    mkdir -p "$(dirname -- "$orig")" || { errors=$((errors+1)); continue; }
    if mv -- "$quar" "$orig"; then
      emit_action_restore --kind restore --src "$quar" --dst "$orig" --dry-run false
      restored=$((restored+1))
      printf '%s\n' "$orig" >> "$done_marker"
    else
      echo "ERROR: mv failed: $quar -> $orig" >&2
      emit_error --code mv_failed --detail "$quar -> $orig"
      errors=$((errors+1))
    fi
  done < "$mf"

  echo "===== RESTORE SUMMARY ====="
  echo "Restored:        $restored"
  echo "Skipped (exists): $skipped_exists"
  echo "Missing:         $missing"
  echo "Unrecoverable:   $unrecoverable"
  echo "Errors:          $errors"
  echo "==========================="

  local restore_status=succeeded
  [[ "$errors" -gt 0 ]] && restore_status=failed
  emit_run_end --status "$restore_status" \
    --restored "$restored" \
    --skipped "$skipped_exists" \
    --missing "$missing" \
    --unrecoverable "$unrecoverable" \
    --errors "$errors" \
    --cancelled false

  if [[ "$errors" -gt 0 ]]; then exit 3; fi
  exit 0
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

    --self-check) SELF_CHECK_MODE=true; SELF_CHECK_DIR="${2:-}"; [[ $# -ge 2 ]] && shift 2 || shift;;
    --include-similar-video) INCLUDE_SIMILAR_VIDEO=true; shift;;

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

    --follow-symlinks)    FOLLOW_SYMLINKS=true;  shift;;
    --no-follow-symlinks) FOLLOW_SYMLINKS=false; shift;;

    --exit-code-on-dupes) EXIT_CODE_ON_DUPES=true; shift;;

    --json-events) JSON_EVENTS=true; shift;;
    --json-in)     JSON_IN=true; shift;;
    --exclude-path) EXCLUDE_PATHS+=("${2:-}"); shift 2;;
    --apply-list)  APPLY_LIST="${2:-}"; shift 2;;

    --thumbnail-detect-apply) THUMB_DETECT_APPLY=true; shift;;

    --restore)         RESTORE_MODE=true; RESTORE_MANIFEST="${2:-}"; shift 2;;
    --restore-dry-run) RESTORE_DRY_RUN=true; shift;;

    --thumbnail-detect)        THUMB_DETECT=true; DO_THUMB=true; shift;;
    --thumb-action)            THUMB_ACTION="${2:-}"; shift 2;;
    --thumb-dir)               THUMB_DIR="${2:-}"; shift 2;;
    --thumb-max-edge)          THUMB_MAX_EDGE="${2:-}"; shift 2;;
    --thumb-maybe-max-edge)    THUMB_MAYBE_MAX_EDGE="${2:-}"; shift 2;;
    --thumb-require-exif-match) THUMB_REQUIRE_EXIF_MATCH=true; shift;;
    --thumb-review-csv)        THUMB_REVIEW_CSV="${2:-}"; shift 2;;

    -h|--help) usage 0;;
    *) echo "Unknown option: $1" >&2; usage 2;;
  esac
done

# --json-events output discipline:
#   stdout becomes the pure NDJSON event stream; existing human-readable
#   chatter is re-routed to stderr (where the Web UI captures it as the
#   raw log panel). Implementation: save real stdout as fd 3, redirect
#   fd 1 to fd 2. The typed emit_* helpers (lib/events.sh) write to fd 3.
if $JSON_EVENTS; then
  exec 3>&1 1>&2
  RUN_ID="${TWINCUT_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
fi

# --json-in validation: only valid with --thumbnail-detect-apply and --json-events.
if $JSON_IN; then
  if ! $THUMB_DETECT_APPLY; then
    die "--json-in only valid with --thumbnail-detect-apply"
  fi
  if ! $JSON_EVENTS; then
    die "--json-in requires --json-events"
  fi
fi

# Apply symlink policy
if $FOLLOW_SYMLINKS; then FIND_FOLLOW="-L"; else FIND_FOLLOW=""; fi

# Restore mode short-circuits everything else
if $RESTORE_MODE; then
  [[ -n "$RESTORE_MANIFEST" ]] || die "--restore requires a manifest path"
  do_restore "$RESTORE_MANIFEST"
fi

# --thumbnail-detect-apply --json-in: read ApplyCommand JSON-lines from stdin.
if $THUMB_DETECT_APPLY && $JSON_IN; then
  emit_run_start --mode thumbnail_detect_apply --source "${SOURCE_DIR:-}"
  init_manifest
  process_apply_list_jsonin
  exit 0
fi

# -------------------------- Mode resolution/guards -------------------------
# Translate --self-check <DIR> into the existing source-self-check internal
# state, with self-check-specific defaults: hash-only (no similar-video
# unless opt-in), self-named quarantine subdir, quarantine inside <DIR>.
if $SELF_CHECK_MODE; then
  [[ -n "$SELF_CHECK_DIR" ]] || die "--self-check requires a directory"
  [[ -d "$SELF_CHECK_DIR" ]] || die "--self-check dir not found: $SELF_CHECK_DIR"
  [[ -n "$SOURCE_DIR" ]] && die "--self-check is mutually exclusive with --source"
  [[ ${#BACKUP_DIRS[@]} -gt 0 ]] && die "--self-check is mutually exclusive with --backup"
  ( $DO_SOURCE_SELF || $DO_BACKUP_SELF ) && die "--self-check is mutually exclusive with --report/--fix-*-dupes"
  $DO_THUMB && die "--self-check is mutually exclusive with --thumbnail-detect"

  SOURCE_DIR="$SELF_CHECK_DIR"
  DO_SOURCE_SELF=true
  if $DRY_RUN; then
    REPORT_SOURCE_DUPES=true
  else
    FIX_SOURCE_DUPES=true
  fi
  SOURCE_DUPE_SUBDIR="_self_dupes"
  SOURCE_DUPE_LOG="_self_dupes_map.csv"
  if [[ "$QUAR_DIR" == "./_QUARANTINE" ]]; then
    QUAR_DIR="${SELF_CHECK_DIR%/}/_QUARANTINE"
  fi
  if $INCLUDE_SIMILAR_VIDEO; then
    EXACT=false; VIDEO_FAST=true
  else
    EXACT=true; VIDEO_FAST=false
  fi
fi

# Cross-check auto-enables whenever source+backup are both provided AND no
# self-check mode is requested. --thumbnail-detect coexists peacefully.
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
if $DO_THUMB; then
  [[ -z "$SOURCE_DIR" ]] && die "--thumbnail-detect requires --source"
  $THUMB_LIB_LOADED || die "thumbnail lib not loaded; expected $LIB_DIR/thumb.sh"
  [[ -n "${APPLY_LIST:-}" ]] && die "--thumbnail-detect and --apply-list are mutually exclusive (separate apply paths)"
fi
# If only --thumbnail-detect is set with no other mode, that's fine (standalone).
# It can also coexist with cross/self checks; thumb phase runs after them.

# Re-apply strict thresholds after CLI is parsed (parser runs after initial defaults)
if $VIDEO_FAST_STRICT; then SIZE_PCT=0.2; DUR_SEC=0.15; fi
# vid_eq.sh runs as a child process; export so --size-pct/--dur-sec (and the
# strict-tightened values above) actually reach it.
export SIZE_PCT DUR_SEC

# Resolved-mode emission for the Web UI. One concise event with the active
# mode + the most relevant flags. Goes out before any disk-heavy work.
if $JSON_EVENTS; then
  _mode="cross_check"
  $SELF_CHECK_MODE && _mode="self_check"
  if ! $SELF_CHECK_MODE; then
    if $DO_SOURCE_SELF && ! $DO_CROSS; then _mode="source_self"; fi
    if $DO_BACKUP_SELF && ! $DO_CROSS; then _mode="backup_self"; fi
    if $DO_THUMB && ! $DO_CROSS && ! $DO_SOURCE_SELF && ! $DO_BACKUP_SELF; then
      # Standalone thumbnail-detect: discriminate preview vs non-preview.
      if $DRY_RUN; then _mode="thumbnail_detect_preview"
      else              _mode="thumbnail_detect"
      fi
    fi
  fi
  emit_run_start --mode "$_mode" --source "${SOURCE_DIR:-}" --dry-run "$DRY_RUN"
fi

# Early rebuild of video-meta if requested (non-destructive; honored even in dry-run)
if $REBUILD_VMETA; then
  if [[ -n "$SOURCE_DIR" ]]; then ensure_video_meta_index "$SOURCE_DIR" >/dev/null; fi
  for d in ${BACKUP_DIRS[@]+"${BACKUP_DIRS[@]}"}; do ensure_video_meta_index "$d" >/dev/null; done
fi

mkdir -p "$QUAR_DIR"

# Apply-list short-circuit: skip the full scan and just execute the moves
# requested by the Web UI. Falls through to the normal run_end emission at
# the bottom so counters/manifest land in the standard event stream.
if [[ -n "$APPLY_LIST" ]]; then
  process_apply_list
else

# 为每个备份目录建立/复用 hash 索引（带进度&抗中断）
if $DO_CROSS || $DO_BACKUP_SELF; then
for BDIR in ${BACKUP_DIRS[@]+"${BACKUP_DIRS[@]}"}; do
  LOCAL_CACHE="$BDIR/$CACHE_FILE"
  build_name_predicate
  TOTAL_B=$(find $FIND_FOLLOW "$BDIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" | wc -l | tr -d ' ')
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
  done < <(find $FIND_FOLLOW "$BDIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" -print0)
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
          mt="$(mtime "$p")"
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
            if qmove "$dp" "$BK_DUPE_DIR" "$KEEP_PATH" "$h" "backup_self_hash"; then
              DUPES=$((DUPES+1)); $DRY_RUN || MOVED=$((MOVED+1))
              printf '"%s","%s","%s"\n' "$h" "$KEEP_PATH" "$dp" >> "$BK_DUPE_CSV"
            fi
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
          handle_bad_video "$bf"
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
          mt_bf=$(mtime "$bf")
          mt_cd=$(mtime "$cand")
          if (( mt_bf <= mt_cd )); then KEEP="$bf"; MOVE="$cand"; else KEEP="$cand"; MOVE="$bf"; fi
          _sim_reason="video_fast"; $VIDEO_FAST_STRICT && _sim_reason="video_strict"
          emit_similar_video_group "$_sim_reason" "$KEEP" "$VMETA_FILE" "$MOVE" "$VMETA_FILE"
          if $REPORT_BACKUP_DUPES; then
            echo "[BACKUP-SIMILAR(video-fast)] keep='$KEEP' move='$MOVE'"
          fi
          if $FIX_BACKUP_DUPES; then
            DEC="backup_self_video_fast"; $VIDEO_FAST_STRICT && DEC="backup_self_video_strict"
            if qmove "$MOVE" "$B_SIM_DIR" "$KEEP" "" "$DEC"; then
              $DRY_RUN || echo "\"$MOVE\",\"$KEEP\"" >> "$B_SIM_CSV"
              SIMILAR_CNT=$((SIMILAR_CNT+1)); $DRY_RUN || MOVED=$((MOVED+1))
            fi
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
    done < <(find $FIND_FOLLOW "$BDIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" -print0)
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

TOTAL_SRC=$(find $FIND_FOLLOW "$SOURCE_DIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" | wc -l | tr -d ' ')
[[ -z "$TOTAL_SRC" ]] && TOTAL_SRC=0
echo "[*] Candidates in source: $TOTAL_SRC"
if ! $FOLLOW_SYMLINKS; then
  SKIPPED_SYMLINK=$(find "$SOURCE_DIR" -type l 2>/dev/null | wc -l | tr -d ' ')
  [[ "${SKIPPED_SYMLINK:-0}" -gt 0 ]] && echo "[*] Symlinks not followed in source: $SKIPPED_SYMLINK (use --follow-symlinks to opt in)"
fi

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
      handle_appledouble "$f"
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
    MATCH_LINE="$(grep -m1 "^${H}"$'\t' "$TMP_CACHE" || true)"
    if [[ -n "$MATCH_LINE" ]]; then
      MATCHED_PATH="${MATCH_LINE#*$'\t'}"
      DUPES=$((DUPES+1))
      _sz_keep="$(fsize "$MATCHED_PATH")"; _mt_keep="$(mtime "$MATCHED_PATH")"
      _sz_rm="$(fsize "$f")"; _mt_rm="$(mtime "$f")"
      emit_dup_group --group-id "$DUPES" --match-reason md5 --hash "$H" \
        --keep-path "$MATCHED_PATH" --keep-size "${_sz_keep:-0}" --keep-mtime "${_mt_keep:-0}" \
        --remove-json "$(dup_remove_json "$f" "${_sz_rm:-0}" "${_mt_rm:-0}")"
      case "$DEST_ACTION" in
        list) echo "[DUPE] $f  ~~  $MATCHED_PATH" ;;
        delete)
          if qdelete "$f" "$MATCHED_PATH" "$H" "cross_hash"; then
            $DRY_RUN || DELETED=$((DELETED+1))
          fi ;;
        move)
          if qmove "$f" "$QUAR_DIR" "$MATCHED_PATH" "$H" "cross_hash"; then
            $DRY_RUN || MOVED=$((MOVED+1))
          fi ;;
        *) die3 "Unknown action: $DEST_ACTION";;
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
          handle_bad_video "$f"
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
          _sim_reason="video_fast"; $VIDEO_FAST_STRICT && _sim_reason="video_strict"
          if $DO_CROSS; then
            # Cross: backup-side $b is the keeper; source-side $f is removed.
            emit_similar_video_group "$_sim_reason" "$b" "${VMETA_FILE:-}" "$f" "${SVMETA_FILE:-}"
            DEC="cross_video_fast"; $VIDEO_FAST_STRICT && DEC="cross_video_strict"
            if qmove "$f" "$SIM_DIR" "$b" "" "$DEC"; then
              $DRY_RUN || echo "\"$f\",\"$b\"" >> "$SIMILAR_CSV"
              echo "[SIMILAR(video-fast)] $f  ~~  $b"
              SIMILAR_CNT=$((SIMILAR_CNT+1)); $DRY_RUN || MOVED=$((MOVED+1))
            fi
          else
            # Source self-check: prefer oldest keep.
            # Note: --self-check without --include-similar-video sets EXACT=true
            # upstream, so the outer video-fast block is never entered and this
            # branch is only reached via the legacy --report/--fix-source-dupes
            # path or when --include-similar-video is explicitly set.
            mt_src=$(mtime "$f")
            mt_b=$(mtime "$b")
            if (( mt_src <= mt_b )); then KEEP="$f"; MOVE="$b"; else KEEP="$b"; MOVE="$f"; fi
            # Each source-self pair is reachable from both sides of the outer
            # find loop; canonicalize the pair and skip duplicates so the
            # event stream and the qmove decision happen exactly once.
            if [[ "$f" < "$b" ]]; then _pa="$f"; _pb="$b"; else _pa="$b"; _pb="$f"; fi
            _pkey="${_pa}"$'\x1f'"${_pb}"
            case ":${_SOURCE_SIM_SEEN:-}:" in *":${_pkey}:"*) break ;; esac
            _SOURCE_SIM_SEEN="${_SOURCE_SIM_SEEN:-}:${_pkey}"
            emit_similar_video_group "$_sim_reason" "$KEEP" "$SVMETA_FILE" "$MOVE" "$SVMETA_FILE"
            if $REPORT_SOURCE_DUPES; then
              echo "[SOURCE-SIMILAR(video-fast)] keep='$KEEP' move='$MOVE'"
            fi
            if $FIX_SOURCE_DUPES; then
              DEC="source_self_video_fast"; $VIDEO_FAST_STRICT && DEC="source_self_video_strict"
              if qmove "$MOVE" "$SIM_DIR" "$KEEP" "" "$DEC"; then
                $DRY_RUN || echo "\"$MOVE\",\"$KEEP\"" >> "$SIMILAR_CSV"
                SIMILAR_CNT=$((SIMILAR_CNT+1)); $DRY_RUN || MOVED=$((MOVED+1))
              fi
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

  if (( TOTAL % PROG_STEP == 0 )); then
    printf "\r[*] Scanned %-7d / %s | dupes: %-7d" "$TOTAL" "$TOTAL_SRC" "$DUPES"
    emit_progress --phase scan --done "$TOTAL" --total "${TOTAL_SRC:-0}" --current-path "$f"
  fi
done < <(find $FIND_FOLLOW "$SOURCE_DIR" -type f \( "${NAME_PREDICATE[@]}" \) -size +"$MIN_SIZE" -print0)
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
          [[ -n "$p" && -e "$p" ]] && printf "%s\t%s\n" "$h" "$p" || true
        done > "$SRC_FILTERED"

    SDUP_HASHES_FILE="$(mktemp)"
    awk -F '\t' 'NF>=2{print $1}' "$SRC_FILTERED" | sort | uniq -d > "$SDUP_HASHES_FILE"
    SDUP_COUNT=$(wc -l < "$SDUP_HASHES_FILE" | tr -d ' ')
    echo "[*] Source-internal duplicate hashes: $SDUP_COUNT"

    if [[ "$SDUP_COUNT" -gt 0 ]]; then
      _GROUP_ID=0
      while IFS= read -r sh; do
        SMAP_FILE="$(mktemp)"
        awk -F '\t' -v hh="$sh" '$1==hh{print $2}' "$SRC_FILTERED" > "$SMAP_FILE"

        # Keep policy: prefer oldest mtime within SOURCE_DIR
        KEEP_SPATH=""; KEEP_SMT=""
        while IFS= read -r sp; do
          [[ -z "$sp" ]] && continue
          smt="$(mtime "$sp")"
          if [[ -z "$KEEP_SPATH" || "$smt" -lt "$KEEP_SMT" ]]; then
            KEEP_SPATH="$sp"; KEEP_SMT="$smt"
          fi
        done < "$SMAP_FILE"

        _GROUP_ID=$((_GROUP_ID+1))
        if $JSON_EVENTS; then
          # Build remove[] entries (one per non-keep file) as --remove-json args.
          _rm_args=()
          while IFS= read -r _rp; do
            [[ -z "$_rp" || "$_rp" == "$KEEP_SPATH" ]] && continue
            _rsz="$(fsize "$_rp")"; _rmt="$(mtime "$_rp")"
            _rm_args+=( --remove-json "$(dup_remove_json "$_rp" "${_rsz:-0}" "${_rmt:-0}")" )
          done < "$SMAP_FILE"
          _ksz="$(fsize "$KEEP_SPATH")"; _kmt="$(mtime "$KEEP_SPATH")"
          emit_dup_group --group-id "$_GROUP_ID" --match-reason md5 --hash "$sh" \
            --keep-path "$KEEP_SPATH" --keep-size "${_ksz:-0}" --keep-mtime "${_kmt:-0}" \
            ${_rm_args[@]+"${_rm_args[@]}"}
        fi

        # Process all except KEEP_SPATH
        while IFS= read -r sdp; do
          [[ -z "$sdp" || "$sdp" == "$KEEP_SPATH" ]] && continue
          $REPORT_SOURCE_DUPES && echo "[SOURCE-DUPE] hash=$sh keep='$KEEP_SPATH' dupe='$sdp'"
          SRC_DUPE_CNT=$((SRC_DUPE_CNT+1))
          if $FIX_SOURCE_DUPES; then
            if qmove "$sdp" "$SRC_DUPE_DIR" "$KEEP_SPATH" "$sh" "source_self_hash"; then
              printf '"%s","%s","%s"\n' "$sh" "$KEEP_SPATH" "$sdp" >> "$SRC_DUPE_CSV"
              DUPES=$((DUPES+1)); $DRY_RUN || MOVED=$((MOVED+1))
            fi
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

# --- Thumbnail detect phase (P1: L1+L2+L3) ---
if $DO_THUMB; then
  thumb_detect_run || true
fi

fi  # end of "else" branch for the apply-list short-circuit

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
[[ $SKIPPED_HARDLINK -gt 0 ]] && echo "Skipped (hardlink): $SKIPPED_HARDLINK"
[[ $SKIPPED_SYMLINK  -gt 0 ]] && echo "Skipped (symlink):  $SKIPPED_SYMLINK"
$MANIFEST_INITED && echo "Manifest:           $MANIFEST_FILE"
if $SELF_CHECK_MODE && $MANIFEST_INITED && ! $DRY_RUN && [[ $MOVED -gt 0 ]]; then
  echo "[i] Inspect duplicates: $QUAR_DIR/$SOURCE_DUPE_SUBDIR/"
  echo "[i] Roll back this run: $(basename "$0") --restore \"$MANIFEST_FILE\""
fi
echo "===================="
$DO_THUMB && thumb_print_summary

emit_run_end --status succeeded --total "${TOTAL:-0}" \
  --moved "${MOVED:-0}" --deleted "${DELETED:-0}" \
  --manifest-path "${MANIFEST_FILE:-}" --cancelled false

# Exit code policy:
#   0 = normal
#   1 = normal AND duplicates were processed (only when --exit-code-on-dupes)
#   2 = arg error (handled inline via die / usage)
#   3 = runtime error (handled inline via die3)
if $EXIT_CODE_ON_DUPES && [[ $DUPES -gt 0 || $SIMILAR_CNT -gt 0 || $BK_DUPE_CNT -gt 0 || $SRC_DUPE_CNT -gt 0 || ${THUMB_L2_HITS:-0} -gt 0 || ${THUMB_L3_HITS:-0} -gt 0 ]]; then
  exit 1
fi
exit 0