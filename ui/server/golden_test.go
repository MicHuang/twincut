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

// goldenCases pins templates whose data is cheap to construct. Results and
// running panels are already covered by the handler tests; this file exists to
// prove the Task 3 refactor is inert, not to re-test rendering.
func goldenCases() []struct {
	Name string
	Data any
} {
	return []struct {
		Name string
		Data any
	}{
		{"app.html", selfCheckFormData{DefaultFolder: "/tmp/g", Recents: []string{"/tmp/g", "/tmp/h"}}},
		{"selfcheck_form.html", selfCheckFormData{DefaultFolder: "/tmp/g", Recents: []string{"/tmp/g", "/tmp/h"}}},
		{"crosscheck_form.html", map[string]any{"Recents": []string{"/tmp/g"}}},
		{"thumbnails_form.html", map[string]any{"Recents": []string{"/tmp/g"}}},
		{"history_list.html", historyView{Entries: nil}},
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
	tmpl, err := template.New("").Funcs(fm).ParseGlob("../templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return tmpl
}

// renderFor is the seam this task pivots on. Before the refactor it parses the
// templates directly — equivalent to the old s.tmpl, same files, same FuncMap.
// Step 7 replaces its body with the new path, and the goldens then prove the
// two produce identical bytes.
func renderFor(t *testing.T, s *Server, r *http.Request) *template.Template {
	t.Helper()
	return s.tmplFor(r)
}

func TestEnglishRenderUnchanged(t *testing.T) {
	srv := &Server{opts: Options{StateDir: t.TempDir(), TwincutPath: "/dev/null"}}
	srv.tmpls = map[string]*template.Template{defaultLocale: newRealLocaleTemplates(t, defaultLocale)}

	for _, tc := range goldenCases() {
		t.Run(tc.Name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			var buf bytes.Buffer
			// Step 3 renders through renderFor(t, srv, r); Step 7 swaps its body
			// to srv.tmplFor(r). That swap is what the goldens prove is inert.
			if err := renderFor(t, srv, r).ExecuteTemplate(&buf, tc.Name, tc.Data); err != nil {
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
