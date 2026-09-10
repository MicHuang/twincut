# twincut Web UI i18n (en + zh-Hans) — Design

**Status:** approved design, pending implementation plan
**Task:** I-1 (`claude/i18n-zh-hans`)
**Author:** claude@macmini-yiqi, 2026-09-09
**Supersedes:** §4 and §10 stage 8 of
[`2026-05-15-twincut-web-ui-design.md`](2026-05-15-twincut-web-ui-design.md)

---

## 1. Why this exists, and why it is late

The 2026-05-15 Web UI design listed i18n as stage 8 of ten. It never landed.
The implementation renumbered the stages after stage 7 — the shipped "Stage 8"
was the Thumbnail-detect UI, and Stages 9/9.5/11 became the Go-owned event
contract — so the spec's stage 8 (i18n) and stage 9 (Settings panel) were
silently orphaned. Nothing rejected them; the numbering collision made them
invisible on every board for roughly four months.

The residue is visible in the tree today: `ui/main.go:36` advertises
`--lang en | zh-Hans`, and `Opts.Lang` (`ui/server/http.go:28`) is declared and
read by nothing. The flag is dead.

This is a stated user requirement, recorded at the original brainstorm
("I wanna add Mandarin to the UI as well so the user can switch languages"),
not a nice-to-have.

## 2. Goals

- The whole UI renders in English or Simplified Chinese, chosen per viewer.
- A visible switcher; the choice persists across restarts.
- Adding a third language later means dropping in one JSON file — no code change.
- A missing translation fails CI rather than reaching a user — with two limits worth
  stating plainly, both raised by the Tier-1 review (2026-09-10). The scanner recognises
  only the `{{t "literal"}}` form that §6.1 mandates, so a key written as
  `` {{t `key`}} ``, `{{ "key" | t }}` or via a bare `cat.lookup("…")` is invisible to
  it: the scanner enforces the rule, it does not detect a violation of the rule. And the
  checks that need the source tree run in CI, not in the shipped binary — see §6.

## 3. Non-goals

Explicitly out of scope, with reasons, so a later reader does not "fix" them:

| Not doing | Why |
|---|---|
| Localizing `bin/twincut.sh` output | The NDJSON event stream is a **machine contract** shared with Go (Stage 11) and pinned by `tests/fixtures/events/` + `events_roundtrip_test.go`. Translation belongs to the display layer only. Not one byte of the event contract changes. |
| Translating the raw log stream ("Show log ▾") | Debug detail; carried over from the original spec. |
| Translating `debug.html` / `debug_run.html` | Developer surfaces, not in the nav. ~59 JS strings for an audience of one. |
| Translating internal 500s (`"render: "+err.Error()`) | Internal failures. Translating them obstructs debugging. See §8. |
| `settings.json` persistence layer | The Settings panel (original spec stage 9) was never implemented; there is no file to read. The cookie plus `--lang` covers persistence. |
| Full RFC 4647 language negotiation | Two locales. A `zh*` prefix test is sufficient and honest. |
| Any client-side i18n library | Every fragment is server-rendered; the client never needs a catalog. |

## 4. Architecture

### 4.1 The problem being solved

There is no central render helper. Twenty-four `ExecuteTemplate` call sites are
spread across five files, and the data they pass is a mix of typed structs
(`selfCheckRunningData`, `historyView`, `selfCheckFormData`) and bare
`map[string]any`. Threading a translator through the *data* would mean editing
every view struct and every data literal — inconsistently, because the two
shapes need different edits.

### 4.2 The choice: one pre-parsed template set per locale

Bind the translator into the template **FuncMap**, not the data, and bind it at
parse time rather than render time:

```go
// ui/server/http.go, in New()
tmpls := make(map[string]*template.Template, len(cats))
for code, cat := range cats {
    fm := baseFuncMap()                      // dict, hasPrefix — unchanged
    fm["t"]    = func(k string) string { return cat.lookup(k) }
    fm["tmap"] = func(prefix string) map[string]string { return cat.subtree(prefix) }
    fm["lang"] = func() string { return code }
    tmpls[code] = template.Must(
        template.New("").Funcs(fm).ParseFS(opts.Assets, "templates/*.html"))
}
```

`Server.tmpl` becomes `Server.tmpls map[string]*template.Template`, plus:

```go
func (s *Server) tmplFor(r *http.Request) *template.Template {
    return s.tmpls[resolveLocale(r, s.opts.Lang, s.tmpls)]
}
```

Every call site changes mechanically:

```go
-  s.tmpl.ExecuteTemplate(w, "selfcheck_form.html", data)
+  s.tmplFor(r).ExecuteTemplate(w, "selfcheck_form.html", data)
```

**All twenty-four sites change, including the two debug ones.** The debug
templates contain no `{{t}}` calls, so the change is a no-op for them —
consistency is cheaper to maintain than a documented exception.

Properties this buys:

- **No view struct or data literal changes at all.**
- Because `t` closes over one catalog at parse time, a handler **cannot** render
  a mixed-language page. The failure mode is structurally absent, not merely
  untested.
- Zero per-request cost. Parsing happens `len(locales)` times at startup.
- Cost: one template set per locale in memory. Fifteen small files × 2.

### 4.3 Alternatives rejected

**Per-request `Clone()` + `Funcs()`.** Same template syntax, but deep-copies
fifteen templates on every request to buy nothing that §4.2 does not already
give. It moves fixed startup work into the hot path.

**Locale in the data (`{{t .Lang "key"}}`).** Requires every view struct and
every `map[string]any` to carry `Lang`; touches all twenty-four data
constructions in two different styles. Most invasive, least benefit.

### 4.4 New file: `ui/server/i18n.go`

```go
type catalog map[string]string

// lookup returns the translation for k. Startup validation (§6) makes a miss
// unreachable in a shipped binary; if one somehow occurs, it returns a loud
// marker ("!"+k+"!") rather than an empty string, so a defect is visible on the
// page instead of silently erasing UI text.
func (c catalog) lookup(k string) string

// subtree returns every key under prefix with the prefix stripped, flat.
// tmap "progress." over {"progress.error", "progress.phase.scan"} yields
// {"error": …, "phase.scan": …} — keys stay flat, dots and all.
func (c catalog) subtree(prefix string) map[string]string

func loadCatalogs(fsys fs.FS) (map[string]catalog, error)
func validateCatalogs(cats map[string]catalog, used []string) error
func resolveLocale(r *http.Request, flagLang string, avail map[string]catalog) string
```

`//go:embed templates/*.html static/*` in `ui/main.go` gains `locales/*.json`.

## 5. Locale resolution

The original spec's four-layer chain loses its second layer (`settings.json`
does not exist), leaving:

| # | Source | Notes |
|---|---|---|
| 1 | `lang` cookie | The user's explicit in-UI choice. **The value is untrusted input and must be validated against loaded locales**; an unknown value falls through rather than erroring. |
| 2 | `--lang` flag | The operator default, replacing the settings layer. Validated at startup — an unknown value is `log.Fatalf`, never a silent ignore. |
| 3 | `Accept-Language` | `zh*` → `zh-Hans`, else `en`. |
| 4 | — | `en`. |

The cookie outranks the flag deliberately: the flag's own help text says
"default: auto from Accept-Language", i.e. it overrides *detection*, not a
person's explicit pick.

## 6. Catalogs

**Location and format.** `ui/locales/en.json`, `ui/locales/zh-Hans.json`. Flat
key → string, e.g. `{"button.preview": "Preview"}` / `{"button.preview": "预览"}`.
Roughly 200–230 keys.

**Naming.** Dotted, segmented by surface: `nav.selfCheck`, `selfcheck.form.title`,
`button.preview`, `badge.md5`, `progress.error`, `err.pathNotAllowed`. The
codebase has no prior i18n naming to follow (checked, per CLAUDE.md's
"search the codebase first" rule), so this document defines the convention.

**Missing keys are a build defect, not a runtime condition.** Catalogs are
`embed.FS` assets baked into the binary, exactly like templates. The existing
code already panics on a template parse failure
(`panic("twincut-ui: parse embedded templates: " + …)`). Catalog validation
takes the same posture: `New()` panics if the locales' key sets are not
identical. **What `New()` does not check, deliberately:** it passes `nil` for the
used-key list, because `templateKeys()` reads the working tree and a shipped
binary has no source to scan. So key coverage and value non-emptiness are
CI-time guarantees (`TestCatalogsCoverEveryUsedKey`,
`TestCatalogsHaveNoUnusedKeys`, `TestTmapBridgeRequiredSubkeys`,
`TestRenderAllLocales`), not startup ones. A binary built from a tree that
never ran those tests can therefore ship an empty value. CI is where a user
must never be the one who discovers a missing translation.

### 6.1 Hard rule: `t` takes string literals only

```
{{t "badge.md5"}}                              ✅
{{t (printf "badge.%s" .MatchReason)}}         ❌ forbidden
```

A dynamically built key is invisible to the coverage scanner (§9), which would
tear a hole in the one mechanism that keeps translations from silently rotting.

This matters because `MatchReason` (`md5`, `video_fast`, …), the thumbnail
`Reason` (`l1_only_thumb`, `l1_only_maybe`, `l1_phash_match`) and `Decision`
(`thumb_l2_exif`, …) are machine tokens that arrive from bash and are already
rendered through explicit `{{if eq …}}` branches in the templates. Those
branches stay. Go continues to pass tokens; only the template decides words.

## 7. Live-progress JS bridge

Some user-facing text is produced by inline JavaScript inside
`selfcheck_running.html`, where a server-side `t` cannot reach: the literals
`'Error'` and `' (stream closed)'`, and the bash `phase` token, which is
currently rendered raw — the user literally sees `scan` today.

`html/template` serializes a Go value to JSON in a JS context, so the bridge is
one line, with no hand-written marshaling:

```html
<script>const I18N = {{tmap "progress."}};</script>
```

```js
progressText.textContent = I18N.error;                    // was 'Error'
progressText.textContent += ' ' + I18N.streamClosed;      // was ' (stream closed)'
const label = I18N['phase.' + p.phase] ?? p.phase;        // was `${p.phase}`
```

`subtree` keeps keys flat, so the lookup is bracketed on `'phase.' + token`
rather than a nested object — one less transformation between catalog and page.

**An unknown phase falls back to the raw token, never to blank.** `bin/twincut.sh`
emits exactly three today (`scan`, `apply`, `restore`); a fourth added later
must degrade to something visible rather than erasing the progress line.

## 8. Go-side errors

New helper, used **only for user-triggerable 4xx**:

```go
func (s *Server) httpErrorT(w http.ResponseWriter, r *http.Request, key string, status int)
```

In scope (about a dozen of the 128 `http.Error` sites): allowlist rejection for
source, backup, self-check-folder, and open-path targets, form-parse 400,
missing `preview_run_id`, preview-run-not-found, wrong-mode 422.

`restore-conflict` was removed from this list during Task 8 (2026-09-09): it
names `bin/twincut.sh`'s `restore_conflict` **event kind**, not an HTTP error —
it has no Go handler and no status code, and reaches a user only through the
raw NDJSON action-log stream, which §3's non-goals already keep untranslated.
Listing it here was a category error in this spec, not an implementation gap.

Out of scope: everything shaped like `http.Error(w, "render: "+err.Error(), 500)`.
These are internal failures whose value is the raw Go error; translating them
obstructs debugging and would put untranslatable interpolated text into a
catalog. **This boundary is normative** — without it, a later contributor
reasonably "finishes the job" and translates all 128.

## 9. Testing

The coverage scanner is the load-bearing test; the rest are ordinary.

| Test | Pins |
|---|---|
| `TestCatalogKeysMatch` | Both catalogs have byte-identical key sets. |
| `TestCatalogCoversTemplates` | Scans `ui/templates/*.html` for `{{t "…"}}` / `{{tmap "…"}}` literals and Go for `httpErrorT(…, "…", …)` literals; asserts every key exists in **both** catalogs. **This is the durable answer to "someone adds a string and forgets to translate it" — CI catches it, not a person's memory.** |
| `TestResolveLocale` | Table-driven: valid cookie, invalid cookie, flag, `Accept-Language` variants, default. |
| `TestRenderAllLocales` | Every locale × every template renders without panic and with no empty translation. |
| `TestEnglishRenderUnchanged` | Phase-1 only (§11): the English render is byte-identical to pre-change output. |

The twenty-four mechanical call-site edits are covered by the existing handler
tests — a broken render fails them. CI needs no change; `go test ./...` already
runs in the `go-tests` job.

## 10. Terminology (draft — needs user sign-off)

The user is bilingual and owns these calls. Implementation follows this table
once approved; the notes explain choices that are not one-to-one.

| English | 中文 | Note |
|---|---|---|
| twincut | twincut | Product name, untranslated |
| Self-check | 自检 | Duplicates within one folder |
| Cross-check | 交叉比对 | Source against backup |
| Thumbnails | 缩略图 | |
| History | 历史记录 | |
| source | 源目录 | |
| backup | 备份目录 | |
| duplicate | 重复文件 | |
| de-dup | 去重 | Deduplication used as a compound modifier (e.g. "Past de-dup applies"); distinct from *duplicate* (重复文件), the noun for a matched file. Precedent set in `history.subtitle` (Task 7); recorded here per that task's reviewer so the choice is not re-litigated. |
| quarantine (place) / quarantine (action) | 隔离区 / 隔离 | Not 检疫; files are moved here, not destroyed. The noun names the destination folder; the verb is the checkbox/action label — do not use 隔离区 for the action (found in `results.quarantine` during Task 6 review, R8) |
| Preview (dry-run) | 预览 | Button; body text spells out "试运行，不会移动任何文件" |
| Apply | 执行 | Not 应用 — this is the destructive step that actually moves files |
| Restore | 还原 | |
| keep / keeper | 保留 / 保留项 | |
| hash | 哈希 | |
| match reason | 匹配依据 | |
| similar-video | 相似视频 | |
| scan | 扫描 | `phase` token |
| min size | 最小文件大小 | |
| extensions | 扩展名 | |
| allowlist | 允许范围 | Appears in error copy |
| suspect | 疑似 | L1 pHash |
| cluster / group | 分组 | |
| bad video | 损坏视频 | |
| sidecar | 附属文件 | |
| AppleDouble | AppleDouble | Technical term, untranslated |

## 11. Phased delivery

Each phase is independently verifiable and independently landable.

**Phase 1 — plumbing, zero user-visible change.** `i18n.go`, catalogs (English
extracted, `zh-Hans` as same-key placeholders), `tmpls`/`tmplFor`, the 24 call
sites, the embed directive, the test skeleton.
*Acceptance:* `TestEnglishRenderUnchanged` proves the English output is
byte-identical to today. A refactor this wide must be provably inert before any
copy changes on top of it.

**Phase 2 — copy.** Extract the ~194 template strings plus attributes into
catalogs; write the `zh-Hans` draft; settle §10.
*Acceptance:* key-coverage and both-locale render tests green.

**Phase 3 — interaction.** `POST /api/lang`, the switcher, run-active disabling,
the JS bridge, the Go 4xx translations.
*Acceptance:* end-to-end switch; switcher greys out during a run.

## 12. Switcher

A dropdown in the `app.html` header, data-driven from the loaded locale set
(§2 goal 3: dropping a new `ui/locales/*.json` file in is enough, no code
change) rather than a hard-coded pair of options — today that set has two
members (English, 简体中文), so the dropdown has two items, but "two-item" here
describes the current catalog set, not a limit the implementation enforces.
Each catalog names its own display string via a `locale.name` key (e.g.
`"English"` / `"中文"`), so the dropdown can show every locale's self-name
regardless of which locale is currently rendering. `POST /api/lang` validates the
submitted code, sets the cookie (`Path=/`, `SameSite=Lax`, `HttpOnly`, one year,
no `Secure` — this is `http://localhost`), returns 204; the client calls
`location.reload()`.

CSRF protection is free: `originGuard` is mux-wide and enforces an Origin check
on every non-GET.

**Full reload, and the switcher is disabled while a run is active** (user
decision, 2026-09-09). `app.html`'s main region is hard-coded to
`{{template "selfcheck_form.html"}}`, so a reload during a run returns to the
form and the running view is lost from the screen — the subprocess survives, but
the user cannot see it. Rather than build per-tab resumable URLs (the largest
single piece of complexity available here), the switcher is greyed out for the
duration: `selfcheck_running.html`'s existing SSE lifecycle already has the
hooks — set `document.body.dataset.runActive = "1"` when the stream opens, clear
it on `run_end` / close, and let the shell style `body[data-run-active]
.lang-switcher`. No new state machine.

## 13. Files

**New:** `ui/locales/en.json`, `ui/locales/zh-Hans.json`, `ui/server/i18n.go`,
`ui/server/i18n_test.go`

**Modified:** `ui/main.go` (embed directive, `--lang` validation);
`ui/server/http.go` (`tmpls`, `tmplFor`, `New()`, `POST /api/lang`,
`httpErrorT`, `<html lang>`); `ui/server/{selfcheck,crosscheck,history,thumbnail,openpath}.go`
(render sites + user-facing 4xx — `openpath.go` added during Task 8 for the
open-path allowlist rejection, §8); 12 templates; `ui/static/app.css` (switcher).

One drive-by, user-approved: `app.html`'s sidebar footer reads `stage 8`, which
has been wrong since Stage 9. Corrected while the file is open.

## 14. Verified facts

Measured on `main` at `c16a85c`, 2026-09-09. Recorded so the plan does not
re-derive them.

| Fact | Evidence |
|---|---|
| 24 `ExecuteTemplate` sites, no central render helper | `crosscheck.go` ×6, `history.go` ×4, `http.go` ×3, `selfcheck.go` ×6, `thumbnail.go` ×5 |
| Template data is mixed typed-struct / `map[string]any` | e.g. `selfcheck.go:38` struct vs `crosscheck.go:53` map |
| `html/template`, not `text/template` | `ui/server/http.go:11` |
| Existing FuncMap: `dict`, `hasPrefix` | `ui/server/http.go:45-60` |
| Embed directive | `ui/main.go:28` — `templates/*.html static/*` |
| `Opts.Lang` declared, read nowhere | `ui/server/http.go:28`; `grep` finds 3 hits, all in that declaration block |
| `settings.json` unimplemented | No hit in `ui/server/*.go` or `ui/main.go` |
| `originGuard` is mux-wide, non-GET Origin check | `ui/server/http.go:147-159` |
| bash emits exactly 3 `phase` tokens | `bin/twincut.sh` — `scan`, `apply`, `restore` |
| `MatchReason` / `Reason` are machine tokens | `ui/server/results.go:41,64` |
| ~194 visible text runs across 15 templates | tag-stripped count, per file |
| 128 `http.Error` sites total | `grep -c`, `ui/server/*.go` excluding tests |
| `<html lang="en">` and footer `stage 8` hard-coded | `ui/templates/app.html:2,35` |
| Baseline green | `make test` exit 0, `make build` ok, `gofmt -l ui/` empty, `go vet` clean, CI run `30568064713` green ×4 |
