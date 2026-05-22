#!/usr/bin/env bash
# lib/thumb.sh — thumbnail detection (Layers 1, 2, 3)
#
# Sourced by twincut.sh. Reads/writes globals from the main script:
#   THUMB_DIR             where confirmed thumbnails are moved
#   THUMB_REVIEW_CSV      where L1-only suspects (no L2/L3 evidence) are recorded
#   THUMB_ACTION          move | list | review
#   THUMB_MAX_EDGE        long-edge threshold for "thumb"   (default 512)
#   THUMB_MAYBE_MAX_EDGE  long-edge threshold for "maybe"   (default 1024)
#   THUMB_REQUIRE_EXIF_MATCH  bool — only act on L2/L3 evidence
#   SOURCE_DIR
#   QUAR_DIR              (for manifest reuse)
#
# Counters set by this lib (init in main):
#   THUMB_L1_MARKED       count of L1-suspect files
#   THUMB_L2_HITS         count moved/listed via L2 EXIF clustering
#   THUMB_L3_HITS         count moved/listed via L3 embedded thumb match
#   THUMB_REVIEW_CNT      count written to review.csv
#   THUMB_SKIPPED_NO_DIM  count skipped because dimensions could not be read
#
# Decisions written to manifest:
#   thumb_l2_exif         L2 cluster keep+move
#   thumb_l3_embed        L3 byte-level embedded-thumbnail match
#   thumb_l1_review       (review.csv only — no manifest row)

# ---- feature detection ------------------------------------------------------
THUMB_HAVE_EXIFTOOL=false
THUMB_HAVE_SIPS=false
THUMB_HAVE_IDENTIFY=false
command -v exiftool >/dev/null 2>&1 && THUMB_HAVE_EXIFTOOL=true
command -v sips     >/dev/null 2>&1 && THUMB_HAVE_SIPS=true
command -v identify >/dev/null 2>&1 && THUMB_HAVE_IDENTIFY=true

THUMB_IMG_EXTS_DEFAULT="jpg,jpeg,png,heic,heif,webp,gif,tiff,tif,bmp"

thumb_features_report(){
  if ! $THUMB_HAVE_SIPS && ! $THUMB_HAVE_IDENTIFY; then
    echo "[!] thumbnail-detect: neither 'sips' nor 'identify' found — L1 cannot run." >&2
    echo "    macOS ships sips by default; for Linux install ImageMagick (provides 'identify')." >&2
    return 1
  fi
  if ! $THUMB_HAVE_EXIFTOOL; then
    echo "[!] thumbnail-detect: 'exiftool' not found — L2 (EXIF clustering) and L3 (embedded thumb) will be SKIPPED." >&2
    echo "    Install: brew install exiftool   (or your distro's package manager)" >&2
  fi
  return 0
}

is_image_ext(){
  local ext="${1##*.}"; ext="$(printf '%s' "$ext" | tr '[:upper:]' '[:lower:]')"
  IFS=',' read -r -a _iexts <<< "$THUMB_IMG_EXTS_DEFAULT"
  for e in "${_iexts[@]}"; do [[ "$ext" == "$e" ]] && return 0; done
  return 1
}

# thumb_dimensions FILE → echoes "W H" or empty
thumb_dimensions(){
  local f="$1" w="" h="" out
  if $THUMB_HAVE_SIPS; then
    out=$(sips -g pixelWidth -g pixelHeight "$f" 2>/dev/null) || return 1
    w=$(awk '/pixelWidth:/  {print $2}' <<<"$out")
    h=$(awk '/pixelHeight:/ {print $2}' <<<"$out")
  elif $THUMB_HAVE_IDENTIFY; then
    read -r w h < <(identify -format '%w %h' "$f" 2>/dev/null) || return 1
  fi
  # Strict integer validation — sips can emit "<nil>" or "--" for odd files.
  [[ "$w" =~ ^[0-9]+$ && "$h" =~ ^[0-9]+$ && "$w" -gt 0 && "$h" -gt 0 ]] && echo "$w $h"
}

# thumb_classify_l1 W H → thumb | maybe | ok
thumb_classify_l1(){
  local w="$1" h="$2"
  local long=$(( w > h ? w : h ))
  local pixels=$(( w * h ))
  if (( long <= THUMB_MAX_EDGE )); then echo thumb; return; fi
  if (( long <= THUMB_MAYBE_MAX_EDGE )) && (( pixels <= 500000 )); then echo maybe; return; fi
  echo ok
}

# Build the L1 index for SOURCE_DIR.
# Side effects:
#   writes $THUMB_INDEX_FILE  (TSV: path \t w \t h \t l1class)
#   sets THUMB_L1_MARKED
thumb_build_l1_index(){
  THUMB_INDEX_FILE="$(mktemp)"
  THUMB_L1_MARKED=0
  THUMB_SKIPPED_NO_DIM=0
  local total=0 marked=0

  echo "[*] thumbnail-detect L1: scanning images in $SOURCE_DIR ..."
  while IFS= read -r -d '' f; do
    is_image_ext "$f" || continue
    total=$((total+1))
    local dims w h cls
    dims="$(thumb_dimensions "$f")" || { THUMB_SKIPPED_NO_DIM=$((THUMB_SKIPPED_NO_DIM+1)); continue; }
    [[ -z "$dims" ]] && { THUMB_SKIPPED_NO_DIM=$((THUMB_SKIPPED_NO_DIM+1)); continue; }
    read -r w h <<<"$dims"
    cls="$(thumb_classify_l1 "$w" "$h")"
    printf '%s\t%s\t%s\t%s\n' "$f" "$w" "$h" "$cls" >> "$THUMB_INDEX_FILE"
    [[ "$cls" != "ok" ]] && marked=$((marked+1))
  done < <(find $FIND_FOLLOW "$SOURCE_DIR" -type f -size +"$MIN_SIZE" -print0)

  THUMB_L1_MARKED="$marked"
  echo "[*] L1 indexed: $total images, $marked suspect ($THUMB_SKIPPED_NO_DIM unreadable)"
}

# Build EXIF fingerprint TSV by running exiftool ONCE on the source directory.
# Output format: fingerprint_sha1 \t path
# Skips files where critical EXIF fields are missing (those go to L1-only review).
thumb_build_l2_index(){
  THUMB_EXIF_FILE=""
  $THUMB_HAVE_EXIFTOOL || return 0

  THUMB_EXIF_FILE="$(mktemp)"
  echo "[*] thumbnail-detect L2: extracting EXIF fingerprints (exiftool) ..."

  # exiftool can recurse and emit a compact tab-separated table.
  # -fast2 skips heavy parsing; -m suppresses minor errors; -q -q quiets banner.
  # We deliberately read these fields:
  #   Model, Make, SerialNumber, LensModel,
  #   DateTimeOriginal, SubSecTimeOriginal,
  #   ExposureTime, FNumber, ISO
  # Missing fields print as "-" via -f.
  exiftool -r -fast2 -m -q -q -f \
    -T -FilePath \
    -Make -Model -SerialNumber -LensModel \
    -DateTimeOriginal -SubSecTimeOriginal \
    -ExposureTime -FNumber -ISO \
    -ext jpg -ext jpeg -ext heic -ext heif -ext tiff -ext tif -ext dng \
    "$SOURCE_DIR" 2>/dev/null \
  | while IFS=$'\t' read -r path mk md sn lens dto sst et fn iso; do
      [[ -z "$path" || "$path" == "-" ]] && continue
      # Need at minimum: a camera identity AND a capture timestamp.
      # If both are missing/placeholder we cannot fingerprint reliably.
      if [[ "$md" == "-" && "$mk" == "-" ]] || [[ "$dto" == "-" ]]; then
        continue
      fi
      local raw fp
      raw="${mk}|${md}|${sn}|${lens}|${dto}|${sst}|${et}|${fn}|${iso}"
      fp=$(printf '%s' "$raw" | (shasum 2>/dev/null || sha1sum) | awk '{print $1}')
      printf '%s\t%s\n' "$fp" "$path" >> "$THUMB_EXIF_FILE"
    done

  local rows; rows=$(wc -l < "$THUMB_EXIF_FILE" 2>/dev/null | tr -d ' ')
  echo "[*] L2 EXIF fingerprints: ${rows:-0} files indexed"
}

# L2 pass: cluster by fingerprint, keep largest pixel-count, qmove the rest.
thumb_run_l2(){
  THUMB_L2_HITS=0
  $THUMB_HAVE_EXIFTOOL || return 0
  [[ -s "${THUMB_EXIF_FILE:-}" ]] || return 0

  echo "[*] thumbnail-detect L2: clustering by EXIF fingerprint ..."

  # find duplicate fingerprints (those with ≥2 files)
  local dup_fps; dup_fps="$(mktemp)"
  awk -F'\t' '{c[$1]++} END{for (k in c) if (c[k]>1) print k}' "$THUMB_EXIF_FILE" > "$dup_fps"

  while IFS= read -r fp; do
    [[ -z "$fp" ]] && continue
    # collect group members
    local grp; grp="$(mktemp)"
    awk -F'\t' -v f="$fp" '$1==f {print $2}' "$THUMB_EXIF_FILE" > "$grp"

    # pick keep = max pixel count (use L1 index to get dimensions)
    local keep="" keep_px=0
    while IFS= read -r p; do
      [[ -z "$p" || ! -e "$p" ]] && continue
      local w h px
      read -r _ w h _ < <(awk -F'\t' -v pp="$p" '$1==pp{print $0; exit}' "$THUMB_INDEX_FILE")
      if [[ -z "$w" ]]; then
        local dims; dims="$(thumb_dimensions "$p")" || continue
        read -r w h <<<"$dims"
      fi
      px=$((w * h))
      if (( px > keep_px )); then keep="$p"; keep_px="$px"; fi
    done < "$grp"
    [[ -z "$keep" ]] && { rm -f "$grp"; continue; }

    # move every non-keep
    while IFS= read -r p; do
      [[ -z "$p" || "$p" == "$keep" || ! -e "$p" ]] && continue
      case "$THUMB_ACTION" in
        list)
          echo "[THUMB-L2] '$p' is thumbnail-of '$keep'"
          THUMB_L2_HITS=$((THUMB_L2_HITS+1))
          ;;
        move|review)
          # Even in review-only mode, L2 evidence is conclusive → move.
          # In dry-run mode, emit a NDJSON event instead of moving.
          if ${DRY_RUN:-false}; then
            local _w="" _h="" _sz=""
            read -r _ _w _h _ < <(awk -F'\t' -v pp="$p" '$1==pp{print $0; exit}' "$THUMB_INDEX_FILE") || true
            [[ -z "$_w" ]] && { local _dims; _dims="$(thumb_dimensions "$p")" && read -r _w _h <<<"$_dims" || true; }
            _sz="$(wc -c < "$p" 2>/dev/null | tr -d ' ')" || _sz=0
            emit_event "thumb_candidate" "decision=thumb_l2_exif" "path=$p" "keeper=$keep" "group_id=$fp" "width=@${_w:-0}" "height=@${_h:-0}" "size_bytes=@${_sz:-0}"
            THUMB_L2_HITS=$((THUMB_L2_HITS+1))
          else
            if qmove "$p" "$THUMB_DIR" "$keep" "" "thumb_l2_exif"; then
              THUMB_L2_HITS=$((THUMB_L2_HITS+1))
            fi
          fi
          ;;
      esac
    done < "$grp"
    rm -f "$grp"
  done < "$dup_fps"
  rm -f "$dup_fps"

  echo "[*] L2 EXIF clusters processed: $THUMB_L2_HITS file(s) flagged"
}

# Compute md5 of a big image's EMBEDDED thumbnail (returns "" if none).
thumb_embed_md5(){
  local f="$1" tmp h
  $THUMB_HAVE_EXIFTOOL || return 1
  tmp="$(mktemp)"
  exiftool -b -ThumbnailImage "$f" >"$tmp" 2>/dev/null || true
  if [[ ! -s "$tmp" ]]; then rm -f "$tmp"; return 1; fi
  h="$(hash_file "$tmp")" || { rm -f "$tmp"; return 1; }
  rm -f "$tmp"
  printf '%s\n' "$h"
}

# L3 pass: for each L1=thumb file that wasn't L2-handled, scan big JPEG/HEIC
# files in the SAME parent directory and compare md5(small) to md5(embedded thumb in big).
# Cheap because the candidate set is bounded by directory size.
thumb_run_l3(){
  THUMB_L3_HITS=0
  $THUMB_HAVE_EXIFTOOL || return 0
  [[ -s "${THUMB_INDEX_FILE:-}" ]] || return 0

  echo "[*] thumbnail-detect L3: comparing embedded thumbnails ..."

  # Iterate L1 suspects only
  while IFS=$'\t' read -r f w h cls; do
    [[ "$cls" == "ok" ]] && continue
    [[ ! -e "$f" ]] && continue                            # may have been moved by L2
    case "${f##*.}" in
      jpg|JPG|jpeg|JPEG|heic|HEIC|heif|HEIF) ;;
      *) continue ;;
    esac

    local small_md5 dir
    small_md5="$(hash_file "$f")" || continue
    dir="$(dirname -- "$f")"

    # candidates = bigger images (l1=ok) in the same dir
    local matched=""
    while IFS=$'\t' read -r cand cw ch ccls; do
      [[ "$ccls" != "ok" ]] && continue
      [[ "$(dirname -- "$cand")" != "$dir" ]] && continue
      [[ "$cand" == "$f" || ! -e "$cand" ]] && continue
      local cm
      cm="$(thumb_embed_md5 "$cand")" || continue
      if [[ "$cm" == "$small_md5" ]]; then matched="$cand"; break; fi
    done < "$THUMB_INDEX_FILE"

    if [[ -n "$matched" ]]; then
      case "$THUMB_ACTION" in
        list)
          echo "[THUMB-L3] '$f' is the embedded thumb of '$matched'"
          THUMB_L3_HITS=$((THUMB_L3_HITS+1))
          ;;
        move|review)
          # In dry-run mode, emit a NDJSON event instead of moving.
          if ${DRY_RUN:-false}; then
            local _w="" _h="" _sz=""
            read -r _ _w _h _ < <(awk -F'\t' -v pp="$f" '$1==pp{print $0; exit}' "$THUMB_INDEX_FILE") || true
            [[ -z "$_w" ]] && { local _dims; _dims="$(thumb_dimensions "$f")" && read -r _w _h <<<"$_dims" || true; }
            _sz="$(wc -c < "$f" 2>/dev/null | tr -d ' ')" || _sz=0
            # group_id for L3 = sha1 of the big (keeper) path
            local _gid
            _gid="$(printf '%s' "$matched" | (shasum 2>/dev/null || sha1sum) | awk '{print $1}')"
            emit_event "thumb_candidate" "decision=thumb_l3_embed" "path=$f" "keeper=$matched" "group_id=l3:$_gid" "width=@${_w:-0}" "height=@${_h:-0}" "size_bytes=@${_sz:-0}"
            THUMB_L3_HITS=$((THUMB_L3_HITS+1))
          else
            if qmove "$f" "$THUMB_DIR" "$matched" "$small_md5" "thumb_l3_embed"; then
              THUMB_L3_HITS=$((THUMB_L3_HITS+1))
            fi
          fi
          ;;
      esac
    fi
  done < "$THUMB_INDEX_FILE"

  echo "[*] L3 embedded-thumbnail matches: $THUMB_L3_HITS"
}

# Build / refresh the persistent pHash index, then expose results via the
# three output files declared below. Pairing logic lives in this same
# function (added in Task 3). Failure to compute pHash (missing python,
# missing imagehash, helper exit nonzero) prints a warning and returns 0
# — thumb_write_review then falls back to today's behavior.
#
# Globals (input):
#   THUMB_PHASH_INDEX     path to <source>/.thumb_phash_index.tsv (override)
#   THUMB_PHASH_HAMMING   match threshold (default 5; used in T3)
#   THUMB_PHASH_ALGO      dhash|phash (default dhash)
#   THUMB_PHASH_ENABLED   bool (default true)
#   THUMB_INDEX_FILE      per-run TSV from thumb_build_l1_index
#   SOURCE_DIR
# Globals (output, temp-file paths set by this function; used by T3):
#   THUMB_PHASH_KEEPER_FILE   TSV: suspect_path TAB keeper_path
#   THUMB_PHASH_GROUP_FILE    TSV: suspect_path TAB group_id
#   THUMB_PHASH_DIST_FILE     TSV: suspect_path TAB hamming_distance
#   THUMB_PHASH_LIVE_INDEX    temp copy of the in-memory index (path TAB mtime TAB size TAB hash)

thumb_run_l1_phash(){
  : "${THUMB_PHASH_ENABLED:=true}"
  : "${THUMB_PHASH_HAMMING:=5}"
  : "${THUMB_PHASH_ALGO:=dhash}"
  : "${THUMB_PHASH_HASH_SIZE:=8}"
  : "${THUMB_PHASH_INDEX:="$SOURCE_DIR/.thumb_phash_index.tsv"}"

  if [[ "$THUMB_PHASH_ENABLED" != "true" ]]; then
    echo "[*] L1 pHash disabled by env" >&2
    return 0
  fi

  local script_dir phash_bin
  script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
  phash_bin="$script_dir/bin/phash.py"
  if [[ ! -x "$phash_bin" ]]; then
    if command -v phash >/dev/null 2>&1; then
      phash_bin="$(command -v phash)"
    else
      echo "[!] L1 pHash skipped: bin/phash.py not found" >&2
      return 0
    fi
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    echo "[!] L1 pHash skipped: python3 not found" >&2
    return 0
  fi

  [[ -s "${THUMB_INDEX_FILE:-}" ]] || return 0

  # --- Step A: validate meta header of existing index ---
  # live_index_file: path TAB mtime TAB size TAB hash (no header line)
  local live_index_file; live_index_file="$(mktemp)"
  local meta_ok=true
  local idx="$THUMB_PHASH_INDEX"
  if [[ -f "$idx" ]]; then
    local meta_line algo hsize ver
    meta_line="$(head -n1 "$idx" 2>/dev/null || echo "")"
    if [[ ! "$meta_line" =~ ^\#\ meta: ]]; then
      meta_ok=false
    else
      algo="$(printf '%s\n' "$meta_line" | sed -n 's/.*algo=\([^ ]*\).*/\1/p')"
      hsize="$(printf '%s\n' "$meta_line" | sed -n 's/.*hash_size=\([0-9]*\).*/\1/p')"
      ver="$(printf '%s\n' "$meta_line" | sed -n 's/.*version=\([0-9]*\).*/\1/p')"
      if [[ "$algo" != "$THUMB_PHASH_ALGO" \
         || "$hsize" != "$THUMB_PHASH_HASH_SIZE" \
         || "$ver" != "1" ]]; then
        meta_ok=false
      fi
    fi
    if $meta_ok; then
      # copy valid data rows into live_index_file (strip header, skip bad rows)
      awk -F'\t' 'NR>1 && $1!="" && $1!~/^#/ && $2~/^[0-9]+$/ && $3~/^[0-9]+$/ && $4~/^[0-9a-f]+$/ {print}' \
        "$idx" > "$live_index_file"
    else
      echo "[*] pHash index rebuild (meta drift)" >&2
      # live_index_file stays empty → everything gets re-hashed
    fi
  fi

  # --- Step B: walk THUMB_INDEX_FILE; determine which files need rehash ---
  local to_hash_file; to_hash_file="$(mktemp)"
  local cache_hits=0 cold=0
  local _f _w _h _cls _live_mt _live_sz _cached_mt _cached_sz _cached_h
  while IFS=$'\t' read -r _f _w _h _cls; do
    [[ -e "$_f" ]] || continue
    _live_mt="$(stat -f '%m' "$_f" 2>/dev/null || stat -c '%Y' "$_f" 2>/dev/null || echo "")"
    _live_sz="$(stat -f '%z' "$_f" 2>/dev/null || stat -c '%s' "$_f" 2>/dev/null || echo "")"
    [[ -z "$_live_mt" || -z "$_live_sz" ]] && continue
    # lookup in live_index_file
    _cached_mt=""; _cached_sz=""; _cached_h=""
    if [[ -s "$live_index_file" ]]; then
      IFS=$'\t' read -r _ _cached_mt _cached_sz _cached_h < <(
        awk -F'\t' -v p="$_f" '$1==p{print; exit}' "$live_index_file"
      ) 2>/dev/null || true
    fi
    if [[ -n "$_cached_h" \
       && "$_cached_mt" == "$_live_mt" \
       && "$_cached_sz" == "$_live_sz" ]]; then
      cache_hits=$((cache_hits+1))
    else
      printf '%s\n' "$_f" >> "$to_hash_file"
      cold=$((cold+1))
    fi
  done < "$THUMB_INDEX_FILE"

  # --- Step C: hash the cold set in one batch ---
  if [[ -s "$to_hash_file" ]]; then
    echo "[*] thumbnail-detect L1-pHash: hashing $cold images …" >&2
    local hash_out; hash_out="$(mktemp)"
    local phash_err; phash_err="$(mktemp)"
    set +e
    python3 "$phash_bin" \
        --algo "$THUMB_PHASH_ALGO" \
        --hash-size "$THUMB_PHASH_HASH_SIZE" \
        < "$to_hash_file" > "$hash_out" 2>"$phash_err"
    local rc=$?
    set -e
    if [[ $rc -ne 0 ]]; then
      if [[ $rc -eq 3 ]]; then
        echo "[!] L1 pHash skipped: install pillow imagehash" >&2
        cat "$phash_err" >&2 || true
      else
        echo "[!] L1 pHash skipped: bin/phash.py exited $rc" >&2
      fi
      rm -f "$phash_err" "$to_hash_file" "$hash_out" "$live_index_file"
      return 0
    fi
    local _errs; _errs="$(wc -l < "$phash_err" 2>/dev/null | tr -d ' ')" || _errs=0
    if [[ "${_errs:-0}" -gt 0 ]]; then
      echo "[*] pHash: $_errs files unreadable, see warnings above" >&2
      cat "$phash_err" >&2 || true
    fi
    rm -f "$phash_err"

    # merge new hashes into live_index_file (append; we'll deduplicate when writing)
    local new_entries_file; new_entries_file="$(mktemp)"
    local _p2 _h2 _mt2 _sz2
    while IFS=$'\t' read -r _p2 _h2; do
      [[ -z "$_p2" || -z "$_h2" ]] && continue
      _mt2="$(stat -f '%m' "$_p2" 2>/dev/null || stat -c '%Y' "$_p2" 2>/dev/null || echo "")"
      _sz2="$(stat -f '%z' "$_p2" 2>/dev/null || stat -c '%s' "$_p2" 2>/dev/null || echo "")"
      [[ -z "$_mt2" || -z "$_sz2" ]] && continue
      printf '%s\t%s\t%s\t%s\n' "$_p2" "$_mt2" "$_sz2" "$_h2" >> "$new_entries_file"
    done < "$hash_out"
    rm -f "$hash_out"

    # rebuild live_index_file: new entries override old ones (last-write-wins by path)
    # Use awk: process new_entries_file first (high priority), then live_index_file
    # keeping first occurrence per path.
    local merged; merged="$(mktemp)"
    awk -F'\t' '!seen[$1]++' "$new_entries_file" "$live_index_file" > "$merged"
    mv -f "$merged" "$live_index_file"
    rm -f "$new_entries_file"
  fi
  rm -f "$to_hash_file"

  # --- Step D: prune entries whose files no longer exist ---
  local pruned; pruned="$(mktemp)"
  while IFS=$'\t' read -r _f _mt _sz _h; do
    [[ -e "$_f" ]] && printf '%s\t%s\t%s\t%s\n' "$_f" "$_mt" "$_sz" "$_h" >> "$pruned"
  done < "$live_index_file"
  mv -f "$pruned" "$live_index_file"

  # --- Step E: write index back (atomic via tempfile + mv) ---
  local idx_tmp="$idx.tmp"
  if ! {
    printf '# meta: algo=%s hash_size=%s version=1 created=%s\n' \
      "$THUMB_PHASH_ALGO" "$THUMB_PHASH_HASH_SIZE" \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    cat "$live_index_file"
  } > "$idx_tmp" 2>/dev/null; then
    echo "[!] cannot write $idx (read-only?); skipping cache" >&2
    rm -f "$idx_tmp"
  else
    mv -f "$idx_tmp" "$idx"
  fi

  echo "[*] thumbnail-detect L1-pHash: cache hits $cache_hits, recomputed $cold (cold or modified)" >&2

  # Expose the live index for T3's pairing pass.
  THUMB_PHASH_LIVE_INDEX="$live_index_file"

  # T3 will append the pairing pass here, populating the THUMB_PHASH_* output files.
  # For now, clean up the live index temp file.
  rm -f "$live_index_file"
  THUMB_PHASH_LIVE_INDEX=""
}

# Anything still L1=suspect (after L2/L3 passes) and still on disk:
# - Under --json-events: emit one thumb_candidate event per suspect (decision=thumb_l1_review);
#   do not write the source-scoped _review.csv (Stage 8.5 Fix 1: Go consumes events, not disk).
# - Legacy CLI (no --json-events): write _review.csv as before.
# We never delete or move L1-only suspects automatically.
thumb_write_review(){
  THUMB_REVIEW_CNT=0
  [[ -s "${THUMB_INDEX_FILE:-}" ]] || return 0

  if $JSON_EVENTS; then
    local f w h cls _sz
    while IFS=$'\t' read -r f w h cls; do
      [[ "$cls" == "ok" ]] && continue
      [[ ! -e "$f" ]] && continue
      _sz="$(wc -c < "$f" 2>/dev/null | tr -d ' ')" || _sz=0
      emit_event "thumb_candidate" \
        "decision=thumb_l1_review" \
        "path=$f" \
        "reason=l1_only_${cls}" \
        "width=@${w:-0}" \
        "height=@${h:-0}" \
        "size_bytes=@${_sz:-0}"
      THUMB_REVIEW_CNT=$((THUMB_REVIEW_CNT+1))
    done < "$THUMB_INDEX_FILE"

    if (( THUMB_REVIEW_CNT > 0 )); then
      echo "[*] L1-only suspects emitted as events: $THUMB_REVIEW_CNT"
    fi
    return 0
  fi

  mkdir -p "$THUMB_DIR" || die3 "cannot create $THUMB_DIR"
  if [[ ! -f "$THUMB_REVIEW_CSV" ]]; then
    printf 'path\treason\twidth\theight\tnote\n' > "$THUMB_REVIEW_CSV"
  fi

  while IFS=$'\t' read -r f w h cls; do
    [[ "$cls" == "ok" ]] && continue
    [[ ! -e "$f" ]] && continue
    local reason="l1_only_${cls}"
    printf '%s\t%s\t%s\t%s\t\n' "$f" "$reason" "$w" "$h" >> "$THUMB_REVIEW_CSV"
    THUMB_REVIEW_CNT=$((THUMB_REVIEW_CNT+1))
  done < "$THUMB_INDEX_FILE"

  if (( THUMB_REVIEW_CNT > 0 )); then
    echo "[*] L1-only suspects pending review: $THUMB_REVIEW_CNT  → $THUMB_REVIEW_CSV"
  fi
}

# Top-level driver — called from main twincut.sh when --thumbnail-detect is set.
thumb_detect_run(){
  thumb_features_report || return 1

  : "${THUMB_MAX_EDGE:=512}"
  : "${THUMB_MAYBE_MAX_EDGE:=1024}"
  : "${THUMB_DIR:="$SOURCE_DIR/_thumbnails"}"
  : "${THUMB_REVIEW_CSV:="$THUMB_DIR/_review.csv"}"
  : "${THUMB_ACTION:=move}"

  mkdir -p "$THUMB_DIR" || die3 "cannot create thumbnail dir: $THUMB_DIR"

  # If thumbnail-detect is the only mode running, point the manifest at THUMB_DIR
  # so the rollback file lives next to the thumbnails (not in ./_QUARANTINE).
  if ! $DO_CROSS && ! $DO_BACKUP_SELF && ! $DO_SOURCE_SELF && ! $MANIFEST_INITED; then
    QUAR_DIR="$THUMB_DIR"
  fi

  THUMB_L1_MARKED=0; THUMB_L2_HITS=0; THUMB_L3_HITS=0
  THUMB_REVIEW_CNT=0; THUMB_SKIPPED_NO_DIM=0

  thumb_build_l1_index
  thumb_build_l2_index    # no-op without exiftool
  thumb_run_l2            # no-op without exiftool
  thumb_run_l3            # no-op without exiftool
  thumb_run_l1_phash      # adds keeper/group_id metadata to L1 suspects
  thumb_write_review

  rm -f "${THUMB_INDEX_FILE:-}" "${THUMB_EXIF_FILE:-}" 2>/dev/null || true
}

thumb_print_summary(){
  echo "----- THUMBNAIL SUMMARY -----"
  echo "L1 suspects:           ${THUMB_L1_MARKED:-0}"
  echo "L2 EXIF cluster hits:  ${THUMB_L2_HITS:-0}"
  echo "L3 embedded-thumb hits: ${THUMB_L3_HITS:-0}"
  echo "L1-only → review:      ${THUMB_REVIEW_CNT:-0}"
  [[ -n "${THUMB_REVIEW_CSV:-}" && -f "$THUMB_REVIEW_CSV" ]] && echo "Review file:           $THUMB_REVIEW_CSV"
  echo "Thumbnail dir:         ${THUMB_DIR:-}"
  echo "-----------------------------"
}

# --thumb-confirm <review.csv>: take rows from a (possibly user-edited) review CSV
# and process each path with qmove. The user is expected to delete rows they don't
# want to act on. We do not enforce ordering.
thumb_confirm_review(){
  local csv="$1"
  [[ -f "$csv" ]] || die "review csv not found: $csv"
  : "${THUMB_DIR:="$(dirname -- "$csv")"}"
  mkdir -p "$THUMB_DIR" || die3 "cannot create $THUMB_DIR"
  # Manifest lives next to the thumbnails (not in ./_QUARANTINE).
  QUAR_DIR="$THUMB_DIR"
  MANIFEST_INITED=false

  local moved=0 skipped=0 missing=0
  echo "[*] confirming review: $csv → $THUMB_DIR"

  # Allowed decision values for the optional 6th column.
  # Any other non-empty value → reject the row with a warning.
  local _allowed_decisions="thumb_l2_exif thumb_l3_embed thumb_confirmed"

  # Skip header row. Parse each TSV row with awk to handle empty fields
  # correctly — bash IFS=$'\t' read collapses consecutive tabs, losing
  # empty fields. awk -F'\t' does not collapse, matching TSV contract.
  local first=true
  while IFS= read -r _raw_line; do
    if $first; then first=false; continue; fi
    # Extract fields with awk to avoid bash IFS tab-collapse.
    local p dec keeper
    p="$(awk -F'\t' '{print $1}' <<< "$_raw_line")"
    dec="$(awk -F'\t' '{print $6}' <<< "$_raw_line")"
    keeper="$(awk -F'\t' '{print $7}' <<< "$_raw_line")"
    keeper="${keeper%$'\r'}"  # defend against CRLF-tainted TSV input
    [[ -z "$p" ]] && continue

    # Trim whitespace (TSV has no quoting).
    dec="${dec// /}"
    # Default to thumb_confirmed when absent (legacy 5-column TSV).
    [[ -z "$dec" ]] && dec="thumb_confirmed"

    # Validate against the allowed set.
    local _valid=false
    local _allowed
    for _allowed in $_allowed_decisions; do
      [[ "$dec" == "$_allowed" ]] && _valid=true && break
    done
    if ! $_valid; then
      echo "[warn] unknown decision value '$dec' for '$p' — skipping row" >&2
      skipped=$((skipped+1))
      continue
    fi

    if [[ ! -e "$p" ]]; then
      echo "[missing] $p"; missing=$((missing+1)); continue
    fi
    if qmove "$p" "$THUMB_DIR" "$keeper" "" "$dec"; then
      moved=$((moved+1))
    else
      skipped=$((skipped+1))
    fi
  done < "$csv"

  echo "===== CONFIRM SUMMARY ====="
  echo "Moved:    $moved"
  echo "Skipped:  $skipped"
  echo "Missing:  $missing"
  echo "==========================="
}
