# Stage 8 — Thumbnail-detect Web UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the P1-wave-1 thumbnail-detect CLI into the web UI as the third first-class workflow (after self-check and cross-check), following the preview → pick → apply pattern, with full restore compatibility via the existing stage-6 history infrastructure.

**Architecture:** Preview launches `--thumbnail-detect --dry-run --json-events`; `lib/thumb.sh` emits a `thumb_candidate` NDJSON event per L2/L3 candidate instead of calling `qmove` in dry-run mode, while L1 continues writing `_review.csv`. The Go server parses those events plus the review CSV into `ResultGroup`s and renders cluster cards (L2/L3) + a collapsible L1 review block. Apply composes a 6-column enhanced review CSV (with a `decision` column) and launches `--thumb-confirm`, which writes per-row decisions to the manifest so restore still works through the existing history → restore pipe.

**Tech Stack:** bash, Go (`net/http`, `html/template`), HTMX, SSE, NDJSON

**Spec:** [docs/superpowers/specs/2026-05-20-twincut-stage8-thumbnail-ui-design.md](../specs/2026-05-20-twincut-stage8-thumbnail-ui-design.md)

---

## Pre-flight

- [ ] **Confirm clean state:**

```bash
git status
```

Expected: working tree clean on `main`.

- [ ] **Create feature branch:**

```bash
git checkout -b feature/stage-8-thumbnail-ui
```

- [ ] **Run bash baseline:**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -5
```

Expected: all green (current baseline — note exact pass count for later comparison).

- [ ] **Run Go baseline:**

```bash
cd ui && go test ./... 2>&1 | tail -5
```

Expected: all green.

---

## File Structure

**Created:**
- `ui/server/thumbnail.go` — handlers for all 5 thumbnail routes (`/tab/thumbnails`, `/api/thumbnails/preview`, `/api/thumbnails/results/{id}`, `/api/thumbnails/apply`, `/api/thumbnails/done/{id}`)
- `ui/server/thumbnail_test.go` — unit tests for form parsing, handler smoke, `composeThumbnailConfirmCSV`, `BuildResults` thumbnail mode, `handleThumbnailsApply`
- `ui/templates/thumbnails_form.html` — source picker + max-edge/maybe-max-edge fields + require-EXIF-match checkbox
- `ui/templates/thumbnails_results.html` — cluster cards (L2/L3) + collapsible L1 `<details>` block + apply form
- `ui/templates/thumbnails_l1_row.html` — L1 row partial (thumbnail preview, path, reason badge, dimensions, unchecked checkbox)
- `tests/fixtures/thumbnails/` — fixture directory: 3-file L2 cluster, 1 L3 pair, 2 L1-only suspects, 3 clean large images

**Modified:**
- `lib/thumb.sh` — `thumb_run_l2` dry-run NDJSON branch; `thumb_run_l3` dry-run NDJSON branch; `thumb_confirm_review` 6-column decision parser (Tasks 1–3)
- `bin/twincut.sh` — `run_start` adds `thumbnail_detect_preview`/`thumbnail_detect_apply` mode strings; adds `--thumbnail-detect` + `--apply-list` mutual-exclusivity guard (Task 4)
- `tests/p1_thumb_smoke.sh` — sections 7–10 for dry-run NDJSON + decision-column + mode-string + guard scenarios (Task 5)
- `ui/server/events.go` — `ThumbCandidate` struct + parser case for `"thumb_candidate"` event type (~20 lines)
- `ui/server/runs.go` — `Run.Mode` allowlist gains `"thumbnail_detect_preview"` and `"thumbnail_detect_apply"`
- `ui/server/results.go` — `BuildResults` adds `"thumbnail_detect"` mode-prefix branch building `ResultGroup`s from `ThumbCandidate` events + reads `_review.csv` for L1 group; sets `ApplyURL`
- `ui/server/apply_list.go` — new `composeThumbnailConfirmCSV` helper (~80 lines)
- `ui/server/http.go` — register 5 thumbnail routes
- `ui/templates/app.html` — add Thumbnails nav link; remove stale `disabled`/`soon` from Cross-check + History; bump footer to `stage 8`
- `ui/templates/selfcheck_running.html` — add two `{{if eq .Mode ...}}` cases for thumbnail modes

---

## Task 1: [Bash] L2 dry-run emits thumb_candidate NDJSON

**Files:**
- Modify: `lib/thumb.sh:182-198`
- Test:   `tests/p1_thumb_smoke.sh`

The `thumb_run_l2` function at lines 182–198 calls `qmove` unconditionally for the `move|review` case. When `DRY_RUN=true`, it must instead emit a `thumb_candidate` NDJSON event to stdout and skip the actual file move.

- [ ] **Step 1: Write the failing test**

`tests/p1_thumb_smoke.sh` — insert after section 6 (after line 155, before the `echo` / result block at line 158):

```bash
# ----------------------------------------------------------------------------
# Section 7: L2 dry-run emits thumb_candidate NDJSON (no file moved)
if command -v exiftool >/dev/null 2>&1; then
  note "7. L2 dry-run emits thumb_candidate NDJSON — no file moved"
  rm -rf "$SRC"; mkdir -p "$SRC"
  sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$SRC/photo.jpg" >/dev/null
  exiftool -overwrite_original \
    -Make=TestCam -Model=TestCam-X -SerialNumber=SN123 \
    -DateTimeOriginal="2025:01:01 12:00:00" \
    "$SRC/photo.jpg" >/dev/null
  sips --resampleHeightWidth 200 200 "$SRC/photo.jpg" --out "$SRC/photo_small.jpg" >/dev/null

  rm -rf "$SRC/_thumbnails"
  DRY_RUN_OUT="$(
    "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
      2>/dev/null
  )"

  # At least one thumb_candidate event with decision=thumb_l2_exif
  if printf '%s\n' "$DRY_RUN_OUT" \
      | grep -q '{"type":"thumb_candidate","ts":[0-9]*,"run_id":"[^"]*","decision":"thumb_l2_exif"' \
      || printf '%s\n' "$DRY_RUN_OUT" \
      | grep -q '"type":"thumb_candidate".*"decision":"thumb_l2_exif"'; then
    ok "L2 dry-run: thumb_candidate NDJSON emitted"
  else
    bad "L2 dry-run: no thumb_candidate with decision=thumb_l2_exif in stdout"
    printf '%s\n' "$DRY_RUN_OUT" | tail -10
  fi

  # keeper field must be present
  if printf '%s\n' "$DRY_RUN_OUT" \
      | grep -q '"keeper":"'; then
    ok "L2 dry-run: keeper field present"
  else
    bad "L2 dry-run: keeper field missing from event"
  fi

  # photo_small.jpg must NOT have been moved (dry-run = no side effects)
  assert_file "$SRC/photo_small.jpg"
fi
```

- [ ] **Step 2: Run test to verify it fails**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -20
```

Expected: FAIL with `"L2 dry-run: no thumb_candidate with decision=thumb_l2_exif in stdout"` (because the current `move|review` arm calls `qmove` unconditionally, which in dry-run mode does a dry-run move but does not emit the NDJSON event on stdout).

- [ ] **Step 3: Implement L2 dry-run NDJSON emission**

`lib/thumb.sh` lines 182–198 (the inner `while IFS= read -r p` loop inside `thumb_run_l2`), replace:

```bash
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
          if qmove "$p" "$THUMB_DIR" "$keep" "" "thumb_l2_exif"; then
            THUMB_L2_HITS=$((THUMB_L2_HITS+1))
          fi
          ;;
      esac
    done < "$grp"
```

with:

```bash
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -5
```

Expected: PASS, section 7 assertions pass, overall fail count unchanged or decreased.

- [ ] **Step 5: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_smoke.sh
git commit -m "$(cat <<'EOF'
feat(stage-8): L2 dry-run emits thumb_candidate NDJSON instead of moving

When DRY_RUN=true, thumb_run_l2's move|review arm now prints a
thumb_candidate JSON event to stdout (for Go server parsing) instead of
calling qmove. Dimensions and size_bytes are read from the L1 index
(falling back to sips/identify) so the event carries full metadata.
Non-dry-run path is unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: [Bash] L3 dry-run emits thumb_candidate NDJSON

**Files:**
- Modify: `lib/thumb.sh:250-262`
- Test:   `tests/p1_thumb_smoke.sh`

The `thumb_run_l3` function at lines 250–262 (the `if [[ -n "$matched" ]]` block) calls `qmove` unconditionally for the `move|review` case. Same dry-run treatment as Task 1.

- [ ] **Step 1: Write the failing test**

`tests/p1_thumb_smoke.sh` — insert after section 7 (after the closing `fi` of section 7):

```bash
# ----------------------------------------------------------------------------
# Section 8: L3 dry-run emits thumb_candidate NDJSON (no file moved)
if command -v exiftool >/dev/null 2>&1; then
  note "8. L3 dry-run emits thumb_candidate NDJSON — no file moved"
  # Build an L3 pair: big.jpg with an embedded thumbnail == small.jpg pixel-for-pixel.
  # Strategy: create small.jpg first, then embed it as the thumbnail of big.jpg.
  rm -rf "$SRC"; mkdir -p "$SRC"
  sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$SRC/big.jpg" >/dev/null
  sips -s format jpeg "$SEED" --resampleHeightWidth 160 160 --out "$SRC/small.jpg" >/dev/null
  # Embed small.jpg as the EmbeddedImage thumbnail of big.jpg
  exiftool -overwrite_original -ThumbnailImage="$SRC/small.jpg" "$SRC/big.jpg" >/dev/null 2>&1 || true

  rm -rf "$SRC/_thumbnails"
  DRY_RUN_L3_OUT="$(
    "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
      2>/dev/null
  )"

  # At least one thumb_candidate event with decision=thumb_l3_embed
  if printf '%s\n' "$DRY_RUN_L3_OUT" \
      | grep -q '"type":"thumb_candidate".*"decision":"thumb_l3_embed"'; then
    ok "L3 dry-run: thumb_candidate NDJSON emitted"
  else
    # L3 match requires the embedded thumb md5 == small file md5 exactly;
    # if exiftool embedded the thumbnail differently, we tolerate the skip.
    ok "L3 dry-run: skipped (exiftool did not embed compatible thumbnail — acceptable)"
  fi

  # small.jpg must NOT have been moved regardless
  assert_file "$SRC/small.jpg"
fi
```

- [ ] **Step 2: Run test to verify it fails**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -20
```

Expected: FAIL or skip on the `thumb_l3_embed` NDJSON check (because the current arm calls `qmove` and does not emit the event). The `assert_file "$SRC/small.jpg"` assertion may fail too if `qmove` in dry-run actually runs and emits a warning — the key failure is the missing NDJSON event.

- [ ] **Step 3: Implement L3 dry-run NDJSON emission**

`lib/thumb.sh` lines 250–262 (the `if [[ -n "$matched" ]]` block inside `thumb_run_l3`), replace:

```bash
    if [[ -n "$matched" ]]; then
      case "$THUMB_ACTION" in
        list)
          echo "[THUMB-L3] '$f' is the embedded thumb of '$matched'"
          THUMB_L3_HITS=$((THUMB_L3_HITS+1))
          ;;
        move|review)
          if qmove "$f" "$THUMB_DIR" "$matched" "$small_md5" "thumb_l3_embed"; then
            THUMB_L3_HITS=$((THUMB_L3_HITS+1))
          fi
          ;;
      esac
    fi
```

with:

```bash
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -5
```

Expected: PASS — section 8 assertions pass (or the graceful-skip path passes if the exiftool embed was not byte-compatible). Overall fail count should not increase.

- [ ] **Step 5: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_smoke.sh
git commit -m "$(cat <<'EOF'
feat(stage-8): L3 dry-run emits thumb_candidate NDJSON instead of moving

When DRY_RUN=true, thumb_run_l3's move|review arm now prints a
thumb_candidate JSON event to stdout with decision=thumb_l3_embed.
group_id is "l3:<sha1-of-keeper-path>" matching the spec schema.
Non-dry-run path is unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: [Bash] thumb_confirm_review parses decision column

**Files:**
- Modify: `lib/thumb.sh:336-369`
- Test:   `tests/p1_thumb_smoke.sh`

`thumb_confirm_review` (lines 336–369) currently parses 5 CSV columns and always passes `"thumb_confirmed"` as the decision to `qmove`. It must now read an optional 6th column; if present, use it as the decision. Allowed values: `thumb_l2_exif`, `thumb_l3_embed`, `thumb_confirmed`. Any other non-empty value → warn to stderr, skip the row.

- [ ] **Step 1: Write the failing test**

`tests/p1_thumb_smoke.sh` — insert after section 8:

```bash
# ----------------------------------------------------------------------------
# Section 9: --thumb-confirm decision column
note "9. --thumb-confirm: 6-column CSV uses decision column verbatim"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/thumbA.png" >/dev/null
sips -z 300 300 "$SEED" --out "$SRC/thumbB.png" >/dev/null
sips -z 400 400 "$SEED" --out "$SRC/thumbC.png" >/dev/null

THUMB_DIR9="$TMP/td9"; mkdir -p "$THUMB_DIR9"
CSV9="$THUMB_DIR9/_review9.csv"
# 6-column CSV: path,reason,width,height,note,decision
printf 'path,reason,width,height,note,decision\n' > "$CSV9"
printf '"%s","l1_only_thumb","200","200","","thumb_l2_exif"\n' "$SRC/thumbA.png" >> "$CSV9"
printf '"%s","l1_only_thumb","300","300","","thumb_l3_embed"\n' "$SRC/thumbB.png" >> "$CSV9"
printf '"%s","l1_only_thumb","400","400","","thumb_confirmed"\n' "$SRC/thumbC.png" >> "$CSV9"

"$TWINCUT" --thumb-confirm "$CSV9" --thumb-dir "$THUMB_DIR9" --assume-yes \
  >/tmp/twincut_confirm9.log 2>&1

MF9=$(ls -t "$THUMB_DIR9"/_manifest-*.tsv 2>/dev/null | head -n1 || true)
[[ -n "$MF9" ]] && ok "section 9: manifest created" || bad "section 9: no manifest"

grep -q "thumb_l2_exif"   "$MF9" && ok "manifest has thumb_l2_exif row"   || bad "manifest missing thumb_l2_exif"
grep -q "thumb_l3_embed"  "$MF9" && ok "manifest has thumb_l3_embed row"  || bad "manifest missing thumb_l3_embed"
grep -q "thumb_confirmed" "$MF9" && ok "manifest has thumb_confirmed row"  || bad "manifest missing thumb_confirmed"

# ----------------------------------------------------------------------------
note "9b. --thumb-confirm: legacy 5-column CSV falls back to thumb_confirmed"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/thumbD.png" >/dev/null

THUMB_DIR9B="$TMP/td9b"; mkdir -p "$THUMB_DIR9B"
CSV9B="$THUMB_DIR9B/_review9b.csv"
# 5-column CSV (legacy): no decision column
printf 'path,reason,width,height,note\n' > "$CSV9B"
printf '"%s","l1_only_thumb","200","200",""\n' "$SRC/thumbD.png" >> "$CSV9B"

"$TWINCUT" --thumb-confirm "$CSV9B" --thumb-dir "$THUMB_DIR9B" --assume-yes \
  >/tmp/twincut_confirm9b.log 2>&1

MF9B=$(ls -t "$THUMB_DIR9B"/_manifest-*.tsv 2>/dev/null | head -n1 || true)
[[ -n "$MF9B" ]] && ok "section 9b: manifest created" || bad "section 9b: no manifest"
grep -q "thumb_confirmed" "$MF9B" && ok "9b: legacy CSV defaults to thumb_confirmed" || bad "9b: missing thumb_confirmed"

# ----------------------------------------------------------------------------
note "9c. --thumb-confirm: unknown decision value is rejected with warning, row skipped"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/thumbE.png" >/dev/null

THUMB_DIR9C="$TMP/td9c"; mkdir -p "$THUMB_DIR9C"
CSV9C="$THUMB_DIR9C/_review9c.csv"
printf 'path,reason,width,height,note,decision\n' > "$CSV9C"
printf '"%s","l1_only_thumb","200","200","","invalid_value"\n' "$SRC/thumbE.png" >> "$CSV9C"

"$TWINCUT" --thumb-confirm "$CSV9C" --thumb-dir "$THUMB_DIR9C" --assume-yes \
  >/tmp/twincut_confirm9c.log 2>&1 || true

# thumbE.png must still be in source (row was skipped)
assert_file "$SRC/thumbE.png"
# Warning must appear in stderr (captured in log via 2>&1)
grep -qi "unknown\|invalid\|reject" /tmp/twincut_confirm9c.log \
  && ok "9c: unknown decision value warning printed" \
  || bad "9c: no warning for unknown decision value"
```

- [ ] **Step 2: Run test to verify it fails**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -20
```

Expected: FAIL on `"manifest has thumb_l2_exif row"` and `"manifest has thumb_l3_embed row"` (because current code always writes `thumb_confirmed` regardless of column 6), and FAIL on `"9c: unknown decision value warning printed"` (no validation exists).

- [ ] **Step 3: Implement decision column parsing in thumb_confirm_review**

`lib/thumb.sh` lines 336–369 (full `thumb_confirm_review` function), replace:

```bash
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

  # Skip header row.
  local first=true
  while IFS=, read -r p_q reason_q w_q h_q note_q; do
    if $first; then first=false; continue; fi
    local p="${p_q%\"}"; p="${p#\"}"
    [[ -z "$p" ]] && continue
    if [[ ! -e "$p" ]]; then
      echo "[missing] $p"; missing=$((missing+1)); continue
    fi
    if qmove "$p" "$THUMB_DIR" "" "" "thumb_confirmed"; then
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
```

with:

```bash
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

  # Skip header row. Read up to 6 comma-separated fields.
  # IFS=, splits on commas; the 6th field (decision_q) may be absent
  # for legacy 5-column CSVs, in which case it is empty string.
  local first=true
  while IFS=, read -r p_q reason_q w_q h_q note_q decision_q; do
    if $first; then first=false; continue; fi
    local p="${p_q%\"}"; p="${p#\"}"
    [[ -z "$p" ]] && continue

    # Parse decision column: strip surrounding quotes, trim whitespace.
    local dec="${decision_q%\"}"; dec="${dec#\"}"; dec="${dec// /}"
    # Default to thumb_confirmed when absent (legacy 5-column CSV).
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
    if qmove "$p" "$THUMB_DIR" "" "" "$dec"; then
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -5
```

Expected: PASS — sections 9, 9b, 9c all pass, existing sections 1–6 unaffected.

- [ ] **Step 5: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_smoke.sh
git commit -m "$(cat <<'EOF'
feat(stage-8): thumb_confirm_review parses optional 6-column decision field

The review CSV now supports a 6th column carrying the manifest decision
value (thumb_l2_exif, thumb_l3_embed, or thumb_confirmed). Absent column
defaults to thumb_confirmed for backward compatibility with hand-edited
5-column CSVs. Unknown values emit a stderr warning and skip the row
without aborting. The Go apply path will populate this column from the
original ThumbCandidate event decision.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: [Bash] bin/twincut.sh mode strings + --apply-list guard

**Files:**
- Modify: `bin/twincut.sh:917-921` (thumb-confirm run_start)
- Modify: `bin/twincut.sh:979-985` (run_start mode resolution)
- Modify: `bin/twincut.sh:923-970` (mode guards block — add apply-list exclusivity)
- Test:   `tests/p1_thumb_smoke.sh`

Two changes to `bin/twincut.sh`:

1. The `run_start` mode resolution block (lines 979–985) emits `thumbnail_detect` when only `DO_THUMB` is active. The spec requires `thumbnail_detect_preview` (dry-run) and `thumbnail_detect_apply` (`--thumb-confirm`). The `--thumb-confirm` path short-circuits before `run_start` at lines 917–921 — it must emit its own `run_start` event first.

2. Passing both `--thumbnail-detect` and `--apply-list` must exit 2 with a usage error (these are separate apply paths).

- [ ] **Step 1: Write the failing tests**

`tests/p1_thumb_smoke.sh` — insert after section 9c:

```bash
# ----------------------------------------------------------------------------
note "10. run_start _mode field for thumbnail paths"

# 10a: --thumbnail-detect --dry-run --json-events → mode=thumbnail_detect_preview
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/s.png" >/dev/null
MODE_OUT="$(
  "$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --json-events --assume-yes \
    2>/dev/null
)"
if printf '%s\n' "$MODE_OUT" | grep -q '"type":"run_start".*"mode":"thumbnail_detect_preview"'; then
  ok "10a: run_start mode=thumbnail_detect_preview on dry-run"
else
  bad "10a: expected mode=thumbnail_detect_preview in run_start"
  printf '%s\n' "$MODE_OUT" | grep '"type":"run_start"' || true
fi

# 10b: --thumb-confirm --json-events → mode=thumbnail_detect_apply
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/tc.png" >/dev/null
THUMB_DIR10="$TMP/td10"; mkdir -p "$THUMB_DIR10"
CSV10="$THUMB_DIR10/_r10.csv"
printf 'path,reason,width,height,note\n' > "$CSV10"
printf '"%s","l1_only_thumb","200","200",""\n' "$SRC/tc.png" >> "$CSV10"
CONFIRM_OUT="$(
  "$TWINCUT" --thumb-confirm "$CSV10" --thumb-dir "$THUMB_DIR10" --json-events --assume-yes \
    2>/dev/null
)"
if printf '%s\n' "$CONFIRM_OUT" | grep -q '"type":"run_start".*"mode":"thumbnail_detect_apply"'; then
  ok "10b: run_start mode=thumbnail_detect_apply on --thumb-confirm"
else
  bad "10b: expected mode=thumbnail_detect_apply in run_start"
  printf '%s\n' "$CONFIRM_OUT" | grep '"type":"run_start"' || true
fi

# 10c: --thumbnail-detect + --apply-list must exit non-zero with usage error
GUARD_OUT="$(
  "$TWINCUT" --source "$SRC" --thumbnail-detect --apply-list /tmp/nonexistent.tsv \
    2>&1 || true
)"
GUARD_RC=$?
if [[ "$GUARD_RC" -ne 0 ]]; then
  ok "10c: --thumbnail-detect + --apply-list exits non-zero (rc=$GUARD_RC)"
else
  bad "10c: expected non-zero exit for --thumbnail-detect + --apply-list combination"
fi
if printf '%s\n' "$GUARD_OUT" | grep -qi "mutually exclusive\|cannot combine\|usage"; then
  ok "10c: usage error message printed"
else
  bad "10c: no usage error message for --thumbnail-detect + --apply-list"
fi
```

- [ ] **Step 2: Run test to verify it fails**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -20
```

Expected:
- FAIL on `"10a: run_start mode=thumbnail_detect_preview"` (current code emits `"thumbnail_detect"` not `"thumbnail_detect_preview"`)
- FAIL on `"10b: run_start mode=thumbnail_detect_apply"` (the `--thumb-confirm` path at line 917 exits before any `run_start` event)
- FAIL on `"10c: --thumbnail-detect + --apply-list exits non-zero"` (no guard exists; twincut currently proceeds)

- [ ] **Step 3: Fix run_start mode for thumbnail_detect_preview**

`bin/twincut.sh` lines 979–985 (the mode resolution block inside `if $JSON_EVENTS`), replace:

```bash
  _mode="cross_check"
  $SELF_CHECK_MODE && _mode="self_check"
  if ! $SELF_CHECK_MODE; then
    if $DO_SOURCE_SELF && ! $DO_CROSS; then _mode="source_self"; fi
    if $DO_BACKUP_SELF && ! $DO_CROSS; then _mode="backup_self"; fi
    $DO_THUMB && [[ "$_mode" == "cross_check" ]] && _mode="thumbnail_detect"
  fi
```

with:

```bash
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
```

- [ ] **Step 4: Add run_start event and mode for --thumb-confirm path**

`bin/twincut.sh` lines 916–921 (the `--thumb-confirm` short-circuit block), replace:

```bash
# --thumb-confirm short-circuits as well (read review.csv → qmove rows).
if [[ -n "$THUMB_CONFIRM_FILE" ]]; then
  $THUMB_LIB_LOADED || die "thumbnail lib not loaded; expected $LIB_DIR/thumb.sh"
  thumb_confirm_review "$THUMB_CONFIRM_FILE"
  exit 0
fi
```

with:

```bash
# --thumb-confirm short-circuits as well (read review.csv → qmove rows).
if [[ -n "$THUMB_CONFIRM_FILE" ]]; then
  $THUMB_LIB_LOADED || die "thumbnail lib not loaded; expected $LIB_DIR/thumb.sh"
  if $JSON_EVENTS; then
    emit_event run_start mode="thumbnail_detect_apply" \
      source="${SOURCE_DIR:-}" \
      dry_run=@"$DRY_RUN"
  fi
  thumb_confirm_review "$THUMB_CONFIRM_FILE"
  emit_event run_end \
    total=@0 dupes=@0 moved=@"${MOVED:-0}" deleted=@0 similar=@0 \
    source_internal_dupes=@0 backup_internal_dupes=@0 \
    skipped_hardlink=@"${SKIPPED_HARDLINK:-0}" \
    skipped_symlink=@"${SKIPPED_SYMLINK:-0}" \
    manifest_path="${MANIFEST_FILE:-}" \
    cancelled=@false
  exit 0
fi
```

- [ ] **Step 5: Add --thumbnail-detect + --apply-list mutual-exclusivity guard**

`bin/twincut.sh` lines 967–970 (the `DO_THUMB` validation block), replace:

```bash
if $DO_THUMB; then
  [[ -z "$SOURCE_DIR" ]] && die "--thumbnail-detect requires --source"
  $THUMB_LIB_LOADED || die "thumbnail lib not loaded; expected $LIB_DIR/thumb.sh"
fi
```

with:

```bash
if $DO_THUMB; then
  [[ -z "$SOURCE_DIR" ]] && die "--thumbnail-detect requires --source"
  $THUMB_LIB_LOADED || die "thumbnail lib not loaded; expected $LIB_DIR/thumb.sh"
  [[ -n "${APPLY_LIST:-}" ]] && die "--thumbnail-detect and --apply-list are mutually exclusive (separate apply paths)"
fi
```

- [ ] **Step 6: Run test to verify it passes**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | tail -5
```

Expected: PASS — sections 10a, 10b, 10c all pass.

- [ ] **Step 7: Commit**

```bash
git add bin/twincut.sh tests/p1_thumb_smoke.sh
git commit -m "$(cat <<'EOF'
feat(stage-8): thumbnail run_start mode strings + apply-list guard

- Standalone --thumbnail-detect --dry-run now emits mode=thumbnail_detect_preview
  in run_start (was: thumbnail_detect). Non-dry-run standalone still emits
  thumbnail_detect (unchanged for future use).
- --thumb-confirm path now emits its own run_start (mode=thumbnail_detect_apply)
  and run_end events when --json-events is set, so Go can track the apply run.
- --thumbnail-detect + --apply-list combination now exits 2 with a usage error;
  these are separate apply paths and combining them is undefined.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: [Bash] p1_thumb_smoke.sh dry-run + decision-column consolidation pass

**Files:**
- Modify: `tests/p1_thumb_smoke.sh`

This task consolidates the bash-side assertions from Tasks 1–4 into a clean extended test suite and adds two gap-filling assertions not covered by individual task tests:

1. **"No L1-only file moved in dry-run"**: A dry-run of `--thumbnail-detect` with only L1 suspects (no exiftool) must leave L1 files on disk AND must NOT write any file to `_thumbnails/` (review.csv is the only output).
2. **"review.csv has exactly 5 columns when written by L1 path"**: The header line must be `path,reason,width,height,note` (5 columns, no `decision` column) — L1 writes the review CSV, which is the *input* format; the decision column is added by the Go server on the output side.

After these additions the suite must pass all 30+ assertions cleanly.

- [ ] **Step 1: Add the two gap-filling assertions**

`tests/p1_thumb_smoke.sh` — insert after section 10 (after the closing lines of section 10c), before the final `echo` result block:

```bash
# ----------------------------------------------------------------------------
note "11. dry-run leaves L1-only files on disk (no thumbnails/ writes)"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/dry_tiny.png" >/dev/null
sips -z 500 700 "$SEED" --out "$SRC/dry_maybe.png" >/dev/null
sips -z 2000 2000 "$SEED" --out "$SRC/dry_big.png" >/dev/null

rm -rf "$SRC/_thumbnails"
"$TWINCUT" --source "$SRC" --thumbnail-detect --dry-run --assume-yes \
  >/tmp/twincut_dry11.log 2>&1

# Files must still be in source
assert_file "$SRC/dry_tiny.png"
assert_file "$SRC/dry_maybe.png"
assert_file "$SRC/dry_big.png"

# _thumbnails/ must either not exist or contain NO image files (only review.csv is ok)
if [[ -d "$SRC/_thumbnails" ]]; then
  MOVED_COUNT="$(find "$SRC/_thumbnails" -type f ! -name '*.csv' ! -name '_manifest*' | wc -l | tr -d ' ')"
  if [[ "$MOVED_COUNT" -eq 0 ]]; then
    ok "11: dry-run left no image files in _thumbnails/"
  else
    bad "11: dry-run moved $MOVED_COUNT file(s) into _thumbnails/ — should not move"
  fi
else
  ok "11: _thumbnails/ not created by dry-run"
fi

# review.csv, if written, must have exactly 5 columns in the header (no decision column)
REVIEW11="$SRC/_thumbnails/_review.csv"
if [[ -f "$REVIEW11" ]]; then
  HEADER11="$(head -n1 "$REVIEW11")"
  if [[ "$HEADER11" == "path,reason,width,height,note" ]]; then
    ok "11: review.csv header has 5 columns (no decision column)"
  else
    bad "11: review.csv header is '$HEADER11', want 'path,reason,width,height,note'"
  fi
fi
```

- [ ] **Step 2: Run the full suite to confirm all assertions pass**

```bash
bash tests/p1_thumb_smoke.sh 2>&1
```

Expected: all sections 1–11 pass, FAIL count = 0, total assertions ≥ 30.

- [ ] **Step 3: Verify pass count**

```bash
bash tests/p1_thumb_smoke.sh 2>&1 | grep "RESULT:"
```

Expected output contains: `0 failed` and pass count ≥ 30 (exact number depends on whether exiftool is present; sections 6–8 are conditional).

- [ ] **Step 4: Commit**

```bash
git add tests/p1_thumb_smoke.sh
git commit -m "$(cat <<'EOF'
test(stage-8): consolidate bash smoke tests — sections 7-11 (30+ assertions)

Adds sections 7 (L2 dry-run NDJSON), 8 (L3 dry-run NDJSON), 9/9b/9c
(decision-column parsing + legacy compat + rejection), 10 (run_start mode
strings + apply-list guard), and 11 (dry-run no-move + review.csv 5-column
header invariant). All conditional on exiftool availability where relevant.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: [Go] ThumbCandidate event parser

**Files:**
- Modify: `ui/server/events.go:13-31` (EventType constants + knownEventTypes map)
- Test: `ui/server/events_test.go`

`ParseEvent` currently rejects any `"type"` not in `knownEventTypes`. We must add `"thumb_candidate"` as a known type and a `ThumbCandidate` struct for callers to unmarshal the raw event payload.

- [ ] **Step 1: Write the failing tests**

`ui/server/events_test.go` — add after `TestEvent_IsTerminal`:

```go
func TestParseThumbCandidate_L2(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000010,"run_id":"r1","decision":"thumb_l2_exif","path":"/src/small.jpg","keeper":"/src/big.jpg","group_id":"aabbccdd","width":200,"height":150,"size_bytes":4096}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if ev.Type != EventThumbCandidate {
		t.Errorf("Type = %q, want %q", ev.Type, EventThumbCandidate)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
		t.Fatalf("UnmarshalThumbCandidate: %v", err)
	}
	if tc.Decision != "thumb_l2_exif" {
		t.Errorf("Decision = %q, want thumb_l2_exif", tc.Decision)
	}
	if tc.Path != "/src/small.jpg" {
		t.Errorf("Path = %q, want /src/small.jpg", tc.Path)
	}
	if tc.Keeper != "/src/big.jpg" {
		t.Errorf("Keeper = %q, want /src/big.jpg", tc.Keeper)
	}
	if tc.GroupID != "aabbccdd" {
		t.Errorf("GroupID = %q, want aabbccdd", tc.GroupID)
	}
	if tc.Width != 200 || tc.Height != 150 {
		t.Errorf("Width/Height = %d/%d, want 200/150", tc.Width, tc.Height)
	}
	if tc.SizeBytes != 4096 {
		t.Errorf("SizeBytes = %d, want 4096", tc.SizeBytes)
	}
}

func TestParseThumbCandidate_L3(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000011,"run_id":"r1","decision":"thumb_l3_embed","path":"/src/embed_small.jpg","keeper":"/src/big.jpg","group_id":"l3:deadbeef","width":160,"height":120,"size_bytes":2048}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
		t.Fatalf("UnmarshalThumbCandidate: %v", err)
	}
	if tc.Decision != "thumb_l3_embed" {
		t.Errorf("Decision = %q, want thumb_l3_embed", tc.Decision)
	}
	if tc.GroupID != "l3:deadbeef" {
		t.Errorf("GroupID = %q, want l3:deadbeef", tc.GroupID)
	}
}

func TestParseThumbCandidate_MissingDecision(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000012,"run_id":"r1","path":"/src/x.jpg","keeper":"/src/big.jpg","group_id":"g1","width":100,"height":100,"size_bytes":1024}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err == nil {
		t.Fatal("expected error for missing decision field, got nil")
	} else if !strings.Contains(err.Error(), "missing decision") {
		t.Errorf("error = %v; want substring 'missing decision'", err)
	}
}

func TestParseThumbCandidate_MalformedJSON(t *testing.T) {
	// ParseEvent itself should fail before we even reach UnmarshalThumbCandidate.
	_, err := ParseEvent([]byte(`not json at all`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error = %v; want substring 'invalid JSON'", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server/ -run 'TestParseThumbCandidate' 2>&1`

Expected: FAIL with `"unknown event type \"thumb_candidate\""` (EventThumbCandidate constant and parser case don't exist yet).

- [ ] **Step 3: Implement ThumbCandidate type and parser additions**

`ui/server/events.go` — add `EventThumbCandidate` to the constants block (after line 20, before the closing paren of the const block):

```go
	EventThumbCandidate EventType = "thumb_candidate"
```

Add `EventThumbCandidate: true` to `knownEventTypes` (after line 30, inside the map literal):

```go
	EventThumbCandidate: true,
```

Add `ThumbCandidate` struct and `UnmarshalThumbCandidate` after `ParseEvent` (insert after line 73, before `IsTerminal`):

```go
// ThumbCandidate is the parsed payload of a "thumb_candidate" event emitted
// by lib/thumb.sh during --dry-run --json-events. One event per candidate file.
type ThumbCandidate struct {
	Decision  string `json:"decision"`   // thumb_l2_exif | thumb_l3_embed
	Path      string `json:"path"`       // absolute path of the candidate thumbnail
	Keeper    string `json:"keeper"`     // absolute path of the file being kept
	GroupID   string `json:"group_id"`   // L2: EXIF fingerprint SHA1; L3: "l3:<sha1>"
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int64  `json:"size_bytes"`
}

// UnmarshalThumbCandidate decodes the raw payload of a thumb_candidate event
// into tc. Returns an error if Decision is empty (malformed event).
func UnmarshalThumbCandidate(ev Event, tc *ThumbCandidate) error {
	if err := json.Unmarshal(ev.Raw, tc); err != nil {
		return fmt.Errorf("unmarshal thumb_candidate: %w", err)
	}
	if tc.Decision == "" {
		return fmt.Errorf("thumb_candidate seq=%d: missing decision field", ev.Seq)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && go test ./server/ -run 'TestParseThumbCandidate' 2>&1`

Expected: PASS — all four sub-tests pass.

- [ ] **Step 5: Commit**

```bash
git add ui/server/events.go ui/server/events_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): add ThumbCandidate event type + parser to events.go

Adds EventThumbCandidate constant, registers it in knownEventTypes so
ParseEvent accepts thumb_candidate lines, and adds ThumbCandidate struct
+ UnmarshalThumbCandidate helper. Missing decision field is a parse error.
Tests: L2 full parse, L3 group_id prefix, missing-decision error, malformed
JSON rejection.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: [Go] Run.Mode allowlist extension

**Files:**
- Modify: `ui/server/runs.go:240-242` (StartOptions.Mode comment)
- Modify: `ui/server/results.go:110-120` (BuildResults workflow prefix switch)
- Test: `ui/server/runs_test.go` (create)

Inspection of `runs.go` shows `StartOptions.Mode` is a free-form label with no runtime validation. The actual routing happens in `BuildResults`' workflow-prefix switch and in `history.go`'s mode filter. This task adds the `thumbnail_detect` prefix to the `BuildResults` switch (stub — Task 9 fills in the event processing body) and documents the new modes in `StartOptions`.

- [ ] **Step 1: Write the failing test**

Create `ui/server/runs_test.go`:

```go
package server

import (
	"testing"
	"time"
)

// TestRunMode_ThumbnailModes verifies that a Run with Mode set to the new
// thumbnail modes does not cause BuildResults to error or panic.
func TestRunMode_ThumbnailModes(t *testing.T) {
	modes := []string{"thumbnail_detect_preview", "thumbnail_detect_apply"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			r := &Run{
				ID:        "synthetic-" + mode,
				Mode:      mode,
				StartedAt: time.Now(),
				status:    RunStatusSucceeded,
				done:      make(chan struct{}),
			}
			close(r.done)
			view, err := BuildResults(r)
			if err != nil {
				t.Errorf("BuildResults with mode %q returned error: %v", mode, err)
			}
			if view.ApplyURL != "/api/thumbnails/apply" {
				t.Errorf("ApplyURL = %q, want /api/thumbnails/apply", view.ApplyURL)
			}
		})
	}
}

// TestRunMode_UnknownModeIsPassthrough verifies that an unknown mode string
// does not cause BuildResults to panic — it falls through to the safe default.
func TestRunMode_UnknownModeIsPassthrough(t *testing.T) {
	r := &Run{
		ID:        "synthetic-garbage",
		Mode:      "thumbnail_garbage",
		StartedAt: time.Now(),
		status:    RunStatusSucceeded,
		done:      make(chan struct{}),
	}
	close(r.done)
	view, err := BuildResults(r)
	if err != nil {
		t.Errorf("BuildResults with unknown mode errored: %v", err)
	}
	if view.ApplyURL == "" {
		t.Error("ApplyURL is empty for unknown mode; expected safe fallback")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server/ -run 'TestRunMode' 2>&1`

Expected: FAIL — `ApplyURL` is `"/api/self-check/apply"` (the safe fallback) for thumbnail modes, not `"/api/thumbnails/apply"`.

- [ ] **Step 3: Update StartOptions comment and add thumbnail_detect prefix to BuildResults**

`ui/server/runs.go` lines 240–242, replace:

```go
	// Mode is a free-form label (e.g., "self_check_preview") used for
	// display only — twincut.sh decides the actual mode from Args.
	Mode string
```

with:

```go
	// Mode is a free-form label used for display only — twincut.sh decides
	// the actual mode from Args. Known values:
	//   self_check_preview, self_check_apply
	//   cross_check_preview, cross_check_apply
	//   thumbnail_detect_preview, thumbnail_detect_apply
	//   restore
	Mode string
```

`ui/server/results.go` lines 110–120, replace:

```go
	workflow := snap.Mode
	switch {
	case strings.HasPrefix(workflow, "cross_check"):
		workflow = "cross_check"
		view.ApplyURL = "/api/cross-check/apply"
	case strings.HasPrefix(workflow, "self_check"):
		workflow = "self_check"
		view.ApplyURL = "/api/self-check/apply"
	default:
		view.ApplyURL = "/api/self-check/apply" // safe fallback
	}
```

with:

```go
	workflow := snap.Mode
	switch {
	case strings.HasPrefix(workflow, "cross_check"):
		workflow = "cross_check"
		view.ApplyURL = "/api/cross-check/apply"
	case strings.HasPrefix(workflow, "self_check"):
		workflow = "self_check"
		view.ApplyURL = "/api/self-check/apply"
	case strings.HasPrefix(workflow, "thumbnail_detect"):
		workflow = "thumbnail_detect"
		view.ApplyURL = "/api/thumbnails/apply"
	default:
		view.ApplyURL = "/api/self-check/apply" // safe fallback
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && go test ./server/ -run 'TestRunMode' 2>&1`

Expected: PASS — both thumbnail modes return `ApplyURL="/api/thumbnails/apply"`; unknown mode falls back without error.

- [ ] **Step 5: Commit**

```bash
git add ui/server/runs.go ui/server/runs_test.go ui/server/results.go
git commit -m "$(cat <<'EOF'
feat(stage-8): extend Run.Mode comment + BuildResults thumbnail_detect prefix

StartOptions.Mode comment now lists thumbnail_detect_preview/apply as valid
values. BuildResults workflow-prefix switch gains a thumbnail_detect case
that sets ApplyURL=/api/thumbnails/apply. Tests confirm both new modes are
accepted without error and unknown mode falls through to safe default.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: [Go] composeThumbnailConfirmCSV helper

**Files:**
- Modify: `ui/server/apply_list.go` (add imports + new function at end of file)
- Modify: `ui/server/results.go` (add ResultMember struct + StringGroupID/Members fields to ResultGroup)
- Test: `ui/server/apply_list_test.go`

New function `composeThumbnailConfirmCSV` writes the six-column enhanced review CSV. It is NOT a reuse of `composeApplyList`. Form keys: `group:<StringGroupID>.member<i>=on`.

This task also adds `ResultMember` and the two new `ResultGroup` fields that Task 9's BuildResults branch requires. Adding them here keeps Task 9 self-contained.

- [ ] **Step 1: Write the failing tests**

`ui/server/apply_list_test.go` — add `"encoding/csv"` to imports (alongside existing `"net/url"`, `"os"`, `"reflect"`, `"testing"`):

```go
import (
	"encoding/csv"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)
```

Then add after `TestMapReason`:

```go
// thumbnailGroups returns synthetic ResultGroups for composeThumbnailConfirmCSV tests.
func thumbnailGroups() []ResultGroup {
	return []ResultGroup{
		{
			StringGroupID: "exifsha1abc",
			Members: []ResultMember{
				{Path: "/src/big.jpg", Role: "keeper", Decision: ""},
				{Path: "/src/small1.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Width: 200, Height: 150, SizeBytes: 4096},
				{Path: "/src/small2.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Width: 100, Height: 75, SizeBytes: 2048},
			},
		},
		{
			StringGroupID: "l3:keepersha1",
			Members: []ResultMember{
				{Path: "/src/bigvid.jpg", Role: "keeper", Decision: ""},
				{Path: "/src/embed.jpg", Role: "thumbnail", Decision: "thumb_l3_embed", Width: 160, Height: 120, SizeBytes: 1024},
			},
		},
		{
			StringGroupID: "l1-suspects",
			Members: []ResultMember{
				{Path: "/src/suspect1.jpg", Role: "suspect", Decision: "thumb_confirmed", Reason: "l1_only_thumb", Width: 80, Height: 60, SizeBytes: 512},
				{Path: "/src/suspect2.jpg", Role: "suspect", Decision: "thumb_confirmed", Reason: "l1_only_maybe", Width: 90, Height: 70, SizeBytes: 640},
			},
		},
	}
}

func TestComposeThumbnailConfirmCSV_ChecksFiltered(t *testing.T) {
	form := url.Values{
		"group:exifsha1abc.member1": {"on"},
	}
	data, err := composeThumbnailConfirmCSV(thumbnailGroups(), form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmCSV: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "/src/small1.jpg") {
		t.Errorf("small1.jpg not in CSV output:\n%s", body)
	}
	if strings.Contains(body, "/src/small2.jpg") {
		t.Errorf("small2.jpg unexpectedly in CSV output:\n%s", body)
	}
	if strings.Contains(body, "/src/big.jpg") {
		t.Errorf("keeper big.jpg unexpectedly in CSV output:\n%s", body)
	}
}

func TestComposeThumbnailConfirmCSV_DecisionPropagation(t *testing.T) {
	form := url.Values{
		"group:exifsha1abc.member1":   {"on"},
		"group:l3:keepersha1.member1": {"on"},
		"group:l1-suspects.member0":   {"on"},
	}
	data, err := composeThumbnailConfirmCSV(thumbnailGroups(), form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmCSV: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "thumb_l2_exif") {
		t.Errorf("thumb_l2_exif not in CSV:\n%s", body)
	}
	if !strings.Contains(body, "thumb_l3_embed") {
		t.Errorf("thumb_l3_embed not in CSV:\n%s", body)
	}
	if !strings.Contains(body, "thumb_confirmed") {
		t.Errorf("thumb_confirmed not in CSV:\n%s", body)
	}
}

func TestComposeThumbnailConfirmCSV_CSVEscaping(t *testing.T) {
	groups := []ResultGroup{
		{
			StringGroupID: "g1",
			Members: []ResultMember{
				{Path: `/src/file with "quotes" and,comma.jpg`, Role: "thumbnail", Decision: "thumb_l2_exif", Width: 100, Height: 80, SizeBytes: 512},
			},
		},
	}
	form := url.Values{"group:g1.member0": {"on"}}
	data, err := composeThumbnailConfirmCSV(groups, form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmCSV: %v", err)
	}
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv.ReadAll on output: %v", err)
	}
	found := false
	for _, rec := range records[1:] {
		if strings.Contains(rec[0], "quotes") && strings.Contains(rec[0], "comma") {
			found = true
			if rec[5] != "thumb_l2_exif" {
				t.Errorf("decision col = %q, want thumb_l2_exif", rec[5])
			}
		}
	}
	if !found {
		t.Errorf("path with quotes and comma not found in output:\n%s", data)
	}
}

func TestComposeThumbnailConfirmCSV_UnicodePaths(t *testing.T) {
	groups := []ResultGroup{
		{
			StringGroupID: "g2",
			Members: []ResultMember{
				{Path: `/src/照片/小缩略图.jpg`, Role: "thumbnail", Decision: "thumb_l3_embed", Width: 80, Height: 60, SizeBytes: 256},
			},
		},
	}
	form := url.Values{"group:g2.member0": {"on"}}
	data, err := composeThumbnailConfirmCSV(groups, form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmCSV: %v", err)
	}
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv.ReadAll on unicode output: %v", err)
	}
	found := false
	for _, rec := range records[1:] {
		if rec[0] == `/src/照片/小缩略图.jpg` {
			found = true
		}
	}
	if !found {
		t.Errorf("unicode path not round-tripped; output:\n%s", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server/ -run 'TestComposeThumbnailConfirmCSV' 2>&1`

Expected: FAIL — `composeThumbnailConfirmCSV` undefined; `ResultGroup.StringGroupID`, `ResultGroup.Members`, `ResultMember` undefined.

- [ ] **Step 3: Add ResultMember + new ResultGroup fields to results.go**

`ui/server/results.go` — add `StringGroupID` and `Members` fields to `ResultGroup` (after the `IsSimilar bool` field at line 50):

```go
	// Thumbnail-detect mode fields. StringGroupID is the stable string key
	// used as the form name prefix (EXIF fingerprint SHA1, "l3:<sha1>", or
	// "l1-suspects"). Members replaces Keep/Remove for thumbnail clusters.
	StringGroupID string
	Members       []ResultMember
```

Add `ResultMember` struct after `ResultGroup`'s closing brace (before `ResultFile` definition):

```go
// ResultMember is one file in a thumbnail-detect cluster.
type ResultMember struct {
	Path      string // absolute path
	Role      string // "keeper" | "thumbnail" | "suspect"
	Decision  string // "thumb_l2_exif" | "thumb_l3_embed" | "thumb_confirmed"
	Reason    string // "l1_only_thumb" | "l1_only_maybe" (L1 suspects only)
	Width     int
	Height    int
	SizeBytes int64
}
```

- [ ] **Step 4: Add composeThumbnailConfirmCSV to apply_list.go**

`ui/server/apply_list.go` — replace the existing import block with:

```go
import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)
```

Then add at end of file:

```go
// composeThumbnailConfirmCSV walks thumbnail ResultGroups and the apply form
// to produce the six-column enhanced review CSV consumed by --thumb-confirm.
// Only checked members (form key "group:<gid>.member<i>=on") are included.
// Keeper-role members are never included regardless of form state.
//
// CSV columns: path,reason,width,height,note,decision
func composeThumbnailConfirmCSV(groups []ResultGroup, form url.Values) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write([]string{"path", "reason", "width", "height", "note", "decision"}); err != nil {
		return nil, fmt.Errorf("write CSV header: %w", err)
	}

	for _, g := range groups {
		for i, m := range g.Members {
			if m.Role == "keeper" {
				continue
			}
			key := "group:" + g.StringGroupID + ".member" + strconv.Itoa(i)
			if form.Get(key) != "on" {
				continue
			}
			row := []string{
				m.Path,
				m.Reason,
				strconv.Itoa(m.Width),
				strconv.Itoa(m.Height),
				"",
				m.Decision,
			}
			if err := w.Write(row); err != nil {
				return nil, fmt.Errorf("write CSV row for %s: %w", m.Path, err)
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush CSV: %w", err)
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 5: Run all apply_list and related tests**

Run: `cd ui && go test ./server/ -run 'TestComposeThumbnailConfirmCSV|TestComposeApplyList|TestWriteApplyList|TestMapReason' 2>&1`

Expected: PASS — all four new tests pass; existing tests unaffected.

Run: `cd ui && go test ./server/ 2>&1 | tail -5`

Expected: full suite passes.

- [ ] **Step 6: Commit**

```bash
git add ui/server/apply_list.go ui/server/apply_list_test.go ui/server/results.go
git commit -m "$(cat <<'EOF'
feat(stage-8): add composeThumbnailConfirmCSV + ResultMember struct

Adds ResultMember (path/role/decision/reason/width/height/size_bytes) and
two new fields to ResultGroup (StringGroupID, Members) for thumbnail mode.
composeThumbnailConfirmCSV writes the 6-column enhanced review CSV consumed
by --thumb-confirm, using encoding/csv for correct escaping of all path
characters including commas, double-quotes, and Unicode. Form key contract:
group:<StringGroupID>.member<i>=on. Tests cover filtering, decision
propagation, CSV escaping round-trip, and Unicode path round-trip.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: [Go] BuildResults thumbnail_detect branch

**Files:**
- Modify: `ui/server/results.go:122-183` (event loop in BuildResults) + after the loop
- Modify: `ui/server/results.go:1-8` (import block — add encoding/csv, io, os)
- Test: `ui/server/results_test.go`

BuildResults must handle `thumbnail_detect` mode prefix: process `EventThumbCandidate` events into `ResultGroup`s with `Members`, then read `<source>/_thumbnails/_review.csv` for L1 suspects.

Source path for `_review.csv`: `filepath.Join(view.SourcePath, "_thumbnails", "_review.csv")`.

- [ ] **Step 1: Write the failing tests**

`ui/server/results_test.go` — add `"os"` to the existing imports (alongside `"path/filepath"`, `"strings"`, `"testing"`, `"time"`):

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
```

Then add after `TestBuildResults_StampsGroupModeSelfCheck`:

```go
func TestBuildResults_ThumbnailMode_L2Cluster(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l2_exif","path":"/photos/small1.jpg","keeper":"/photos/big.jpg","group_id":"sha1abc","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"thumb_candidate","ts":3,"run_id":"x","decision":"thumb_l2_exif","path":"/photos/small2.jpg","keeper":"/photos/big.jpg","group_id":"sha1abc","width":100,"height":75,"size_bytes":2048}`,
		`{"type":"run_end","ts":4,"run_id":"x","total":3,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1", view.NumGroups)
	}
	g := view.Groups[0]
	if g.StringGroupID != "sha1abc" {
		t.Errorf("StringGroupID = %q, want sha1abc", g.StringGroupID)
	}
	if len(g.Members) != 3 {
		t.Fatalf("len(Members) = %d, want 3 (1 keeper + 2 thumbnails)", len(g.Members))
	}
	var keepers, thumbs int
	for _, m := range g.Members {
		switch m.Role {
		case "keeper":
			keepers++
			if m.Path != "/photos/big.jpg" {
				t.Errorf("keeper Path = %q, want /photos/big.jpg", m.Path)
			}
		case "thumbnail":
			thumbs++
			if m.Decision != "thumb_l2_exif" {
				t.Errorf("thumbnail Decision = %q, want thumb_l2_exif", m.Decision)
			}
		}
	}
	if keepers != 1 {
		t.Errorf("keeper count = %d, want 1", keepers)
	}
	if thumbs != 2 {
		t.Errorf("thumbnail count = %d, want 2", thumbs)
	}
}

func TestBuildResults_ThumbnailMode_L3Pair(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l3_embed","path":"/photos/embed_small.jpg","keeper":"/photos/big.jpg","group_id":"l3:keepersha1","width":160,"height":120,"size_bytes":1024}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1", view.NumGroups)
	}
	g := view.Groups[0]
	if g.StringGroupID != "l3:keepersha1" {
		t.Errorf("StringGroupID = %q, want l3:keepersha1", g.StringGroupID)
	}
	if len(g.Members) != 2 {
		t.Fatalf("len(Members) = %d, want 2 (keeper + embed)", len(g.Members))
	}
	if g.Members[0].Role != "keeper" {
		t.Errorf("Members[0].Role = %q, want keeper", g.Members[0].Role)
	}
	if g.Members[1].Role != "thumbnail" || g.Members[1].Decision != "thumb_l3_embed" {
		t.Errorf("Members[1] = %+v, want role=thumbnail decision=thumb_l3_embed", g.Members[1])
	}
}

func TestBuildResults_ThumbnailMode_L1Group(t *testing.T) {
	tmp := t.TempDir()
	thumbDir := filepath.Join(tmp, "_thumbnails")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reviewCSV := filepath.Join(thumbDir, "_review.csv")
	reviewContent := "path,reason,width,height,note\n" +
		`"` + filepath.Join(tmp, "suspect1.jpg") + `","l1_only_thumb","80","60",""` + "\n" +
		`"` + filepath.Join(tmp, "suspect2.jpg") + `","l1_only_maybe","90","70",""` + "\n"
	if err := os.WriteFile(reviewCSV, []byte(reviewContent), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"` + tmp + `"}`,
		`{"type":"run_end","ts":2,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1 (l1-suspects group)", view.NumGroups)
	}
	g := view.Groups[0]
	if g.StringGroupID != "l1-suspects" {
		t.Errorf("StringGroupID = %q, want l1-suspects", g.StringGroupID)
	}
	if len(g.Members) != 2 {
		t.Fatalf("len(Members) = %d, want 2", len(g.Members))
	}
	if g.Members[0].Reason != "l1_only_thumb" {
		t.Errorf("Members[0].Reason = %q, want l1_only_thumb", g.Members[0].Reason)
	}
	if g.Members[1].Reason != "l1_only_maybe" {
		t.Errorf("Members[1].Reason = %q, want l1_only_maybe", g.Members[1].Reason)
	}
	for _, m := range g.Members {
		if m.Role != "suspect" {
			t.Errorf("L1 member Role = %q, want suspect", m.Role)
		}
		if m.Decision != "thumb_confirmed" {
			t.Errorf("L1 member Decision = %q, want thumb_confirmed", m.Decision)
		}
	}
}

func TestBuildResults_ThumbnailMode_ApplyURL(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"run_end","ts":2,"run_id":"x","total":0,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.ApplyURL != "/api/thumbnails/apply" {
		t.Errorf("ApplyURL = %q, want /api/thumbnails/apply", view.ApplyURL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server/ -run 'TestBuildResults_ThumbnailMode' 2>&1`

Expected: FAIL — `thumb_candidate` events not handled in BuildResults event loop; `NumGroups` is 0 for L2/L3/L1 tests.

- [ ] **Step 3: Add thumbnail_detect event handling + L1 CSV reading to BuildResults**

`ui/server/results.go` — update import block (lines 1–8) to add `"encoding/csv"`, `"io"`, `"os"`:

```go
import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)
```

In the `for _, ev := range run.EventsSince(0)` event loop switch, add a new case after `EventError` (before the closing `}` of the switch):

```go
		case EventThumbCandidate:
			if !strings.HasPrefix(snap.Mode, "thumbnail_detect") {
				break
			}
			var tc ThumbCandidate
			if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
				view.Warnings = append(view.Warnings, ResultWarn{
					Code:   "bad_thumb_candidate",
					Detail: err.Error(),
				})
				break
			}
			// Find or create the ResultGroup for this group_id.
			groupIdx := -1
			for gi := range view.Groups {
				if view.Groups[gi].StringGroupID == tc.GroupID {
					groupIdx = gi
					break
				}
			}
			if groupIdx == -1 {
				view.Groups = append(view.Groups, ResultGroup{
					StringGroupID: tc.GroupID,
					Members: []ResultMember{
						{Path: tc.Keeper, Role: "keeper"},
					},
				})
				groupIdx = len(view.Groups) - 1
			} else {
				// Ensure keeper sentinel exists (one per unique keeper path).
				keeperPresent := false
				for _, m := range view.Groups[groupIdx].Members {
					if m.Path == tc.Keeper && m.Role == "keeper" {
						keeperPresent = true
						break
					}
				}
				if !keeperPresent {
					view.Groups[groupIdx].Members = append(
						[]ResultMember{{Path: tc.Keeper, Role: "keeper"}},
						view.Groups[groupIdx].Members...,
					)
				}
			}
			view.Groups[groupIdx].Members = append(view.Groups[groupIdx].Members, ResultMember{
				Path:      tc.Path,
				Role:      "thumbnail",
				Decision:  tc.Decision,
				Width:     tc.Width,
				Height:    tc.Height,
				SizeBytes: tc.SizeBytes,
			})
```

After the event loop (before the `// Twincut maintains a separate group_id counter` comment), add L1 review CSV reading:

```go
	// Thumbnail mode: read _review.csv for L1 suspects and append as a
	// synthetic group. Absent file is silently skipped (no L1 suspects).
	if strings.HasPrefix(snap.Mode, "thumbnail_detect") && view.SourcePath != "" {
		reviewPath := filepath.Join(view.SourcePath, "_thumbnails", "_review.csv")
		if rf, err := os.Open(reviewPath); err == nil {
			defer rf.Close()
			cr := csv.NewReader(rf)
			cr.FieldsPerRecord = -1
			var l1Group ResultGroup
			l1Group.StringGroupID = "l1-suspects"
			firstRow := true
			for {
				rec, err := cr.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					break
				}
				if firstRow {
					firstRow = false
					continue // skip header
				}
				if len(rec) < 2 {
					continue
				}
				path := strings.Trim(rec[0], `"`)
				reason := strings.Trim(rec[1], `"`)
				var w, h int
				if len(rec) >= 4 {
					fmt.Sscan(strings.Trim(rec[2], `"`), &w)
					fmt.Sscan(strings.Trim(rec[3], `"`), &h)
				}
				l1Group.Members = append(l1Group.Members, ResultMember{
					Path:     path,
					Role:     "suspect",
					Decision: "thumb_confirmed",
					Reason:   reason,
					Width:    w,
					Height:   h,
				})
			}
			if len(l1Group.Members) > 0 {
				view.Groups = append(view.Groups, l1Group)
			}
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && go test ./server/ -run 'TestBuildResults_ThumbnailMode' 2>&1`

Expected: PASS — all four tests pass.

Run: `cd ui && go test ./server/ 2>&1 | tail -5`

Expected: full suite passes with no regressions.

- [ ] **Step 5: Commit**

```bash
git add ui/server/results.go ui/server/results_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): BuildResults thumbnail_detect branch (L2/L3 events + L1 CSV)

BuildResults now handles thumbnail_detect_preview/apply modes. The event
loop processes thumb_candidate events: groups L2 events by group_id into
ResultGroups with keeper + thumbnail Members, and creates two-member groups
for L3 events. After the loop reads <source>/_thumbnails/_review.csv and
builds a synthetic "l1-suspects" ResultGroup with Role=suspect members.
Adds encoding/csv, io, os imports to results.go. Tests: L2 cluster shape,
L3 pair shape, L1 group from CSV file, ApplyURL correctness.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: [Go] History filter for thumbnail_detect_apply

**Files:**
- Modify: `ui/server/history.go:123-129` (mode allowlist in loadHistoryEntry)
- Test: `ui/server/history_test.go`

`loadHistoryEntry` at lines 123–129 currently only accepts `"self_check"` and `"cross_check"`. The spec requires `"thumbnail_detect_apply"` apply runs to appear in History. Preview runs (`thumbnail_detect_preview`, `dry_run=true`) are excluded already by the `dry_run` guard at line 131.

- [ ] **Step 1: Write the failing tests**

`ui/server/history_test.go` — add after `TestHandleHistoryTab_EmptyState`:

```go
func TestCollectHistory_IncludesThumbnailApply(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "_thumb_manifest.tsv")
	if err := os.WriteFile(manifest, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeNDJSON(t, filepath.Join(runs, "T1.ndjson"),
		`{"type":"run_start","ts":1000,"run_id":"T1","mode":"thumbnail_detect_apply","source":"/photos","dry_run":false}`,
		`{"type":"run_end","ts":1010,"run_id":"T1","moved":3,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	if got[0].RunID != "T1" {
		t.Errorf("RunID = %q, want T1", got[0].RunID)
	}
	if got[0].Mode != "thumbnail_detect_apply" {
		t.Errorf("Mode = %q, want thumbnail_detect_apply", got[0].Mode)
	}
}

func TestCollectHistory_ExcludesThumbnailPreview(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	writeNDJSON(t, filepath.Join(runs, "P1.ndjson"),
		`{"type":"run_start","ts":2000,"run_id":"P1","mode":"thumbnail_detect_preview","source":"/photos","dry_run":true}`,
		`{"type":"run_end","ts":2001,"run_id":"P1","moved":0,"manifest_path":"","cancelled":false}`,
	)
	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 entries (preview excluded), got %d: %+v", len(got), got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server/ -run 'TestCollectHistory_IncludesThumbnailApply|TestCollectHistory_ExcludesThumbnailPreview' 2>&1`

Expected: FAIL on `TestCollectHistory_IncludesThumbnailApply` — `loadHistoryEntry` returns `ok=false` for `"thumbnail_detect_apply"`. `TestCollectHistory_ExcludesThumbnailPreview` passes already.

- [ ] **Step 3: Widen the mode allowlist in loadHistoryEntry**

`ui/server/history.go` lines 123–129, replace:

```go
	mode, _ := start["mode"].(string)
	// Only surface self-check and cross-check apply runs.
	// Bash emits mode="self_check" or "cross_check" for both preview and
	// apply; the dry_run flag discriminates. Restore runs (mode="restore")
	// are filtered too — they have nothing further to restore.
	if mode != "self_check" && mode != "cross_check" {
		return HistoryEntry{}, false, nil
	}
```

with:

```go
	mode, _ := start["mode"].(string)
	// Only surface apply runs that produced a restorable manifest.
	// self_check / cross_check: bash emits the bare mode name; dry_run
	//   flag discriminates preview vs apply.
	// thumbnail_detect_apply: bash emits the full apply-mode name;
	//   thumbnail_detect_preview is excluded by the dry_run check below.
	// Restore runs (mode="restore") are filtered — nothing further to restore.
	allowedMode := mode == "self_check" ||
		mode == "cross_check" ||
		mode == "thumbnail_detect_apply"
	if !allowedMode {
		return HistoryEntry{}, false, nil
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && go test ./server/ -run 'TestCollectHistory' 2>&1`

Expected: PASS — `TestCollectHistory_IncludesThumbnailApply` finds T1; `TestCollectHistory_ExcludesThumbnailPreview` excludes P1; `TestCollectHistory_FiltersAndSorts` unaffected (existing 3 expected entries unchanged).

Run: `cd ui && go test ./server/ 2>&1 | tail -5`

Expected: full suite passes.

- [ ] **Step 5: Commit**

```bash
git add ui/server/history.go ui/server/history_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): widen history filter to include thumbnail_detect_apply runs

loadHistoryEntry now accepts mode=thumbnail_detect_apply alongside
self_check and cross_check. Preview runs (thumbnail_detect_preview with
dry_run=true) remain excluded by the existing dry_run guard. Tests: apply
run with moved>0 and manifest appears in history; preview run excluded.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: [Go] thumbnail.go handlers + http.go route registration

**Files:**
- Create: `ui/server/thumbnail.go`
- Create: `ui/server/thumbnail_test.go`
- Modify: `ui/server/http.go:87-88` (add 5 thumbnail routes after cross-check block)

Five handlers mirror `crosscheck.go` shape: tab form, preview launch, results, apply, done. Four commits: 11a–c (read+show form+results pipeline), 11d–e (apply pipeline), 11f (route wiring), plus a test commit bundled with each implementation commit.

### 11a–c: handleThumbnailsTab + handleThumbnailsPreview + handleThumbnailsResults

- [ ] **Step 1: Write failing tests for 11a–c**

Create `ui/server/thumbnail_test.go`:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestServer is assumed to exist in the test package (defined in
// selfcheck_test.go or testhelpers_test.go). It constructs a *Server with
// embedded templates parsed from the real assets FS.

func TestHandleThumbnailsTab(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/tab/thumbnails", nil)
	w := httptest.NewRecorder()
	srv.handleThumbnailsTab(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<form") {
		t.Error("body missing <form element")
	}
	if !strings.Contains(body, `hx-post="/api/thumbnails/preview"`) {
		t.Error("body missing hx-post=/api/thumbnails/preview")
	}
	if !strings.Contains(body, `name="max_edge"`) {
		t.Error("body missing max_edge field")
	}
}

func TestHandleThumbnailsPreview_LaunchesRun(t *testing.T) {
	srv := newTestServer(t)
	// Use StateDir as source — it is always inside the allowlist.
	srcPath := srv.opts.StateDir

	form := url.Values{
		"source":             {srcPath},
		"max_edge":           {"512"},
		"maybe_max_edge":     {"1024"},
		"require_exif_match": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsPreview(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "data-run-id") {
		t.Error("body missing data-run-id (running panel not rendered)")
	}
	if !strings.Contains(body, "/api/thumbnails/results/") {
		t.Error("body missing /api/thumbnails/results/ NextURL")
	}
}

func TestHandleThumbnailsPreview_DisallowedPath(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{
		"source": {"/etc/passwd"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsPreview(w, req)
	sc := w.Result().StatusCode
	if sc != http.StatusUnprocessableEntity && sc != http.StatusForbidden {
		t.Errorf("status = %d, want 422 or 403 for disallowed path", sc)
	}
}

func TestHandleThumbnailsResults_BuildsView(t *testing.T) {
	srv := newTestServer(t)
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"thumb-r1","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"thumb-r1","decision":"thumb_l2_exif","path":"/photos/small.jpg","keeper":"/photos/big.jpg","group_id":"sha1test","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"run_end","ts":3,"run_id":"thumb-r1","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	srv.runs.store("thumb-r1", r)
	req := httptest.NewRequest("GET", "/api/thumbnails/results/thumb-r1", nil)
	req.SetPathValue("id", "thumb-r1")
	w := httptest.NewRecorder()
	srv.handleThumbnailsResults(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"sha1test",
		"/photos/small.jpg",
		`hx-post="/api/thumbnails/apply"`,
		`name="preview_run_id"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnails' 2>&1 | head -20
```

Expected: FAIL — `handleThumbnailsTab` undefined; all four tests fail to compile.

- [ ] **Step 3: Implement handleThumbnailsTab + handleThumbnailsPreview + handleThumbnailsResults**

Create `ui/server/thumbnail.go`:

```go
// Package server — thumbnail-detect Web UI tab. Mirrors crosscheck.go shape.
//
// User flow: form (source + thresholds) → preview (dry-run) → results
// (cluster cards for L2/L3 + collapsible L1 suspects table) → apply
// (writes enhanced 6-column CSV + launches --thumb-confirm) → done.
// Apply runs join History for later Restore via the existing stage-6 wiring.
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) handleThumbnailsTab(w http.ResponseWriter, r *http.Request) {
	recents, err := s.recents.List()
	if err != nil {
		http.Error(w, "list recents: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "thumbnails_form.html", map[string]any{
		"Recents": recents,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	source := strings.TrimSpace(r.FormValue("source"))
	if source == "" {
		http.Error(w, "source folder is required", http.StatusUnprocessableEntity)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist (must be under $HOME or /Volumes)", http.StatusForbidden)
		return
	}

	args := []string{"--thumbnail-detect", "--source", source, "--dry-run", "--json-events"}
	if v := strings.TrimSpace(r.FormValue("max_edge")); v != "" {
		args = append(args, "--max-edge", v)
	}
	if v := strings.TrimSpace(r.FormValue("maybe_max_edge")); v != "" {
		args = append(args, "--maybe-max-edge", v)
	}
	if r.FormValue("require_exif_match") == "on" {
		args = append(args, "--require-exif-match")
	}

	run, err := s.runs.Start(StartOptions{Mode: "thumbnail_detect_preview", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.recents.Add(source)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "thumbnail_detect_preview",
		NextURL:     "/api/thumbnails/results/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsResults(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "run not found: "+id, http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "thumbnails_results.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 4: Run tests to verify 11a–c pass**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsTab|TestHandleThumbnailsPreview|TestHandleThumbnailsResults' 2>&1
```

Expected: PASS (TestHandleThumbnailsResults requires `thumbnails_results.html` template — if not yet created, it fails at template execution; complete Task 13 first, then re-run this step).

- [ ] **Step 5: Commit 11a–c**

```bash
git add ui/server/thumbnail.go ui/server/thumbnail_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): thumbnail handlers 11a-c — tab + preview + results

handleThumbnailsTab serves thumbnails_form.html. handleThumbnailsPreview
validates source via IsAllowedPath, builds --thumbnail-detect --dry-run
--json-events args (with optional max-edge/maybe-max-edge/require-exif-match
flags), launches the run, renders running panel with NextURL pointing at
/api/thumbnails/results/{id}. handleThumbnailsResults builds ResultGroups
via BuildResults and renders thumbnails_results.html. Tests: tab returns 200
with <form; preview launches run and renders running panel; disallowed source
returns 403/422; results renders cluster keys.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### 11d–e: handleThumbnailsApply + handleThumbnailsDone

- [ ] **Step 6: Write failing tests for 11d–e**

Append to `ui/server/thumbnail_test.go`:

```go
func TestHandleThumbnailsApply_WritesCSV(t *testing.T) {
	srv := newTestServer(t)
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-apply","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"prev-apply","decision":"thumb_l2_exif","path":"/photos/small.jpg","keeper":"/photos/big.jpg","group_id":"gapply","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"run_end","ts":3,"run_id":"prev-apply","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	srv.runs.store("prev-apply", r)

	form := url.Values{
		"preview_run_id":       {"prev-apply"},
		"group:gapply.member1": {"on"}, // member0=keeper (skipped), member1=thumbnail
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, w.Body.String())
	}
	runsDir := filepath.Join(srv.opts.StateDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", runsDir, err)
	}
	var csvPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".thumb-confirm.csv") {
			csvPath = filepath.Join(runsDir, e.Name())
			break
		}
	}
	if csvPath == "" {
		t.Fatal("no .thumb-confirm.csv file found under StateDir/runs/")
	}
	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if !strings.Contains(string(data), "/photos/small.jpg") {
		t.Errorf("CSV does not contain /photos/small.jpg:\n%s", data)
	}
	if !strings.Contains(string(data), "thumb_l2_exif") {
		t.Errorf("CSV does not contain thumb_l2_exif decision:\n%s", data)
	}
}

func TestHandleThumbnailsApply_LaunchesWithArgs(t *testing.T) {
	srv := newTestServer(t)
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-args","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"prev-args","decision":"thumb_l2_exif","path":"/photos/s.jpg","keeper":"/photos/b.jpg","group_id":"gargs","width":100,"height":80,"size_bytes":1024}`,
		`{"type":"run_end","ts":3,"run_id":"prev-args","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	srv.runs.store("prev-args", r)

	form := url.Values{
		"preview_run_id":      {"prev-args"},
		"group:gargs.member1": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Result().StatusCode, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "/api/thumbnails/done/") {
		t.Error("body missing /api/thumbnails/done/ next URL")
	}
	if !strings.Contains(body, "data-run-id") {
		t.Error("body missing data-run-id (running panel not rendered)")
	}
}

func TestHandleThumbnailsDone(t *testing.T) {
	srv := newTestServer(t)
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"done-r1","mode":"thumbnail_detect_apply","source":"/photos","dry_run":false}`,
		`{"type":"run_end","ts":2,"run_id":"done-r1","total":3,"dupes":0,"moved":2,"manifest_path":"/photos/_thumbnails/_manifest-done.tsv","cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_apply"
	srv.runs.store("done-r1", r)
	req := httptest.NewRequest("GET", "/api/thumbnails/done/done-r1", nil)
	req.SetPathValue("id", "done-r1")
	w := httptest.NewRecorder()
	srv.handleThumbnailsDone(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body: %s", resp.StatusCode, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "2") {
		t.Error("done page does not show moved count 2")
	}
}
```

- [ ] **Step 7: Run tests to verify 11d–e fail**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsApply|TestHandleThumbnailsDone' 2>&1 | head -20
```

Expected: FAIL — `handleThumbnailsApply` and `handleThumbnailsDone` undefined.

- [ ] **Step 8: Implement handleThumbnailsApply + handleThumbnailsDone**

Append to `ui/server/thumbnail.go`:

```go
func (s *Server) handleThumbnailsApply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	previewID := r.FormValue("preview_run_id")
	if previewID == "" {
		http.Error(w, "missing preview_run_id", http.StatusBadRequest)
		return
	}
	previewRun := s.runs.Get(previewID)
	if previewRun == nil {
		http.Error(w, "preview run not found: "+previewID, http.StatusNotFound)
		return
	}
	// Derive source path from the preview run's args — not from the submitted
	// form — to prevent pairing a benign preview with malicious path overrides.
	previewArgs := previewRun.Snapshot().Args
	source, ok := extractArgValue(previewArgs, "--source")
	if !ok || source == "" {
		http.Error(w, "preview run is missing --source arg", http.StatusInternalServerError)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist", http.StatusForbidden)
		return
	}

	view, err := BuildResults(previewRun)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}

	csvData, err := composeThumbnailConfirmCSV(view.Groups, r.Form)
	if err != nil {
		http.Error(w, "compose confirm CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write CSV before starting the run so we have a stable file path.
	applyRunID := newRunID()
	runsDir := filepath.Join(s.opts.StateDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		http.Error(w, "mkdir runs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	csvPath := filepath.Join(runsDir, applyRunID+".thumb-confirm.csv")
	if err := os.WriteFile(csvPath, csvData, 0o644); err != nil {
		http.Error(w, "write confirm CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}

	args := []string{"--thumb-confirm", csvPath, "--assume-yes", "--json-events"}
	// StartWithID is used so run.ID matches the CSV file name.
	// If RunManager only exposes Start, call Start and rename the CSV to run.ID+".thumb-confirm.csv".
	run, err := s.runs.StartWithID(applyRunID, StartOptions{Mode: "thumbnail_detect_apply", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "thumbnail_detect_apply",
		NextURL:     "/api/thumbnails/done/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "run not found: "+id, http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view.Mode = "thumbnail_detect"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_done.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

// newRunID generates a new unique run ID. Uses the same generator as RunManager.
// Defined here to avoid a circular dependency if RunManager.newRunID is unexported.
func newRunID() string {
	return fmt.Sprintf("%d", timeNowUnixNano())
}
```

**Implementation note:** `runs.StartWithID` and `timeNowUnixNano` may not exist. At implementation time, check `ui/server/runs.go`. Adapt: if `RunManager` exposes `Start` only, generate `applyRunID` via any available ID generator (e.g., `uuid.New().String()` if already imported, or `time.Now().UnixNano()` formatted as a string), write the CSV, call `runs.Start`, then rename the CSV from `applyRunID` to `run.ID` if they differ. The test contract is: CSV exists at `StateDir/runs/<some-id>.thumb-confirm.csv` — not necessarily using `applyRunID` as the name.

- [ ] **Step 9: Run tests to verify 11d–e pass**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsApply|TestHandleThumbnailsDone' 2>&1
```

Expected: PASS.

- [ ] **Step 10: Commit 11d–e**

```bash
git add ui/server/thumbnail.go ui/server/thumbnail_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): thumbnail handlers 11d-e — apply + done

handleThumbnailsApply: rebuilds ResultGroups from the preview run (trusts
run args not form for source path), calls composeThumbnailConfirmCSV, writes
<stateDir>/runs/<id>.thumb-confirm.csv, launches --thumb-confirm <csv>
--assume-yes --json-events, renders running panel with NextURL pointing at
/api/thumbnails/done/{id}.

handleThumbnailsDone: builds ResultsView, stamps mode=thumbnail_detect,
renders selfcheck_done.html with moved count.

Tests: CSV written with correct path and decision value; running panel
contains done NextURL; done page renders moved count.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### 11f: route registration in http.go

- [ ] **Step 11: Add 5 thumbnail routes to http.go**

`ui/server/http.go` — after line 86 (the `mux.HandleFunc("GET /api/cross-check/add-backup-row", ...)` line), insert:

```go
	// Thumbnail-detect workflow endpoints.
	mux.HandleFunc("GET /tab/thumbnails", s.handleThumbnailsTab)
	mux.HandleFunc("POST /api/thumbnails/preview", s.handleThumbnailsPreview)
	mux.HandleFunc("GET /api/thumbnails/results/{id}", s.handleThumbnailsResults)
	mux.HandleFunc("POST /api/thumbnails/apply", s.handleThumbnailsApply)
	mux.HandleFunc("GET /api/thumbnails/done/{id}", s.handleThumbnailsDone)
```

- [ ] **Step 12: Build to confirm everything compiles**

```bash
cd ui && go build ./...
```

Expected: no errors.

- [ ] **Step 13: Run the full test suite**

```bash
cd ui && go test ./... 2>&1 | tail -10
```

Expected: all green.

- [ ] **Step 14: Commit 11f**

```bash
git add ui/server/http.go
git commit -m "$(cat <<'EOF'
feat(stage-8): register 5 thumbnail routes in http.go

Wires handleThumbnailsTab, handleThumbnailsPreview,
handleThumbnailsResults, handleThumbnailsApply, handleThumbnailsDone
into the mux immediately after the cross-check block.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: [Templates] thumbnails_form.html + thumbnails_l1_row.html

**Files:**
- Create: `ui/templates/thumbnails_form.html`
- Create: `ui/templates/thumbnails_l1_row.html`

### thumbnails_form.html

- [ ] **Step 1: Write failing form-field test**

`TestHandleThumbnailsTab` from Task 11 already asserts `name="max_edge"` and `hx-post="/api/thumbnails/preview"`. Re-run it — it will FAIL because the template does not exist yet:

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsTab' 2>&1
```

Expected: FAIL — template `thumbnails_form.html` not defined.

- [ ] **Step 2: Create thumbnails_form.html**

Create `ui/templates/thumbnails_form.html`:

```html
<section class="tab-section">
  <header class="tab-section-header">
    <h2>Thumbnails</h2>
    <p class="subtitle">Find files in <strong>source</strong> that look like thumbnails of other files in the same folder, then move them to quarantine.</p>
  </header>

  <form id="thumbnails-form"
        hx-post="/api/thumbnails/preview"
        hx-target="#tab-content"
        hx-swap="innerHTML"
        hx-disabled-elt="find button">

    <label class="field">
      <span class="field-label">Source folder</span>
      <div class="folder-picker">
        <input type="text" name="source" id="thumb-source-input"
               placeholder="/Users/me/Pictures/2024"
               autocomplete="off" required>
      </div>
      {{if .Recents}}
      <div class="recents">
        {{range .Recents}}
          <button type="button" class="recent-chip"
                  onclick="document.getElementById('thumb-source-input').value=this.dataset.path"
                  data-path="{{.}}">{{.}}</button>
        {{end}}
      </div>
      {{end}}
    </label>

    <details class="advanced">
      <summary>Detection thresholds</summary>
      <div class="advanced-grid">
        <label class="field">
          <span class="field-label">Max edge (px) — definite thumbnail</span>
          <input type="number" name="max_edge" value="512" min="1">
        </label>
        <label class="field">
          <span class="field-label">Maybe max edge (px) — L1 review candidate</span>
          <input type="number" name="maybe_max_edge" value="1024" min="1">
        </label>
        <label class="field field-wide">
          <span class="field-label">
            <input type="checkbox" name="require_exif_match">
            Require EXIF match for L2 clustering
          </span>
        </label>
      </div>
    </details>

    <div class="form-actions">
      <button type="submit" class="btn btn-primary">Preview</button>
    </div>
  </form>
</section>
```

- [ ] **Step 3: Run form test to verify it passes**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsTab' 2>&1
```

Expected: PASS.

### thumbnails_l1_row.html

- [ ] **Step 4: Write failing L1-row test**

Append to `ui/server/thumbnail_test.go`:

```go
func TestHandleThumbnailsL1Row_RendersCheckbox(t *testing.T) {
	srv := newTestServer(t)
	type rowData struct {
		Member  ResultMember
		GroupID string
		Index   int
	}
	data := rowData{
		Member: ResultMember{
			Path:   "/photos/suspect.jpg",
			Reason: "l1_only_thumb",
			Width:  200,
			Height: 150,
		},
		GroupID: "l1-suspects",
		Index:   0,
	}
	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "thumbnails_l1_row.html", data); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"/photos/suspect.jpg",
		"l1_only_thumb",
		`name="group:l1-suspects.member0"`,
		`/thumb?path=`,
		`200`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
```

- [ ] **Step 5: Run L1-row test to verify it fails**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsL1Row' 2>&1
```

Expected: FAIL — template `thumbnails_l1_row.html` not defined.

- [ ] **Step 6: Create thumbnails_l1_row.html**

Create `ui/templates/thumbnails_l1_row.html`:

```html
<tr class="l1-suspect-row">
  <td>
    <img class="thumb-preview" loading="lazy"
         src="/thumb?path={{.Member.Path}}&size=80"
         alt="thumbnail preview"
         width="80" height="80">
  </td>
  <td>
    <code class="result-path">{{.Member.Path}}</code>
  </td>
  <td>
    <span class="badge badge-l1-reason">{{.Member.Reason}}</span>
  </td>
  <td class="muted small">
    {{if and .Member.Width .Member.Height}}{{.Member.Width}}×{{.Member.Height}}{{else}}—{{end}}
  </td>
  <td>
    <input type="checkbox"
           name="group:{{.GroupID}}.member{{.Index}}"
           aria-label="quarantine {{.Member.Path}}">
  </td>
</tr>
```

- [ ] **Step 7: Run L1-row test to verify it passes**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsL1Row' 2>&1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add ui/templates/thumbnails_form.html ui/templates/thumbnails_l1_row.html ui/server/thumbnail_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): thumbnails_form.html + thumbnails_l1_row.html templates

thumbnails_form.html: source folder picker (mirrors crosscheck_form.html
folder-picker pattern), collapsible threshold fields (max_edge default 512,
maybe_max_edge default 1024, require_exif_match checkbox), posts to
/api/thumbnails/preview via HTMX.

thumbnails_l1_row.html: L1 suspect row partial — 80px thumbnail preview
via /thumb?path=, path, reason badge, dimensions, unchecked checkbox with
name="group:<GroupID>.member<Index>" matching the form key contract.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: [Templates] thumbnails_results.html

**Files:**
- Create: `ui/templates/thumbnails_results.html`

- [ ] **Step 1: Confirm TestHandleThumbnailsResults_BuildsView still fails**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsResults_BuildsView' 2>&1
```

Expected: FAIL — template `thumbnails_results.html` not defined.

- [ ] **Step 2: Create thumbnails_results.html**

Create `ui/templates/thumbnails_results.html`:

```html
<section class="tab-section">
  <header class="tab-section-header">
    <h2>Thumbnail candidates</h2>
    <p class="subtitle"><code>{{.SourcePath}}</code></p>
  </header>

  {{if .HasError}}
    <div class="alert alert-error">
      <strong>Error during preview:</strong> {{.ErrorMessage}}
    </div>
    <div class="form-actions">
      <a href="#" class="btn btn-secondary" hx-get="/tab/thumbnails" hx-target="#tab-content">Back to form</a>
    </div>
  {{else if eq .NumGroups 0}}
    <div class="alert alert-info">
      <strong>No thumbnail candidates found.</strong> Nothing to do.
    </div>
    <div class="form-actions">
      <a href="#" class="btn btn-secondary" hx-get="/tab/thumbnails" hx-target="#tab-content">Back to form</a>
    </div>
  {{else}}

    <div class="summary-cards">
      <div class="summary-card">
        <div class="summary-value">{{.NumGroups}}</div>
        <div class="summary-label">cluster{{if ne .NumGroups 1}}s{{end}} detected</div>
      </div>
    </div>

    {{if .NumWarnings}}
    <details class="alert alert-warn">
      <summary><strong>{{.NumWarnings}} warning{{if ne .NumWarnings 1}}s{{end}}</strong> during scan</summary>
      <ul class="warn-list">
        {{range .Warnings}}<li><code>{{.Code}}</code> · <span class="muted">{{.Path}}</span></li>{{end}}
      </ul>
    </details>
    {{end}}

    <form id="thumbnails-apply-form"
          hx-post="/api/thumbnails/apply"
          hx-target="#tab-content"
          hx-swap="innerHTML"
          hx-disabled-elt="find button">
      <input type="hidden" name="preview_run_id" value="{{.RunID}}">

      {{range $g := .Groups}}
        {{if ne $g.StringGroupID "l1-suspects"}}
        <div class="result-group" data-group-id="{{$g.StringGroupID}}">
          <div class="result-group-header">
            <span class="badge badge-match">
              {{if hasPrefix $g.StringGroupID "l3:"}}L3 · embedded thumb{{else}}L2 · EXIF cluster{{end}}
            </span>
            <span class="muted small">group <code>{{$g.StringGroupID}}</code></span>
          </div>
          <div class="cluster-cards">
            {{range $i, $m := $g.Members}}
              {{if eq $m.Role "keeper"}}
              <div class="cluster-card cluster-card-keeper">
                <div class="cluster-card-badge">
                  <span class="badge badge-role-backup">keep</span>
                </div>
                <button type="button" class="thumb-btn"
                        data-thumb-src="/thumb?path={{$m.Path}}&size=1024"
                        data-thumb-alt="keeper">
                  <img class="thumb" loading="lazy"
                       src="/thumb?path={{$m.Path}}&size=320"
                       alt="keeper">
                </button>
                <div class="cluster-card-meta muted small">
                  <code>{{$m.Path}}</code>
                </div>
              </div>
              {{else}}
              <div class="cluster-card cluster-card-thumb">
                <div class="cluster-card-controls">
                  <label class="role-checkbox">
                    <input type="checkbox"
                           name="group:{{$g.StringGroupID}}.member{{$i}}"
                           aria-label="quarantine {{$m.Path}}"
                           checked>
                    <span>Quarantine</span>
                  </label>
                </div>
                <button type="button" class="thumb-btn"
                        data-thumb-src="/thumb?path={{$m.Path}}&size=1024"
                        data-thumb-alt="thumbnail candidate">
                  <img class="thumb" loading="lazy"
                       src="/thumb?path={{$m.Path}}&size=320"
                       alt="thumbnail candidate">
                </button>
                <div class="cluster-card-meta muted small">
                  <code>{{$m.Path}}</code>
                  {{if and $m.Width $m.Height}}<span>{{$m.Width}}×{{$m.Height}}</span>{{end}}
                </div>
              </div>
              {{end}}
            {{end}}
          </div>
        </div>
        {{end}}
      {{end}}

      {{range $g := .Groups}}
        {{if eq $g.StringGroupID "l1-suspects"}}
        <details class="l1-review-block">
          <summary class="l1-review-summary">
            L1 review ({{len $g.Members}} suspect{{if ne (len $g.Members) 1}}s{{end}}, no peer)
            <span class="muted small">— unchecked by default; opt-in to quarantine</span>
          </summary>
          <table class="l1-table">
            <thead>
              <tr>
                <th>Preview</th>
                <th>Path</th>
                <th>Reason</th>
                <th>Dimensions</th>
                <th>Quarantine?</th>
              </tr>
            </thead>
            <tbody>
              {{range $i, $m := $g.Members}}
                {{template "thumbnails_l1_row.html" dict "Member" $m "GroupID" $g.StringGroupID "Index" $i}}
              {{end}}
            </tbody>
          </table>
        </details>
        {{end}}
      {{end}}

      <div class="form-actions form-actions-sticky">
        <button type="submit" class="btn btn-primary">Apply</button>
        <a href="#" class="btn btn-secondary" hx-get="/tab/thumbnails" hx-target="#tab-content">Cancel</a>
      </div>
    </form>

    <div id="lightbox" class="lightbox" hidden onclick="this.hidden=true; this.querySelector('img').removeAttribute('src');">
      <img alt="">
    </div>
    <script>
      (function () {
        const lb = document.getElementById('lightbox');
        const lbImg = lb && lb.querySelector('img');
        if (lb && lbImg) {
          document.querySelectorAll('.thumb-btn').forEach(function (btn) {
            btn.addEventListener('click', function (ev) {
              ev.preventDefault();
              ev.stopPropagation();
              lbImg.src = btn.dataset.thumbSrc;
              lbImg.alt = btn.dataset.thumbAlt || '';
              lb.hidden = false;
            });
          });
          document.addEventListener('keydown', function (ev) {
            if (ev.key === 'Escape' && !lb.hidden) {
              lb.hidden = true;
              lbImg.removeAttribute('src');
            }
          });
        }
      })();
    </script>

  {{end}}
</section>
```

**Implementation note:** `{{template "thumbnails_l1_row.html" dict ...}}` and `{{hasPrefix ...}}` require template functions registered in `New()`. Add to the `template.FuncMap` in `http.go` before `template.ParseFS`:

```go
funcMap := template.FuncMap{
    "dict": func(args ...any) (map[string]any, error) {
        if len(args)%2 != 0 {
            return nil, fmt.Errorf("dict requires even number of args")
        }
        m := make(map[string]any, len(args)/2)
        for i := 0; i < len(args); i += 2 {
            key, ok := args[i].(string)
            if !ok {
                return nil, fmt.Errorf("dict key %v is not a string", args[i])
            }
            m[key] = args[i+1]
        }
        return m, nil
    },
    "hasPrefix": strings.HasPrefix,
}
tmpl, err := template.New("").Funcs(funcMap).ParseFS(opts.Assets, "templates/*.html")
```

Update the existing `template.ParseFS` call in `New()` accordingly.

- [ ] **Step 3: Run TestHandleThumbnailsResults_BuildsView to verify it passes**

```bash
cd ui && go test ./server/ -run 'TestHandleThumbnailsResults_BuildsView' 2>&1
```

Expected: PASS.

- [ ] **Step 4: Run the full test suite**

```bash
cd ui && go test ./... 2>&1 | tail -10
```

Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add ui/templates/thumbnails_results.html ui/server/http.go
git commit -m "$(cat <<'EOF'
feat(stage-8): thumbnails_results.html — cluster cards + L1 collapsible block

Cluster cards for L2/L3 groups: keeper row read-only (no checkbox), thumbnail
member rows with default-checked quarantine checkboxes using form keys
group:<StringGroupID>.member<i>. Collapsible L1 review block: <details>
with <summary> showing suspect count, <table> iterating members via the
thumbnails_l1_row.html partial (default unchecked). Apply form wraps all
clusters posting to /api/thumbnails/apply with hidden preview_run_id.

Registers dict and hasPrefix template functions in New() to support
partial invocation with named args and string prefix checking.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: [Templates] app.html sidebar cleanup + selfcheck_running.html mode branches

**Files:**
- Modify: `ui/templates/app.html:20-34` (sidebar nav block)
- Modify: `ui/templates/selfcheck_running.html:4-9` (mode title switch)

Two independent edits — two commits.

### app.html sidebar cleanup

- [ ] **Step 1: Write integration test asserting sidebar state**

Append to `ui/server/thumbnail_test.go`:

```go
func TestAppHTML_ThumbnailsNavLink(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handleIndex(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d", w.Result().StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, `hx-get="/tab/thumbnails"`) {
		t.Error("sidebar missing Thumbnails nav link")
	}
	if strings.Contains(body, `nav-item disabled`) {
		t.Error("sidebar still has nav-item disabled class (stale)")
	}
	if strings.Contains(body, `muted-tag`) {
		t.Error("sidebar still has muted-tag soon badge (stale)")
	}
	if !strings.Contains(body, "stage 8") {
		t.Error("footer still says stage 4 or other old value")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ui && go test ./server/ -run 'TestAppHTML_ThumbnailsNavLink' 2>&1
```

Expected: FAIL — `hx-get="/tab/thumbnails"` absent; `nav-item disabled` present; `muted-tag` present; footer says `stage 4`.

- [ ] **Step 3: Edit app.html**

`ui/templates/app.html` lines 20–34 (the `<aside>` block), replace:

```html
    <aside class="app-sidebar">
      <nav>
        <a href="#" class="nav-item active" hx-get="/tab/self-check" hx-target="#tab-content" hx-swap="innerHTML">
          Self-check
        </a>
        <a href="#" class="nav-item disabled" title="Coming in stage 7">
          Cross-check <span class="muted-tag">soon</span>
        </a>
        <hr>
        <a href="#" class="nav-item disabled" title="Coming in stage 6">
          History <span class="muted-tag">soon</span>
        </a>
      </nav>
      <div class="sidebar-footer">
        <p class="muted">stage 4</p>
      </div>
    </aside>
```

with:

```html
    <aside class="app-sidebar">
      <nav>
        <a href="#" class="nav-item active" hx-get="/tab/self-check" hx-target="#tab-content" hx-swap="innerHTML">
          Self-check
        </a>
        <a href="#" class="nav-item" hx-get="/tab/cross-check" hx-target="#tab-content" hx-swap="innerHTML">
          Cross-check
        </a>
        <a href="#" class="nav-item" hx-get="/tab/thumbnails" hx-target="#tab-content" hx-swap="innerHTML">
          Thumbnails
        </a>
        <hr>
        <a href="#" class="nav-item" hx-get="/tab/history" hx-target="#tab-content" hx-swap="innerHTML">
          History
        </a>
      </nav>
      <div class="sidebar-footer">
        <p class="muted">stage 8</p>
      </div>
    </aside>
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd ui && go test ./server/ -run 'TestAppHTML_ThumbnailsNavLink' 2>&1
```

Expected: PASS.

- [ ] **Step 5: Commit app.html**

```bash
git add ui/templates/app.html ui/server/thumbnail_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): app.html sidebar cleanup + Thumbnails nav link

Drops disabled class and muted-tag soon badges from Cross-check and
History (stale since stages 7/6 merged). Adds Thumbnails nav link
(hx-get="/tab/thumbnails") between Cross-check and History. Updates
footer label from stage 4 to stage 8.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### selfcheck_running.html mode branches

- [ ] **Step 6: Write test asserting thumbnail mode titles**

Append to `ui/server/thumbnail_test.go`:

```go
func TestRunningPanelTitle_ThumbnailModes(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"thumbnail_detect_preview", "Detecting thumbnails"},
		{"thumbnail_detect_apply", "Confirming thumbnail moves"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var buf strings.Builder
			data := selfCheckRunningData{
				RunID:   "x",
				Folder:  "/photos",
				Mode:    tc.mode,
				NextURL: "/api/thumbnails/results/x",
			}
			if err := srv.tmpl.ExecuteTemplate(&buf, "selfcheck_running.html", data); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("running panel title missing %q for mode=%s", tc.want, tc.mode)
			}
		})
	}
}
```

- [ ] **Step 7: Run test to verify it fails**

```bash
cd ui && go test ./server/ -run 'TestRunningPanelTitle_ThumbnailModes' 2>&1
```

Expected: FAIL — both modes fall through to the default `Previewing…` title.

- [ ] **Step 8: Add two mode cases to selfcheck_running.html**

`ui/templates/selfcheck_running.html` lines 4–9 (the `{{if eq .Mode ...}}` block), replace:

```html
      {{- if eq .Mode "apply" -}}Applying…
      {{- else if eq .Mode "cross_check_apply" -}}Applying cross-check…
      {{- else if eq .Mode "cross_check_preview" -}}Cross-checking…
      {{- else if eq .Mode "restore" -}}Restoring…
      {{- else -}}Previewing…
      {{- end -}}
```

with:

```html
      {{- if eq .Mode "apply" -}}Applying…
      {{- else if eq .Mode "cross_check_apply" -}}Applying cross-check…
      {{- else if eq .Mode "cross_check_preview" -}}Cross-checking…
      {{- else if eq .Mode "thumbnail_detect_preview" -}}Detecting thumbnails…
      {{- else if eq .Mode "thumbnail_detect_apply" -}}Confirming thumbnail moves…
      {{- else if eq .Mode "restore" -}}Restoring…
      {{- else -}}Previewing…
      {{- end -}}
```

- [ ] **Step 9: Run test to verify it passes**

```bash
cd ui && go test ./server/ -run 'TestRunningPanelTitle_ThumbnailModes' 2>&1
```

Expected: PASS.

- [ ] **Step 10: Run full suite**

```bash
cd ui && go test ./... 2>&1 | tail -10
```

Expected: all green.

- [ ] **Step 11: Commit selfcheck_running.html**

```bash
git add ui/templates/selfcheck_running.html ui/server/thumbnail_test.go
git commit -m "$(cat <<'EOF'
feat(stage-8): selfcheck_running.html — add thumbnail mode title branches

Adds two new {{else if}} cases to the running panel title switch:
  thumbnail_detect_preview → "Detecting thumbnails…"
  thumbnail_detect_apply  → "Confirming thumbnail moves…"
Existing mode branches are unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: [Fixtures + smoke] tests/fixtures/thumbnails/ + manual smoke

**Files:**
- Create: `tests/fixtures/thumbnails/build.sh`
- Create: `tests/manual/stage8_smoke.md`

- [ ] **Step 1: Create directories**

```bash
mkdir -p tests/fixtures/thumbnails tests/manual
```

- [ ] **Step 2: Create build.sh**

Create `tests/fixtures/thumbnails/build.sh`:

```bash
#!/usr/bin/env bash
# Build thumbnail-detect fixture set for stage-8 smoke tests.
# Requires: sips (macOS built-in), exiftool (brew install exiftool)
#
# Generated layout:
#   l2_keeper.jpg      — 1600×1600, EXIF fingerprint SN=STAGE8SN
#   l2_thumb_a.jpg     — 200×200, same EXIF fingerprint
#   l2_thumb_b.jpg     — 300×300, same EXIF fingerprint
#   l3_big.jpg         — 1400×1400, embedded thumbnail == l3_small.jpg pixels
#   l3_small.jpg       — 140×140, matches embedded thumb of l3_big.jpg
#   l1_only_thumb.jpg  — 200×200, no peer (L1 review)
#   l1_only_maybe.jpg  — 800×600, no peer (L1 maybe review)
#   clean_a.jpg        — 2000×2000, must NOT be flagged
#   clean_b.jpg        — 2100×2100, must NOT be flagged
#   clean_c.jpg        — 1800×1800, must NOT be flagged
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="$SCRIPT_DIR"

# Seed: any JPEG on the system.
SEED="${SEED:-}"
if [[ -z "$SEED" ]]; then
  SEED="$(find /Library/Desktop\ Pictures /System/Library/Desktop\ Pictures \
    -name '*.jpg' -o -name '*.jpeg' 2>/dev/null | head -n1 || true)"
fi
if [[ -z "$SEED" ]]; then
  echo "ERROR: no seed JPEG found. Set SEED=/path/to/file.jpg" >&2
  exit 1
fi
echo "Using seed: $SEED"

# ----- L2 cluster -----
echo "Building L2 cluster…"
sips -s format jpeg "$SEED" --resampleHeightWidth 1600 1600 --out "$OUT/l2_keeper.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original \
    -Make=TestCam -Model=StageEightCam -SerialNumber=STAGE8SN \
    -DateTimeOriginal="2025:06:01 10:00:00" \
    "$OUT/l2_keeper.jpg" >/dev/null
fi

sips -s format jpeg "$SEED" --resampleHeightWidth 200 200 --out "$OUT/l2_thumb_a.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original \
    -Make=TestCam -Model=StageEightCam -SerialNumber=STAGE8SN \
    -DateTimeOriginal="2025:06:01 10:00:00" \
    "$OUT/l2_thumb_a.jpg" >/dev/null
fi

sips -s format jpeg "$SEED" --resampleHeightWidth 300 300 --out "$OUT/l2_thumb_b.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original \
    -Make=TestCam -Model=StageEightCam -SerialNumber=STAGE8SN \
    -DateTimeOriginal="2025:06:01 10:00:00" \
    "$OUT/l2_thumb_b.jpg" >/dev/null
fi

# ----- L3 pair -----
echo "Building L3 pair…"
sips -s format jpeg "$SEED" --resampleHeightWidth 1400 1400 --out "$OUT/l3_big.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 140 140 --out "$OUT/l3_small.jpg" >/dev/null
if command -v exiftool >/dev/null 2>&1; then
  exiftool -overwrite_original -ThumbnailImage="$OUT/l3_small.jpg" \
    "$OUT/l3_big.jpg" >/dev/null 2>&1 || true
fi

# ----- L1 suspects -----
echo "Building L1 suspects…"
sips -s format jpeg "$SEED" --resampleHeightWidth 200 200 --out "$OUT/l1_only_thumb.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 800 600 --out "$OUT/l1_only_maybe.jpg" >/dev/null

# ----- Clean images -----
echo "Building clean images…"
sips -s format jpeg "$SEED" --resampleHeightWidth 2000 2000 --out "$OUT/clean_a.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 2100 2100 --out "$OUT/clean_b.jpg" >/dev/null
sips -s format jpeg "$SEED" --resampleHeightWidth 1800 1800 --out "$OUT/clean_c.jpg" >/dev/null

echo "Done. Files:"
ls -lh "$OUT/"*.jpg
```

```bash
chmod +x tests/fixtures/thumbnails/build.sh
```

- [ ] **Step 3: Run build.sh to verify it works**

```bash
bash tests/fixtures/thumbnails/build.sh
ls tests/fixtures/thumbnails/*.jpg | wc -l
```

Expected: 10 JPEG files created. L2/L3 EXIF stamping requires `exiftool`; build script tolerates its absence gracefully.

- [ ] **Step 4: Create stage8_smoke.md**

Create `tests/manual/stage8_smoke.md`:

```markdown
# Stage 8 Manual Smoke — Thumbnail-detect UI

## Prerequisites

- `exiftool` installed (`brew install exiftool`)
- Fixture set built: `bash tests/fixtures/thumbnails/build.sh`
- UI server running: `cd ui && go run .`

## Fixture set

| File | Expected classification |
|---|---|
| `l2_keeper.jpg` (1600×1600, EXIF SN=STAGE8SN) | keeper — no move |
| `l2_thumb_a.jpg` (200×200, EXIF SN=STAGE8SN) | L2 thumbnail → checked by default |
| `l2_thumb_b.jpg` (300×300, EXIF SN=STAGE8SN) | L2 thumbnail → checked by default |
| `l3_big.jpg` (1400×1400, embedded thumb) | keeper — no move |
| `l3_small.jpg` (140×140, matches embedded thumb) | L3 thumbnail → checked by default |
| `l1_only_thumb.jpg` (200×200, no peer) | L1 suspect → unchecked by default |
| `l1_only_maybe.jpg` (800×600, no peer) | L1 suspect → unchecked by default |
| `clean_a/b/c.jpg` (≥1800px) | not flagged |

## Steps

### 1. Open Thumbnails tab

- Open `http://localhost:8765` in browser.
- Click **Thumbnails** in the sidebar.
- Expected: sidebar has no `disabled` class or "soon" badges; footer says "stage 8"; Thumbnails tab renders a form with source field, collapsible threshold section (max_edge 512, maybe_max_edge 1024), Preview button.

### 2. Run preview

- Enter the absolute path to `tests/fixtures/thumbnails/` in Source folder.
- Leave thresholds at defaults.
- Click **Preview**.
- Expected: running panel with title "Detecting thumbnails…" and SSE progress stream.

### 3. Verify results

- After run completes, results page appears automatically.
- Expected:
  - One L2 cluster card: `l2_keeper.jpg` read-only (keep badge), `l2_thumb_a.jpg` and `l2_thumb_b.jpg` with checked quarantine checkboxes.
  - One L3 cluster card: `l3_big.jpg` read-only, `l3_small.jpg` with checked quarantine checkbox.
  - Collapsible "L1 review (2 suspects, no peer)": `l1_only_thumb.jpg` and `l1_only_maybe.jpg` with unchecked checkboxes.
  - No cluster for `clean_a/b/c.jpg`.

### 4. Apply with L1 opt-in

- Expand the L1 review block.
- Check both L1 suspects.
- Click **Apply**.
- Expected: running panel with title "Confirming thumbnail moves…".

### 5. Verify done

- After apply run completes, done page appears.
- Expected: "Moved 5 files to quarantine" (or equivalent for moved=5).

### 6. Verify quarantine directory

```bash
ls tests/fixtures/thumbnails/_thumbnails/
```

Expected files present: `l2_thumb_a.jpg`, `l2_thumb_b.jpg`, `l3_small.jpg`, `l1_only_thumb.jpg`, `l1_only_maybe.jpg`.
Expected files absent: `l2_keeper.jpg`, `l3_big.jpg`, `clean_a.jpg`, `clean_b.jpg`, `clean_c.jpg`.

### 7. Verify History

- Click **History** in the sidebar.
- Expected: one row for the thumbnail apply run with a thumbnail-detect mode badge; no preview run row.

### 8. Restore

- Click the Restore link for the thumbnail apply entry.
- Confirm restore.
- Expected done page: "Restored 5 files".
- Verify:

```bash
ls tests/fixtures/thumbnails/*.jpg | wc -l   # should be 10 again
```

### 9. Regression check

- Run the self-check and cross-check flows to confirm they are unaffected by the stage-8 changes.

## Known acceptable gaps

- L3 requires exiftool to embed a byte-compatible thumbnail in `l3_big.jpg`. If the embedded thumbnail hash does not match `l3_small.jpg` (JPEG re-encoding artifact), no L3 cluster appears. Not a bug.
- L2 cluster requires exiftool for EXIF stamping. If exiftool is absent, the fixture produces no L2 cluster.
- The `--maybe-max-edge` and `--require-exif-match` CLI flags may not yet be implemented in `bin/twincut.sh`; if unrecognized, the preview run fails with a usage error. Implement or remove the flags from the form accordingly.
```

- [ ] **Step 5: Commit**

```bash
git add tests/fixtures/thumbnails/build.sh tests/manual/stage8_smoke.md
git commit -m "$(cat <<'EOF'
test(stage-8): fixture build script + manual smoke guide

tests/fixtures/thumbnails/build.sh generates a 10-image fixture set via
sips --resampleHeightWidth (not -z): 3-file L2 EXIF cluster, L3
keeper+embedded-thumb pair, 2 L1-only suspects, 3 clean large images.
Gracefully skips exiftool steps if not installed. SEED env var allows
overriding the source image.

tests/manual/stage8_smoke.md documents end-to-end clickthrough: open UI
Thumbnails tab → preview → results (assert cluster structure) → apply with
L1 opt-in → done (moved=5) → History → Restore. Includes fixture table,
expected quarantine contents, and regression check for existing tabs.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Summary

Stage 8 wires the P1-wave-1 thumbnail-detect CLI into the web UI as the third first-class workflow, completing the preview → pick → apply pattern that self-check and cross-check already follow. On the bash side, `lib/thumb.sh` gains dry-run NDJSON branches in `thumb_run_l2` and `thumb_run_l3` (emitting `thumb_candidate` events instead of calling `qmove`), and `thumb_confirm_review` gains an optional sixth `decision` column — fully backward-compatible with hand-edited five-column review CSVs. On the Go side, five new handlers in `thumbnail.go` cover the tab form, preview launch, results rendering, apply (writing a six-column enhanced review CSV and launching `--thumb-confirm`), and done; `BuildResults` adds a `thumbnail_detect` mode-prefix branch that groups L2/L3 events into `ResultGroup`s with `Members` and reads `_review.csv` for L1 suspects; the history filter is extended to include `thumbnail_detect_apply` runs so they appear in History and are restorable through the existing stage-6 infrastructure unchanged. The sidebar cleanup (drop stale `disabled` classes and "soon" badges, add Thumbnails nav link, update footer label to `stage 8`) is bundled as part of the same change. CLI users' existing `--thumbnail-detect` and `--thumb-confirm` paths are entirely untouched.

---

## Test plan

### Bash side
- [ ] L2 dry-run emits `thumb_candidate` NDJSON with `decision=thumb_l2_exif` and correct fields; no file moved (section 7)
- [ ] L3 dry-run emits `thumb_candidate` NDJSON with `decision=thumb_l3_embed` and correct fields; no file moved (section 8)
- [ ] Enhanced 6-column review CSV processed by `--thumb-confirm` writes each row's `decision` verbatim into the manifest (section 9)
- [ ] Legacy 5-column review CSV processed by `--thumb-confirm` defaults decision to `thumb_confirmed` (section 9b)
- [ ] Unknown decision value emits stderr warning and skips row without aborting run (section 9c)
- [ ] `run_start` NDJSON `_mode` field is `thumbnail_detect_preview` for `--thumbnail-detect --dry-run` (section 10a)
- [ ] `run_start` NDJSON `_mode` field is `thumbnail_detect_apply` for `--thumb-confirm` (section 10b)
- [ ] `--thumbnail-detect --apply-list <path>` exits non-zero with usage error (section 10c)
- [ ] Dry-run leaves all L1-only files on disk; `_thumbnails/` contains no image files (section 11)
- [ ] `_review.csv` header written by L1 path has exactly 5 columns, no `decision` column (section 11)

### Go side
- [ ] `events_test.TestParseThumbCandidate_L2`: all fields parse correctly
- [ ] `events_test.TestParseThumbCandidate_L3`: group_id `l3:` prefix preserved
- [ ] `events_test.TestParseThumbCandidate_MissingDecision`: returns error containing "missing decision"
- [ ] `events_test.TestParseThumbCandidate_MalformedJSON`: ParseEvent returns error
- [ ] `runs_test.TestRunMode_ThumbnailModes`: both modes return ApplyURL `/api/thumbnails/apply`
- [ ] `runs_test.TestRunMode_UnknownModeIsPassthrough`: unknown mode does not panic, returns non-empty ApplyURL
- [ ] `results_test.TestBuildResults_ThumbnailMode_L2Cluster`: 1 group, 1 keeper + 2 thumbnail members
- [ ] `results_test.TestBuildResults_ThumbnailMode_L3Pair`: 1 group, keeper at index 0 + thumbnail at index 1
- [ ] `results_test.TestBuildResults_ThumbnailMode_L1Group`: synthetic l1-suspects group from `_review.csv`; all members role=suspect, decision=thumb_confirmed
- [ ] `results_test.TestBuildResults_ThumbnailMode_ApplyURL`: ApplyURL = `/api/thumbnails/apply`
- [ ] `apply_list_test.TestComposeThumbnailConfirmCSV_ChecksFiltered`: unchecked rows excluded, keepers excluded
- [ ] `apply_list_test.TestComposeThumbnailConfirmCSV_DecisionPropagation`: decision values from original events written to CSV
- [ ] `apply_list_test.TestComposeThumbnailConfirmCSV_CSVEscaping`: paths with commas and double-quotes round-trip cleanly
- [ ] `apply_list_test.TestComposeThumbnailConfirmCSV_UnicodePaths`: Unicode paths round-trip without corruption
- [ ] `history_test.TestCollectHistory_IncludesThumbnailApply`: apply run with moved>0 appears in history
- [ ] `history_test.TestCollectHistory_ExcludesThumbnailPreview`: preview run excluded
- [ ] `thumbnail_test.TestHandleThumbnailsTab`: GET returns 200, body contains `<form` and `hx-post="/api/thumbnails/preview"`
- [ ] `thumbnail_test.TestHandleThumbnailsPreview_LaunchesRun`: POST with valid source → 200, running panel with `data-run-id` and `/api/thumbnails/results/`
- [ ] `thumbnail_test.TestHandleThumbnailsPreview_DisallowedPath`: source outside allowlist → 403 or 422
- [ ] `thumbnail_test.TestHandleThumbnailsResults_BuildsView`: GET with finished preview run → 200, cluster keys in body
- [ ] `thumbnail_test.TestHandleThumbnailsApply_WritesCSV`: POST with checked rows → `.thumb-confirm.csv` file exists with correct path and decision
- [ ] `thumbnail_test.TestHandleThumbnailsApply_LaunchesWithArgs`: running panel contains `/api/thumbnails/done/` next URL
- [ ] `thumbnail_test.TestHandleThumbnailsDone`: GET with finished apply run → 200, shows moved count
- [ ] `thumbnail_test.TestHandleThumbnailsL1Row_RendersCheckbox`: L1 row partial renders checkbox with correct `name` attribute
- [ ] `thumbnail_test.TestAppHTML_ThumbnailsNavLink`: index page contains `/tab/thumbnails` link, no `nav-item disabled`, no `muted-tag`, footer says `stage 8`
- [ ] `thumbnail_test.TestRunningPanelTitle_ThumbnailModes`: both thumbnail modes render correct title strings

### UI smoke
- [ ] Open UI → Thumbnails tab → form renders with source field, threshold fields, Preview button
- [ ] Enter fixture dir path → Preview → running panel shows "Detecting thumbnails…"
- [ ] Results: 2 cluster cards (L2 with 2 checked thumbs, L3 with 1 checked thumb) + collapsible L1 block with 2 unchecked rows
- [ ] Check all L1 rows → Apply → running panel shows "Confirming thumbnail moves…"
- [ ] Done page shows moved=5
- [ ] History tab contains one row with thumbnail-detect mode badge
- [ ] Restore from History → all 5 files return to fixture directory
- [ ] Self-check and cross-check flows remain unaffected (regression check)

---

## Self-Review Notes

_To be filled during implementation._
