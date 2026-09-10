package server

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"locales/en.json":      {Data: []byte(`{"a.one":"One","a.two":"Two"}`)},
		"locales/zh-Hans.json": {Data: []byte(`{"a.one":"一","a.two":"二"}`)},
	}
}

func TestLoadCatalogs(t *testing.T) {
	cats, err := loadCatalogs(testFS())
	if err != nil {
		t.Fatalf("loadCatalogs: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("want 2 catalogs, got %d", len(cats))
	}
	if got := cats["zh-Hans"]["a.one"]; got != "一" {
		t.Errorf("zh-Hans a.one = %q, want 一", got)
	}
}

func TestLoadCatalogsRejectsEmptyDir(t *testing.T) {
	if _, err := loadCatalogs(fstest.MapFS{}); err == nil {
		t.Fatal("want error for a tree with no catalogs, got nil")
	}
}

func TestCatalogLookupMarksMissingKey(t *testing.T) {
	c := catalog{"a.one": "One"}
	if got := c.lookup("a.one"); got != "One" {
		t.Errorf("lookup hit = %q", got)
	}
	if got := c.lookup("nope"); got != "!nope!" {
		t.Errorf("lookup miss = %q, want !nope! — a miss must be loud, never empty", got)
	}
}

func TestCatalogSubtreeStripsPrefixAndStaysFlat(t *testing.T) {
	c := catalog{"progress.error": "E", "progress.phase.scan": "S", "other.x": "X"}
	got := c.subtree("progress.")
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	if got["error"] != "E" || got["phase.scan"] != "S" {
		t.Errorf("subtree = %v; keys must stay flat, dots and all", got)
	}
}

func TestValidateCatalogsRejectsMissingKey(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"a.one": "One", "a.two": "Two"},
		"zh-Hans": {"a.one": "一"},
	}
	err := validateCatalogs(cats, nil)
	if err == nil {
		t.Fatal("want error when zh-Hans is missing a key present in en")
	}
}

func TestValidateCatalogsRejectsExtraKey(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"a.one": "One"},
		"zh-Hans": {"a.one": "一", "a.extra": "多"},
	}
	if err := validateCatalogs(cats, nil); err == nil {
		t.Fatal("want error when zh-Hans defines a key en does not")
	}
}

func TestValidateCatalogsRejectsUsedButUndefined(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"a.one": "One"},
		"zh-Hans": {"a.one": "一"},
	}
	if err := validateCatalogs(cats, []string{"a.one", "a.missing"}); err == nil {
		t.Fatal("want error when a used key is defined in no catalog")
	}
}

func TestValidateCatalogsAcceptsMatchingSets(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"a.one": "One"},
		"zh-Hans": {"a.one": "一"},
	}
	if err := validateCatalogs(cats, []string{"a.one"}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// TestValidateCatalogsAcceptsPopulatedTmapPrefix covers a {{tmap "sub."}}
// call site: the used entry ends in ".", so it must be satisfied by subtree
// coverage (at least one "sub.*" key in every catalog), not by demanding a
// literal catalog key spelled "sub." itself — a key that would never be
// displayed anywhere.
func TestValidateCatalogsAcceptsPopulatedTmapPrefix(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"sub.x": "X", "sub.y": "Y"},
		"zh-Hans": {"sub.x": "X的中文", "sub.y": "Y的中文"},
	}
	if err := validateCatalogs(cats, []string{"sub."}); err != nil {
		t.Fatalf("want nil for a tmap prefix whose subtree is populated in every catalog, got %v", err)
	}
}

// TestValidateCatalogsRejectsEmptyTmapPrefixSubtree covers the failure mode
// that actually matters: a {{tmap "sub."}} call site whose subtree has zero
// matching keys in a catalog. Left unchecked, that call site would silently
// serve an empty {} object to the browser instead of failing the build. The
// error must read as a distinct defect from an exact-key miss, so this also
// asserts on the message shape, not just err != nil.
func TestValidateCatalogsRejectsEmptyTmapPrefixSubtree(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"other.x": "X"},
		"zh-Hans": {"other.x": "X的中文"},
	}
	err := validateCatalogs(cats, []string{"sub."})
	if err == nil {
		t.Fatal("want error when a used tmap prefix has no matching key in any catalog")
	}
	if !strings.Contains(err.Error(), "subtree") {
		t.Errorf("error should read as an empty-subtree defect (mentioning \"subtree\"), got: %v", err)
	}
	if strings.Contains(err.Error(), "is used but not defined") {
		t.Errorf("error should not read as an exact-key-miss defect: %v", err)
	}
}

// tmapRequiredSubkeys maps each {{tmap "prefix"}} call site to the literal
// dotted subkeys its JS consumer actually reads off the resulting object
// (I18N.<subkey>). This is a stricter, consumer-shaped pin than
// validateCatalogs' tmap check above: that check only demands the subtree be
// non-empty, which a single unrelated sibling key satisfies even while the
// one subkey a script reads is missing. Consumer sites:
//   - selfcheck_running.html: {{tmap "progress."}} — I18N.error (:104, no
//     fallback), I18N.streamClosed (:118, no fallback),
//     I18N['phase.' + p.phase] (:81, "?? p.phase" fallback — not required
//     here for that reason, but the phase.* keys still are, since a bad
//     catalog entry would render an empty string instead of falling back).
//   - selfcheck_results.html: {{tmap "results.file."}} — I18N.one / I18N.other
//     (:250, "??" fallback since Fix 1, but still required so a defect
//     degrades to the hardcoded English fallback instead of silently
//     rendering nothing).
var tmapRequiredSubkeys = map[string][]string{
	"progress.":     {"error", "streamClosed", "phase.scan", "phase.apply", "phase.restore"},
	"results.file.": {"one", "other"},
}

// TestTmapBridgeRequiredSubkeys pins the tmap bridge contract at the
// granularity its JS consumers actually read, closing the gap
// TestValidateCatalogsAcceptsPopulatedTmapPrefix leaves open: validateCatalogs
// only checks that a used tmap prefix's subtree is non-empty in every
// catalog, so deleting one specific required subkey (e.g. "progress.error")
// while a sibling key (e.g. "progress.phase.scan") survives leaves that
// check green. This test instead asserts every subkey a real call site reads
// is present and non-empty in every catalog.
//
// Red-first, verified by hand against the real catalogs (not asserted here,
// since asserting it would just be re-deriving validateCatalogs' own
// behavior): temporarily deleting "progress.error" from both
// ui/locales/en.json and ui/locales/zh-Hans.json turns this test red with
//
//	catalog en: tmap prefix "progress." is missing required subkey "error" (read as I18N.error)
//	catalog zh-Hans: tmap prefix "progress." is missing required subkey "error" (read as I18N.error)
//
// while TestCatalogsCoverEveryUsedKey, TestCatalogsHaveNoUnusedKeys, and
// TestValidateCatalogsAcceptsPopulatedTmapPrefix-shaped coverage all stay
// green, because "progress.phase.scan" etc. keep the subtree non-empty.
// Restoring the key turns it green again. See final-fix-report.md for the
// actual `go test` transcript from that run.
func TestTmapBridgeRequiredSubkeys(t *testing.T) {
	cats, err := loadCatalogs(os.DirFS(".."))
	if err != nil {
		t.Fatalf("loadCatalogs: %v", err)
	}
	for _, code := range sortedKeys(cats) {
		for _, prefix := range sortedKeys(tmapRequiredSubkeys) {
			sub := cats[code].subtree(prefix)
			for _, k := range tmapRequiredSubkeys[prefix] {
				v, ok := sub[k]
				if !ok {
					t.Errorf("catalog %s: tmap prefix %q is missing required subkey %q (read as I18N.%s)", code, prefix, k, k)
					continue
				}
				if strings.TrimSpace(v) == "" {
					t.Errorf("catalog %s: tmap prefix %q required subkey %q is empty", code, prefix, k)
				}
			}
		}
	}
}

func TestResolveLocale(t *testing.T) {
	avail := map[string]catalog{"en": {}, "zh-Hans": {}}

	cases := []struct {
		name   string
		cookie string
		flag   string
		accept string
		want   string
	}{
		{name: "no signals falls back to en", want: "en"},
		{name: "cookie wins", cookie: "zh-Hans", flag: "en", accept: "en-US", want: "zh-Hans"},
		{name: "unknown cookie falls through to flag", cookie: "klingon", flag: "zh-Hans", want: "zh-Hans"},
		{name: "unknown flag falls through to accept", flag: "klingon", accept: "zh-CN,zh;q=0.9", want: "zh-Hans"},
		{name: "flag beats accept", flag: "en", accept: "zh-CN", want: "en"},
		{name: "accept zh-CN maps to zh-Hans", accept: "zh-CN,zh;q=0.9,en;q=0.8", want: "zh-Hans"},
		{name: "accept zh-TW also maps to zh-Hans", accept: "zh-TW", want: "zh-Hans"},
		{name: "accept en-US maps to en", accept: "en-US,en;q=0.9,zh;q=0.8", want: "en"},
		{name: "accept garbage maps to en", accept: ";;;", want: "en"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: langCookie, Value: tc.cookie})
			}
			if tc.accept != "" {
				r.Header.Set("Accept-Language", tc.accept)
			}
			if got := resolveLocale(r, tc.flag, avail); got != tc.want {
				t.Errorf("resolveLocale = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSupportedLocalesIsSorted(t *testing.T) {
	got, err := SupportedLocales(testFS())
	if err != nil {
		t.Fatalf("SupportedLocales: %v", err)
	}
	if len(got) != 2 || got[0] != "en" || got[1] != "zh-Hans" {
		t.Errorf("SupportedLocales = %v, want [en zh-Hans]", got)
	}
}

// keyLiteralRe matches {{t "key"}} and {{tmap "prefix"}} with a STRING LITERAL
// argument only. A computed key — {{t (printf …)}} — is deliberately not
// matched: it would be invisible to this scanner, which is why the spec
// forbids it.
var keyLiteralRe = regexp.MustCompile(`\{\{-?\s*t(?:map)?\s+"([^"]+)"\s*-?\}\}`)

// goKeyRe matches httpErrorT(w, r, key, …) and localeCatalogKey(key), where
// key is a quoted string literal in real call sites. (Written unquoted here
// deliberately: this package directory is itself in the *.go scan glob, and
// a quoted "key" in this very comment would satisfy the pattern below and
// self-inject a phantom used-key — caught by TestCatalogsCoverEveryUsedKey
// during TDD.)
//
// localeCatalogKey is intentionally its own narrow alternative rather than a
// blanket match on catalog.lookup(: several *_test.go files in this same
// scan glob call .lookup with literal fixture strings that are not real
// catalog keys (e.g. "a.one", "nope"); matching those would make
// TestCatalogsCoverEveryUsedKey demand the real catalogs define them.
var goKeyRe = regexp.MustCompile(`(?:httpErrorT\([^,]+,[^,]+,|localeCatalogKey\()\s*"([^"]+)"`)

func scanKeys(src string) []string {
	seen := map[string]bool{}
	for _, m := range keyLiteralRe.FindAllStringSubmatch(src, -1) {
		seen[m[1]] = true
	}
	return sortedKeys(seen)
}

// templateKeys returns every catalog key referenced by a template or by Go.
// It reads from disk relative to the package directory, so it is a TEST-ONLY
// helper: the shipped binary has no source tree to scan. New() never calls it.
func templateKeys() []string {
	seen := map[string]bool{}
	collect := func(dir, glob string, re *regexp.Regexp) {
		paths, _ := filepath.Glob(filepath.Join(dir, glob))
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				seen[m[1]] = true
			}
		}
	}
	collect("../templates", "*.html", keyLiteralRe)
	collect(".", "*.go", goKeyRe)
	return sortedKeys(seen)
}

func TestScanTemplateKeysFindsLiterals(t *testing.T) {
	src := `<p>{{t "a.one"}}</p><span>{{ t  "a.two" }}</span>
	<script>const I={{tmap "progress."}};</script>{{t (printf "bad.%s" .X)}}`
	got := scanKeys(src)
	want := map[string]bool{"a.one": true, "a.two": true, "progress.": true}
	if len(got) != len(want) {
		t.Fatalf("scanKeys = %v, want %v — a computed key must not be picked up", got, want)
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

func TestCatalogsCoverEveryUsedKey(t *testing.T) {
	cats, err := loadCatalogs(os.DirFS(".."))
	if err != nil {
		t.Fatalf("loadCatalogs: %v", err)
	}
	if err := validateCatalogs(cats, templateKeys()); err != nil {
		t.Fatalf("catalog coverage: %v", err)
	}
}

func TestCatalogsHaveNoUnusedKeys(t *testing.T) {
	cats, err := loadCatalogs(os.DirFS(".."))
	if err != nil {
		t.Fatalf("loadCatalogs: %v", err)
	}
	used := make(map[string]bool)
	for _, k := range templateKeys() {
		used[k] = true
	}
	for _, k := range sortedKeys(cats[defaultLocale]) {
		if used[k] {
			continue
		}
		// A tmap prefix covers its whole subtree.
		covered := false
		for u := range used {
			if strings.HasSuffix(u, ".") && strings.HasPrefix(k, u) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("catalog key %q is defined but used by nothing — delete it or use it", k)
		}
	}
}

// TestHttpErrorTUsesRequestLocale uses the real "err.sourceNotAllowed" key
// (not a fixture-only name) deliberately: templateKeys()'s goKeyRe scans
// every *.go file including this one, so a literal httpErrorT(..., "key", …)
// call right here registers "key" as used. A fixture name with no catalog
// entry would fail TestCatalogsCoverEveryUsedKey — the fixture's TABLE
// VALUES below are still test-only and need not match the real catalog copy.
func TestHttpErrorTUsesRequestLocale(t *testing.T) {
	cats := map[string]catalog{
		"en":      {"err.sourceNotAllowed": "outside the allowlist"},
		"zh-Hans": {"err.sourceNotAllowed": "不在允许范围内"},
	}
	srv := &Server{opts: Options{}}
	srv.cats = cats

	for _, tc := range []struct{ cookie, want string }{
		{"", "outside the allowlist"},
		{"zh-Hans", "不在允许范围内"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: langCookie, Value: tc.cookie})
		}
		w := httptest.NewRecorder()
		srv.httpErrorT(w, r, "err.sourceNotAllowed", http.StatusForbidden)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("body = %q, want it to contain %q", w.Body.String(), tc.want)
		}
	}
}

func TestSetLangCookie(t *testing.T) {
	srv := &Server{opts: Options{}}
	srv.cats = map[string]catalog{"en": {}, "zh-Hans": {}}

	t.Run("accepts a known locale", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/lang", strings.NewReader("lang=zh-Hans"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.handleSetLang(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", w.Code)
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != langCookie || cookies[0].Value != "zh-Hans" {
			t.Fatalf("cookies = %v", cookies)
		}
		if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" {
			t.Errorf("cookie attributes wrong: %+v", cookies[0])
		}
		// R14: these two are what a session-only-cookie regression or an
		// accidental Secure flag (which would break the cookie over plain
		// http://localhost) would trip. Asserted separately from the block
		// above so a failure here names the attribute, not just "wrong".
		if want := 365 * 24 * 60 * 60; cookies[0].MaxAge != want {
			t.Errorf("MaxAge = %d, want %d (one year)", cookies[0].MaxAge, want)
		}
		if cookies[0].Secure {
			t.Error("cookie must not set Secure — this server is http://localhost by design")
		}
	})

	t.Run("rejects an unknown locale and sets no cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/lang", strings.NewReader("lang=klingon"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.handleSetLang(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Error("an unknown locale must not set a cookie")
		}
	})
}

// lookupMarkerRe matches catalog.lookup's loud miss marker ("!" + key + "!",
// e.g. "!progress.error!") inside rendered template output. Anchored to
// dotted-identifier-shaped content between the bangs so it cannot fire on an
// unrelated "!" a template might legitimately emit.
var lookupMarkerRe = regexp.MustCompile(`![A-Za-z][A-Za-z0-9_.]*!`)

// renderAllLocalesCases returns one fixture per template, built from the
// same view types the real handlers pass to ExecuteTemplate (selfcheck.go,
// crosscheck.go, history.go, thumbnail.go, http.go). Every field a template
// dereferences must be set, or ExecuteTemplate fails at execute time
// (html/template has no static field check against the concrete type).
func renderAllLocalesCases() []struct {
	Name string
	Data any
} {
	resultsView := ResultsView{
		RunID: "r1", Mode: "cross_check", SourcePath: "/p",
		NumGroups: 2, NumFiles: 2, BytesHuman: "2.0 MB",
		ApplyURL: "/api/cross-check/apply", Backups: []string{"/bk"},
		NumWarnings: 1,
		Warnings:    []ResultWarn{{Code: "bad_video", Path: "/p/x.mov"}},
		Groups: []ResultGroup{
			{
				GroupID: 1, MatchReason: "md5", Hash: "abc123", Mode: "cross_check",
				Keep:   ResultFile{Path: "/bk/keep.jpg", Name: "keep.jpg", SizeStr: "1.0 MB"},
				Remove: []ResultFile{{Path: "/p/dup.jpg", Name: "dup.jpg", SizeStr: "1.0 MB"}},
			},
			{
				GroupID: 2, MatchReason: "video_fast", Mode: "cross_check", IsSimilar: true,
				Keep:   ResultFile{Path: "/bk/keep.mp4", Name: "keep.mp4", SizeStr: "10 MB", HasMedia: true, DurationStr: "1:00"},
				Remove: []ResultFile{{Path: "/p/dup.mp4", Name: "dup.mp4", SizeStr: "9.8 MB"}},
			},
		},
	}
	doneView := resultsView
	doneView.MovedCount = 2
	doneView.ManifestPath = "/p/_QUARANTINE/_manifest-r1.tsv"
	doneView.QuarantineDir = "/p/_QUARANTINE"

	thumbView := ResultsView{
		RunID: "r2", Mode: "thumbnail_detect", SourcePath: "/p",
		NumGroups: 2, NumWarnings: 1, ApplyURL: "/api/thumbnails/apply",
		Warnings: []ResultWarn{{Code: "bad_thumb_candidate", Path: "/p/y.jpg"}},
		Groups: []ResultGroup{
			{
				StringGroupID: "l3:deadbeef",
				Members: []ResultMember{
					{Path: "/p/keeper.jpg", Role: "keeper"},
					{Path: "/p/thumb.jpg", Role: "thumbnail", Width: 80, Height: 80},
				},
			},
			{
				StringGroupID: "l1-suspects",
				Members: []ResultMember{
					{Path: "/p/s1.jpg", Reason: "l1_only_thumb", Width: 64, Height: 64},
					{Path: "/p/s2.jpg", Reason: "l1_only_maybe", Width: 64, Height: 64},
					{Path: "/p/s3.jpg", Reason: "l1_phash_match", Width: 64, Height: 64},
				},
			},
		},
	}

	return []struct {
		Name string
		Data any
	}{
		{"app.html", selfCheckFormData{DefaultFolder: "/tmp/g", Recents: []string{"/tmp/g"}}},
		{"selfcheck_form.html", selfCheckFormData{DefaultFolder: "/tmp/g", Recents: []string{"/tmp/g"}}},
		{"selfcheck_running.html", selfCheckRunningData{RunID: "r1", Folder: "/p", Mode: "apply", NextURL: "/x", ShowActions: true}},
		{"selfcheck_results.html", resultsView},
		{"selfcheck_done.html", doneView},
		{"crosscheck_form.html", map[string]any{"Recents": []string{"/tmp/g"}}},
		{"crosscheck_backup_row.html", nil},
		{"thumbnails_form.html", map[string]any{"Recents": []string{"/tmp/g"}}},
		{"thumbnails_results.html", thumbView},
		{"thumbnails_l1_row.html", map[string]any{"Member": ResultMember{Path: "/p/s1.jpg", Reason: "l1_only_thumb"}, "GroupID": "l1-suspects", "Index": 0}},
		{"history_list.html", historyView{Entries: []HistoryEntry{{RunID: "r1", Timestamp: 1, Folder: "/p", Mode: "self_check", MovedCount: 1, Status: "success"}}}},
		{"history_restore.html", historyRestoreData{RunID: "r1", Folder: "/p", ManifestPath: "/p/_QUARANTINE/_manifest-r1.tsv", MovedCount: 1}},
		{"debug.html", debugPageData{TwincutPath: "/usr/bin/twincut"}},
		{"debug_run.html", debugRunPageData{RunID: "r1"}},
		{"dir_listing.html", DirListing{Path: "/p", Parent: "/", Entries: []DirEntry{{Name: "sub", Path: "/p/sub"}}}},
	}
}

// TestRenderAllLocales executes every template in every locale (spec §9:
// "every locale × every template renders"), not just parses the set: the
// pre-fix version only parsed and then re-inspected catalog *values* in
// isolation, so it never actually called ExecuteTemplate against the real
// zh-Hans catalog and could not catch a template that panics or errors at
// real-data execute time, or a "!key!" lookup-miss marker that only appears
// in *rendered output* (e.g. from an under-populated tmap subtree — see
// TestTmapBridgeRequiredSubkeys above for why validateCatalogs' subtree
// check alone can miss that). It intentionally does NOT catch a
// wrong-but-present English string (that is goldenCases()' job in
// golden_test.go, extended in this same fix wave): setting en.json's "badge.exact" to
// "ZZZ-BROKEN" renders fine here — non-empty, not marker-shaped — and is
// instead caught by TestEnglishRenderUnchanged/selfcheck_results.html.
func TestRenderAllLocales(t *testing.T) {
	cats, err := loadCatalogs(os.DirFS(".."))
	if err != nil {
		t.Fatalf("loadCatalogs: %v", err)
	}
	cases := renderAllLocalesCases()
	for _, code := range sortedKeys(cats) {
		cat := cats[code]
		fm := baseFuncMap()
		fm["t"] = cat.lookup
		fm["tmap"] = cat.subtree
		fm["lang"] = func() string { return code }
		fm["locales"] = func() []localeOption { return localeOptionsFrom(cats) }
		tmpl, err := template.New("").Funcs(fm).ParseGlob("../templates/*.html")
		if err != nil {
			t.Fatalf("%s: parse: %v", code, err)
		}
		if tmpl.Lookup("app.html") == nil {
			t.Errorf("%s: app.html missing from the parsed set", code)
		}
		for _, tc := range cases {
			t.Run(code+"/"+tc.Name, func(t *testing.T) {
				var buf bytes.Buffer
				if err := tmpl.ExecuteTemplate(&buf, tc.Name, tc.Data); err != nil {
					t.Fatalf("execute %s/%s: %v", code, tc.Name, err)
				}
				if m := lookupMarkerRe.FindString(buf.String()); m != "" {
					t.Errorf("%s/%s: rendered output contains a lookup-miss marker %q", code, tc.Name, m)
				}
			})
		}
		for _, k := range sortedKeys(cat) {
			if strings.TrimSpace(cat[k]) == "" {
				t.Errorf("%s: key %q is empty", code, k)
			}
			if strings.HasPrefix(cat[k], "!") && strings.HasSuffix(cat[k], "!") {
				t.Errorf("%s: key %q looks like a lookup marker: %q", code, k, cat[k])
			}
		}
	}
}
