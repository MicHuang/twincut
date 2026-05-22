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

## Stage 8.5 regression cases

### Replay regression (BLOCKER #1)

This test catches the source-scoped `_review.csv` re-emerging or any
similar leak that lets the apply view drift from the preview snapshot.

1. In a scratch source dir, drop ~6 small images (some look like
   thumbnails — under 512px on the long edge — others look like
   half-size dupes between 512 and 1024px).
2. Open the Web UI, hit **Thumbnails**, pick the scratch source dir,
   leave thresholds at default, **Preview**. Note the L1 suspect set.
   Copy the `preview_run_id` from the URL (or page footer if shown).
3. Without leaving the preview page, change `--thumb-max-edge` to a
   much smaller value (e.g. 64) and **Preview again**. The L1 set
   should now be empty or very different (fewer files qualify as
   "thumbnail-sized").
4. Navigate back to the FIRST preview by URL (`/runs/<preview_run_id>`
   or via the History tab). Confirm L1 group shows the **original**
   suspect set.
5. Check the L1 boxes you want to quarantine, **Apply**.
6. After apply completes, open the manifest TSV and the quarantine
   dir. Confirm the moved files match what you selected in the FIRST
   preview, NOT the second preview's (smaller) set.

If files from the SECOND preview's threshold show up in the
quarantine, the source-of-truth has drifted again — re-investigate
whether `_review.csv` is being written under `--json-events` or a
similar source-disk state has crept back into BuildResults.

### Manifest keeper validation (BLOCKER #2)

This test catches a regression where the apply TSV's keeper column
loses its hydration (Go side) or bash's `qmove` stops receiving it.

1. Set up a scratch source dir with at least one L2 hit (full-size
   image + EXIF-stripped thumbnail-size sibling — typically what
   `iPhoto` exports + a generated `convert -strip` thumbnail).
2. Preview, confirm L2 group shows up with both the keeper and the
   thumbnail.
3. Check the thumbnail in the L2 group, **Apply**.
4. Open the manifest TSV at the path shown in the apply result page.
5. Locate the row for the moved thumbnail. Verify the **matched**
   column contains the absolute path to the L2 keeper file. If empty,
   the keeper-hydration chain broke — check that:
   - `thumb_candidate` events in the preview run journal contain
     `keeper=<path>`,
   - `ResultMember.Keeper` is populated when BuildResults runs,
   - `composeThumbnailConfirmTSV` writes the 7th column,
   - `thumb_confirm_review` reads `$7` and passes it to `qmove`.
