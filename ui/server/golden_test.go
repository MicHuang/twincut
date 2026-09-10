package server

import (
	"bytes"
	"flag"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden/*.html")

// goldenCases pins the rendered English bytes for templates with cheap-to-
// construct data. selfcheck_results.html, selfcheck_running.html,
// selfcheck_done.html, and thumbnails_results.html were added 2026-09-10
// because handler-test coverage alone let English copy drift silently
// (mutation-tested: setting en.json's "badge.exact" to a broken value left
// the whole suite green) — this file is the actual English regression net
// for those surfaces, not just proof the Task 3 refactor is inert.
func goldenCases() []struct {
	Name string
	Data any
} {
	runningData := selfCheckRunningData{
		RunID: "g1", Folder: "/tmp/g", Mode: "apply",
		NextURL: "/api/self-check/done/g1", ShowActions: true,
	}

	resultsView := ResultsView{
		RunID: "g1", Mode: "self_check", SourcePath: "/tmp/g",
		NumGroups: 2, NumFiles: 2, BytesHuman: "2.0 MB",
		ApplyURL: "/api/self-check/apply", NumWarnings: 1,
		Warnings: []ResultWarn{{Code: "bad_video", Path: "/tmp/g/broken.mov"}},
		Groups: []ResultGroup{
			{
				GroupID: 1, MatchReason: "md5", Hash: "deadbeef", Mode: "self_check",
				Keep:   ResultFile{Path: "/tmp/g/keep.jpg", Name: "keep.jpg", SizeStr: "1.0 MB"},
				Remove: []ResultFile{{Path: "/tmp/g/dup.jpg", Name: "dup.jpg", SizeStr: "1.0 MB"}},
			},
			{
				GroupID: 2, MatchReason: "video_strict", Mode: "self_check", IsSimilar: true,
				Keep: ResultFile{
					Path: "/tmp/g/keep.mp4", Name: "keep.mp4", SizeStr: "10.0 MB",
					HasMedia: true, DurationStr: "1:00", DimensionsStr: "1920x1080",
				},
				Remove: []ResultFile{{
					Path: "/tmp/g/dup.mp4", Name: "dup.mp4", SizeStr: "9.5 MB",
					HasMedia: true, DurationStr: "1:00",
				}},
			},
		},
	}

	// selfcheck_done.html doesn't render .Groups at all (it's a summary
	// page), but reusing resultsView keeps the fixture set small and this
	// still exercises the post-apply-specific fields.
	doneView := resultsView
	doneView.MovedCount = 2
	doneView.ManifestPath = "/tmp/g/_QUARANTINE/_manifest-g1.tsv"
	doneView.QuarantineDir = "/tmp/g/_QUARANTINE"

	thumbView := ResultsView{
		RunID: "g2", Mode: "thumbnail_detect", SourcePath: "/tmp/g",
		NumGroups: 2, NumWarnings: 1, ApplyURL: "/api/thumbnails/apply",
		Warnings: []ResultWarn{{Code: "bad_thumb_candidate", Path: "/tmp/g/broken.jpg"}},
		Groups: []ResultGroup{
			{
				StringGroupID: "l3:cafef00d",
				Members: []ResultMember{
					{Path: "/tmp/g/keeper.jpg", Role: "keeper"},
					{Path: "/tmp/g/thumb.jpg", Role: "thumbnail", Width: 80, Height: 80},
				},
			},
			{
				StringGroupID: "l1-suspects",
				Members: []ResultMember{
					{Path: "/tmp/g/s1.jpg", Reason: "l1_only_thumb", Width: 64, Height: 64},
					{Path: "/tmp/g/s2.jpg", Reason: "l1_only_maybe", Width: 64, Height: 64},
					{Path: "/tmp/g/s3.jpg", Reason: "l1_phash_match", Width: 64, Height: 64},
				},
			},
		},
	}

	return []struct {
		Name string
		Data any
	}{
		{"app.html", selfCheckFormData{DefaultFolder: "/tmp/g", Recents: []string{"/tmp/g", "/tmp/h"}}},
		{"selfcheck_form.html", selfCheckFormData{DefaultFolder: "/tmp/g", Recents: []string{"/tmp/g", "/tmp/h"}}},
		{"crosscheck_form.html", map[string]any{"Recents": []string{"/tmp/g"}}},
		{"thumbnails_form.html", map[string]any{"Recents": []string{"/tmp/g"}}},
		{"history_list.html", historyView{Entries: nil}},
		{"selfcheck_running.html", runningData},
		{"selfcheck_results.html", resultsView},
		{"selfcheck_done.html", doneView},
		{"thumbnails_results.html", thumbView},
	}
}

func goldenPath(name string) string { return filepath.Join("testdata", "golden", name) }

// newRealLocaleTemplates parses the on-disk templates against the real
// on-disk catalog for code (not newTestTemplates's key-echo stub). The golden
// test must hold real English catalog values so a later task's {{t}} calls
// and populated catalogs still get checked for byte-identical output — a
// key-echo stub would make the goldens hold catalog keys instead of English.
func newRealLocaleTemplates(t *testing.T, code string) *template.Template {
	t.Helper()
	cats, err := loadCatalogs(os.DirFS(".."))
	if err != nil {
		t.Fatalf("loadCatalogs: %v", err)
	}
	cat, ok := cats[code]
	if !ok {
		t.Fatalf("no %q catalog", code)
	}
	fm := baseFuncMap()
	fm["t"] = cat.lookup
	fm["tmap"] = cat.subtree
	fm["lang"] = func() string { return code }
	fm["locales"] = func() []localeOption { return localeOptionsFrom(cats) }
	tmpl, err := template.New("").Funcs(fm).ParseGlob("../templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return tmpl
}

func TestEnglishRenderUnchanged(t *testing.T) {
	srv := &Server{opts: Options{StateDir: t.TempDir(), TwincutPath: "/dev/null"}}
	srv.tmpls = map[string]*template.Template{defaultLocale: newRealLocaleTemplates(t, defaultLocale)}

	for _, tc := range goldenCases() {
		t.Run(tc.Name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			var buf bytes.Buffer
			if err := srv.tmplFor(r).ExecuteTemplate(&buf, tc.Name, tc.Data); err != nil {
				t.Fatalf("render %s: %v", tc.Name, err)
			}
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath(tc.Name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath(tc.Name), buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote golden %s (%d bytes)", tc.Name, buf.Len())
				return
			}
			want, err := os.ReadFile(goldenPath(tc.Name))
			if err != nil {
				t.Fatalf("read golden (run with -update-golden first): %v", err)
			}
			if !bytes.Equal(want, buf.Bytes()) {
				t.Errorf("%s render changed; the Task 3 refactor must be inert", tc.Name)
			}
		})
	}
}
