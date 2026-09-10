package main

import (
	"slices"
	"testing"

	"github.com/MicHuang/twincut/ui/server"
)

// TestSupportedLocalesMatchesEmbeddedSet pins the //go:embed directive above
// (templates/*.html static/* locales/*.json): it is the only thing in the
// tree that determines which locale catalogs actually ship in the binary.
// Nothing else in the test suite reads the embedded `assets` var (the server
// package's tests read catalogs off the source tree via os.DirFS("..")), so
// narrowing the directive to embed a single locale file — English-only,
// with a dead 中文 option left in app.html's switcher — would otherwise ship
// with every other test green.
func TestSupportedLocalesMatchesEmbeddedSet(t *testing.T) {
	got, err := server.SupportedLocales(assets)
	if err != nil {
		t.Fatalf("SupportedLocales(assets): %v", err)
	}
	want := []string{"en", "zh-Hans"}
	if !slices.Equal(got, want) {
		t.Errorf("SupportedLocales(assets) = %v, want %v — check the locales/*.json glob in the //go:embed directive", got, want)
	}
}
