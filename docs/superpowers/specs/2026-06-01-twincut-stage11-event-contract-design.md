# Stage 11 — Go-owned contract for cross-check / self-check / similar-video

**Date**: 2026-06-01
**Predecessors**: Stage 9 (Go-owned contract for `thumbnail_detect`, PR #10), Stage 9.5 (hygiene, PR #11), Stage 10 (pre-UI hardening, PR #12), portability cleanup (PR #13)
**Scope**: cross-check, source/backup self-check, similar-video. `thumbnail_detect` / restore / apply are already typed and out of scope.

## 1. Goal

Finish what Stage 9 started. Stage 9 moved `thumbnail_detect` onto a single typed NDJSON contract anchored in `ui/server/events.go` Go structs and guarded by a round-trip test. The cross-check / self-check / similar-video flows were explicitly deferred and still emit events through the generic, stringly-typed `emit_event "type" k=v …` helper in `bin/twincut.sh`.

Stage 11 retires `emit_event` entirely: every NDJSON line twincut emits flows through a per-type typed helper in `lib/events.sh`, every line is decoded by a typed Go struct, and every shape is exercised by the `DisallowUnknownFields` round-trip test. After this stage `grep -rn 'emit_event' bin/*.sh lib/*.sh` returns zero hits, including the definition.

## 2. Problem: the current "contract" is a facade

The typed triangle that exists today for these flows — `emit_dup_group` helper + `DupGroup` struct + `dup_group__cross_hash.ndjson` fixture — is **self-consistent but does not reflect the wire**. The real cross-check / self-check / similar-video output is richer, uses two different `remove` shapes, and is consumed on the Go side by an *ad-hoc anonymous struct* in `results.go:decodeGroup`, not by the `DupGroup` struct. The round-trip test passes only because its fixtures encode the minimal facade shape and never touch the real fields.

Concretely, three drifts:

| Event | typed helper / Go struct / fixture declare | what the live `emit_event` call sites actually emit | who consumes it |
|---|---|---|---|
| `dup_group` | `group_id, match_reason, keep_path, remove:[{path}]` | cross: `+algo +hash +keep_size +keep_mtime +remove_path/size/mtime` (singular); self: same but `remove:[{path,size,mtime}]` (array); similar-video: `+keep/remove_{duration,width,height,fps,bitrate}` | `results.go:decodeGroup` reads all of it via a **separate anonymous struct**; the typed `DupGroup` + `emit_dup_group` are **never called** (dead code) |
| `run_start` | `mode, source` | `+backups[] +quarantine +algo +min_size +dry_run +video_fast +video_fast_strict +exact` | results/history read only `mode`+`source`+`dry_run`; the rest is emitted-but-unconsumed |
| `run_end` | `status, duration_ms, total, applied, skipped, restored, …` | `total, dupes, moved, deleted, similar, *_internal_dupes, skipped_*, manifest_path, cancelled` (**no `status`**) | results reads `cancelled/moved/deleted/manifest_path` (legacy names); restore uses the typed dialect → **two run_end dialects coexist** |

Legacy `emit_event` call sites in `bin/twincut.sh`: `run_start` (1163), `run_end` (1751), cross dup_group (1466), self dup_group (1673), and the `emit_event dup_group` inside `emit_similar_video_group` (235; callers at 1327/1551/1574). The generic `emit_event` definition is lines 185–209 (comment 181–184).

## 3. Decisions (locked during brainstorming)

1. **Reconcile, don't preserve bytes.** The only consumer is the in-repo Go UI; there is no documented external NDJSON parser. We are free to change the wire format to a clean canonical schema and update the Go consumers in lockstep.
2. **Strict YAGNI on dead fields.** Drop every field that is emitted but unconsumed. Stage 11 stays a pure contract-sealing PR; surfacing scan counters in the UI is a separate future stage.
3. **Single PR, Go-contract-first internal order.** One Stage 11 PR. Internally: extend the Go structs + fold `decodeGroup` into an `UnmarshalDupGroup` + add fixtures/round-trip cases (lock & test the contract), *then* flip the five bash sites and delete `emit_event`.

Verified during exploration: the only event-field consumer among the "dead" candidates is `history.go:136` reading `run_start.dry_run` → **`dry_run` is kept**. `hash` on `dup_group` is kept (results displays it). `algo` is dropped (unconsumed).

## 4. Canonical schema

### 4.1 `dup_group`

`remove` is **always an array**; the singular `remove_path` shape is removed.

```json
{ "type":"dup_group","ts":<int>,"run_id":<str>,
  "group_id":<int>,
  "match_reason":"md5|video_fast|video_strict",
  "hash":"<str>",                          // md5 matches only; omitempty
  "keep_path":<str>, "keep_size":<int>, "keep_mtime":<int>,
  "keep_duration":<float>,"keep_width":<int>,"keep_height":<int>,"keep_fps":<float>,"keep_bitrate":<int>,  // similar-video only; omitempty
  "remove":[ { "path":<str>,"size":<int>,"mtime":<int>,
               "duration":<float>,"width":<int>,"height":<int>,"fps":<float>,"bitrate":<int> } ]          // video meta omitempty
}
```

- cross-check → 1 remove entry (with `hash` + size/mtime)
- self-check → N remove entries (one hash cluster)
- similar-video → 1 remove entry + keep/remove video meta on both sides
- `keep_size`/`keep_mtime` and each entry's `size`/`mtime` are **always present** (UI computes reclaimable bytes and shows mtime).
- `algo` dropped.

### 4.2 `run_start`

```json
{ "type":"run_start","ts":<int>,"run_id":<str>,"mode":<str>,"source":<str>,"dry_run":<bool> }
```

- Drop `backups[] / quarantine / algo / min_size / video_fast / video_fast_strict / exact`.
- `dry_run` kept (consumed by `history.go:136`); `emit_run_start` gains `--dry-run`.

### 4.3 `run_end`

One dialect; each mode selects a subset via `omitempty`.

```json
{ "type":"run_end","ts":<int>,"run_id":<str>,
  "status":"succeeded|failed|interrupted",     // NEW on cross/self
  "total":<int>,"duration_ms":<int>,           // shared, omitempty
  "moved":<int>,"deleted":<int>,"cancelled":<bool>,"manifest_path":<str>,  // cross/self
  "applied":<int>,"skipped":<int>,             // apply mode (existing)
  "restored":<int>,"missing":<int>,"unrecoverable":<int>,"errors":<int>     // restore mode (existing)
}
```

- Drop `dupes / similar / source_internal_dupes / backup_internal_dupes / skipped_hardlink / skipped_symlink`.
- cross/self gain `status` (default `succeeded`; `interrupted` when `cancelled=true`). `manifest_path / moved / deleted / cancelled` added to the typed `emit_run_end`.

## 5. Go-side changes (`ui/server/`)

**`events.go`** — extend the three structs to canonical; add a typed unmarshaller mirroring `UnmarshalThumbCandidate`:

- `DupGroup`: add `Hash` (omitempty), `KeepSize`, `KeepMTime`, keep video meta (omitempty); the `DupRemoveEntry` element grows from `{Path}` to `{Path,Size,MTime,Duration,Width,Height,FPS,Bitrate}` (meta omitempty). New `UnmarshalDupGroup(ev Event, *DupGroup) error`.
- `RunStart`: add `DryRun bool \`json:"dry_run,omitempty"\``.
- `RunEnd`: add `Moved`, `Deleted`, `Cancelled bool`, `ManifestPath` (others already present).

**`results.go`** — the key sealing point:

- Delete the ad-hoc anonymous struct in `decodeGroup` (with its singular `remove_path/remove_size/…` branch); decode via `UnmarshalDupGroup` + canonical `DupGroup`. `newResultFile` is fed from `DupRemoveEntry` / keep fields. The `if len(p.Remove)>0 … else if p.RemovePath!="" …` dual-shape branch is **deleted** — only the array path remains.
- The `run_end` handler's anonymous struct (reading `cancelled/moved/deleted/restored/manifest_path`) switches to the typed `RunEnd`. JSON field names are unchanged → zero behavior change.

**`history.go`** — reads `run_start.source`, `run_start.dry_run`, `run_end.manifest_path`; all JSON names unchanged → **left unmodified** (minimal-diff choice; could switch to typed structs later).

Net effect: Go has exactly one canonical decode path for `dup_group`, genuinely constrained by the round-trip `DisallowUnknownFields` check.

## 6. Bash-side changes

**`lib/events.sh`** — extend three helpers:

- `emit_run_start`: add `--dry-run`.
- `emit_run_end`: add `--moved`, `--deleted`, `--cancelled`, `--manifest-path`.
- `emit_dup_group`: extend to canonical. Keep side via flags (`--group-id --match-reason --hash --keep-path --keep-size --keep-mtime [--keep-duration/width/height/fps/bitrate]`). Remove side via a **repeatable** `--remove-json '<obj>'`, with a small composer `dup_remove_json path size mtime [dur w h fps bps]` building each entry. The helper owns the envelope and joins entries into the `remove` array; callers compose entries (self-check already builds the array inline at 1662–1671 — that logic ports over).

**`bin/twincut.sh`** — migrate the five sites and delete the generic helper:

| Line | now | becomes |
|---|---|---|
| 1163 | `emit_event run_start …` (8 fields) | `emit_run_start --mode --source --dry-run` |
| 1751 | `emit_event run_end …` (11 fields) | `emit_run_end --status succeeded --total --moved --deleted --manifest-path --cancelled` |
| 1466 | `emit_event dup_group` (cross, singular) | `emit_dup_group …` + 1 `--remove-json` |
| 1673 | `emit_event dup_group` (self, array) | `emit_dup_group …` + N `--remove-json` |
| 235 | `emit_event dup_group` inside `emit_similar_video_group` | rewrite the wrapper to call `emit_dup_group` (keep/remove with video meta); the 3 callers (1327/1551/1574) are unchanged |
| 185–209 (+181–184) | `emit_event(){…}` definition + comment | **deleted** |

## 7. Tests & verification

**`tests/events_contract.sh`** (fixture generator) — add/replace fixtures for the real shapes:

- `dup_group__cross_md5.ndjson` (hash + keep size/mtime + 1 remove) — **replaces** the minimal `dup_group__cross_hash.ndjson`
- `dup_group__self_md5_multi.ndjson` (N-entry remove array)
- `dup_group__similar_video.ndjson` (keep/remove video meta both sides)
- `run_start__crosscheck.ndjson` (with `dry_run`)
- `run_end__crosscheck.ndjson` (`status` + `moved/deleted/manifest_path/cancelled`)

**`ui/server/events_roundtrip_test.go`** — add the matching `fixtureCase`s with canonical typed `want` payloads. `DisallowUnknownFields` now genuinely constrains the cross/self/similar shapes.

**`tests/p1_stage11_smoke.sh`** (new; mirrors `p1_stage9_smoke.sh`):

- `--self-check <tmp> --dry-run --json-events`: assert ① `run_start.dry_run==true`, ② ≥1 `dup_group` with `remove` an array carrying `size`, ③ `run_end.status=="succeeded"`, ④ Go `ParseEvent`/`UnmarshalDupGroup` accept every line.
- cross-check dry-run: assert single-entry remove shape + `hash` present.

**Acceptance checklist** (gated by `superpowers:verification-before-completion`):

1. `make test` (`test-script` + `test-go`) green.
2. `grep -rn 'emit_event' bin/*.sh lib/*.sh` → **zero hits** (definition included).
3. Round-trip `DisallowUnknownFields` green on the real shapes.
4. Manual dry-runs of `--self-check` and `--source/--backup` cross-check: UI results page renders with no regression (similar-video metadata strip still present; reclaimable-bytes total still correct).

## 8. Risks

- **Multi-entry `remove[]` helper is the trickiest bash.** Composer + repeatable flag must stay **bash 3.2-compatible** (macOS; see the "bash-3 compat" lessons in git history) — no `readarray`, no associative arrays; reuse existing temp-file/`printf` patterns.
- **Apply path is unaffected.** cross/self apply flows through the HTML form (`apply_list.go` reads `form["quarantine"]`), not by re-reading the `dup_group` remove shape. Changing the array shape touches only the display decoder (`decodeGroup`), keeping blast radius small.

## 9. Out of scope

- Surfacing the dropped scan counters (`dupes/similar/internal_dupes/skipped_*`) in the UI — a clean follow-up stage on top of the now-honest contract.
- Switching `history.go` to typed structs.
- Any change to `thumbnail_detect` / restore / apply event flows (already typed).

## 10. Review plan

Cross-module schema/contract refactor (events.go / results.go / lib/events.sh / fixtures / 5 bash sites, >50 lines). After implementation: `reviewer-gemini` first. Because it is a cross-module contract refactor, **suggest** `reviewer-codex` for an adversarial design pass — dispatched only on explicit user confirmation (quota).

## 11. Post-review follow-ups (deferred — 2026-06-02)

Both reviewers ran. reviewer-gemini: no blocker (two harmless NITs:
`"0.0"`-string duration/fps not zero-suppressed; theoretically-empty
`remove[]` on a fully-filtered cluster). reviewer-codex: three MAJORs, all
premised on the NDJSON being a public/persisted contract. Verified against
the code and **none block** — but they're worth a future stage:

- **Composer seam (codex #5).** `emit_dup_group` splices caller-built
  `--remove-json` strings; the helper doesn't validate entries, so drift can
  re-enter at that seam (today controlled: `dup_remove_json` is the sole
  composer, 3 callers, guarded by fixtures + roundtrip). Follow-up: have
  `emit_dup_group` validate each entry (≥1, required `path/size/mtime`) or
  take structured per-entry args.
- **Journal observability (codex #2).** Run journals (`<state-dir>/runs/<id>.ndjson`)
  persist, so the dropped `run_start` config-echo + `run_end` aggregate
  counters reduce the journal's standalone forensic value. Mitigated today
  (recovery uses the manifest TSV; the journal still has every typed
  `dup_group`/`action`). Follow-up if audit matters: a compact typed audit
  event carrying scan inputs + terminal counters.
- **Schema versioning (codex #1).** No `schema_version` on events; the
  byte-exact-fixture coupling proves the present shape but has no
  evolvability/compat story. Old persisted journals are NOT typed-decoded on
  replay (`RunManager.Get` is in-memory-only; the history list reads only
  `run_start`/`run_end` metadata via generic access), so this is not a
  Stage 11 regression — but a `schema_version` would make the next change
  safer.
