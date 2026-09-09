package server

import (
	"net/http"
	"net/http/httptest"
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
