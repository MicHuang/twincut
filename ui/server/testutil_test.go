package server

import (
	"html/template"
	"os"
	"testing"
)

// SetTestSpawnHook sets SpawnHook on the manager and activates the env-var
// guard. All tests that call rm.Start() must go through this helper; the
// guard in Start() panics if GO_TEST_RUNNING=1 and SpawnHook is nil.
func SetTestSpawnHook(t *testing.T, rm *RunManager, fn func(StartOptions)) {
	t.Helper()
	t.Setenv("GO_TEST_RUNNING", "1")
	rm.SpawnHook = fn
}

func TestSpawnGuardPanicsWithoutHook(t *testing.T) {
	t.Setenv("GO_TEST_RUNNING", "1")
	dir := t.TempDir()
	rm, err := NewRunManager(dir, "/nonexistent/twincut.sh")
	if err != nil {
		t.Fatal(err)
	}
	// SpawnHook intentionally NOT set — should panic.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from spawn guard, got none")
		}
		msg, ok := r.(string)
		if !ok || len(msg) == 0 {
			t.Fatalf("expected string panic message, got %T: %v", r, r)
		}
	}()
	_, _ = rm.Start(StartOptions{Mode: "test"})
}

// newTestTemplates parses the on-disk templates with the production FuncMap
// plus locale-independent stubs for t/tmap/lang. Three test files used to
// duplicate this; they must not, because the FuncMap now grows.
func newTestTemplates(t *testing.T) *template.Template {
	t.Helper()
	fm := baseFuncMap()
	fm["t"] = func(k string) string { return k }
	fm["tmap"] = func(prefix string) map[string]string { return map[string]string{} }
	fm["lang"] = func() string { return defaultLocale }
	tmpl, err := template.New("").Funcs(fm).ParseGlob("../templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return tmpl
}

// mustLoadTestCatalogs loads the real on-disk locale catalogs so handler
// tests exercising httpErrorT see genuine translated text instead of the
// catalog "!key!" lookup-miss marker. Mirrors newTestTemplates' real-template
// loading; unlike newTestTemplates' key-echo funcmap stub, there is no
// analogous stub for httpErrorT since it looks up s.cats directly rather
// than through a FuncMap.
func mustLoadTestCatalogs(t *testing.T) map[string]catalog {
	t.Helper()
	cats, err := loadCatalogs(os.DirFS(".."))
	if err != nil {
		t.Fatalf("load catalogs: %v", err)
	}
	return cats
}
