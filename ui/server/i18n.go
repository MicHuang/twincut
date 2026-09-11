package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
)

// defaultLocale is both the fallback and the reference catalog: every other
// locale must define exactly its key set.
const defaultLocale = "en"

// catalog is a flat translation table, key -> translated string.
type catalog map[string]string

// lookup returns the translation for k. validateCatalogs makes a miss
// unreachable in a shipped binary; if one somehow occurs it returns a loud
// marker rather than an empty string, so the defect is visible on the page
// instead of silently erasing UI text.
func (c catalog) lookup(k string) string {
	if v, ok := c[k]; ok {
		return v
	}
	return "!" + k + "!"
}

// subtree returns every entry whose key starts with prefix, with the prefix
// stripped. Keys stay flat: subtree("progress.") over "progress.phase.scan"
// yields "phase.scan", not a nested map.
func (c catalog) subtree(prefix string) map[string]string {
	out := make(map[string]string)
	for k, v := range c {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}

// loadCatalogs reads every locales/*.json from fsys. The locale code is the
// file's base name without the extension.
func loadCatalogs(fsys fs.FS) (map[string]catalog, error) {
	entries, err := fs.Glob(fsys, "locales/*.json")
	if err != nil {
		return nil, fmt.Errorf("glob locales: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no locale catalogs found under locales/")
	}
	cats := make(map[string]catalog, len(entries))
	for _, name := range entries {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var c catalog
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		cats[strings.TrimSuffix(path.Base(name), ".json")] = c
	}
	return cats, nil
}

// validateCatalogs enforces the invariants that let a missing translation
// fail the build instead of reaching a user: every locale defines exactly the
// default locale's key set; every exact key in used ({{t "..."}} call sites)
// is defined everywhere; and every tmap prefix in used ({{tmap "..."}} call
// sites — recognisable because, unlike a t key, they end in ".") has a
// non-empty subtree in every catalog. The last check is what stops a tmap
// bridge from silently serving an empty {} object to the browser. It is
// deliberately not an exact-membership check: that would instead demand a
// literal catalog key spelled the same as the prefix (e.g. "progress."
// itself), which is never displayed anywhere and would only exist to satisfy
// the checker. Iteration is sorted so the reported key is deterministic.
func validateCatalogs(cats map[string]catalog, used []string) error {
	ref, ok := cats[defaultLocale]
	if !ok {
		return fmt.Errorf("no %q catalog", defaultLocale)
	}
	for _, code := range sortedKeys(cats) {
		if code == defaultLocale {
			continue
		}
		c := cats[code]
		for _, k := range sortedKeys(ref) {
			if _, ok := c[k]; !ok {
				return fmt.Errorf("catalog %s: missing key %q defined in %s", code, k, defaultLocale)
			}
		}
		for _, k := range sortedKeys(c) {
			if _, ok := ref[k]; !ok {
				return fmt.Errorf("catalog %s: key %q is not defined in %s", code, k, defaultLocale)
			}
		}
	}
	sortedUsed := append([]string(nil), used...)
	sort.Strings(sortedUsed)
	for _, k := range sortedUsed {
		if strings.HasSuffix(k, ".") {
			// A tmap prefix is covered by subtree, not by exact membership:
			// what the template actually consumes is every key beneath the
			// prefix, not a literal key spelled the same as the prefix.
			for _, code := range sortedKeys(cats) {
				if len(cats[code].subtree(k)) == 0 {
					return fmt.Errorf("catalog %s: tmap prefix %q is used but its subtree is empty", code, k)
				}
			}
			continue
		}
		for _, code := range sortedKeys(cats) {
			if _, ok := cats[code][k]; !ok {
				return fmt.Errorf("catalog %s: key %q is used but not defined", code, k)
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// langCookie is set by the header switcher and outranks the --lang flag: the
// flag overrides detection, not a person's explicit choice.
const langCookie = "lang"

// resolveLocale picks a locale from, in order: the lang cookie, the --lang
// flag, Accept-Language, then defaultLocale. It is generic over the map value
// so the same function serves a catalog map and a template map.
//
// The cookie is untrusted input: an unrecognised value falls through rather
// than erroring.
func resolveLocale[V any](r *http.Request, flagLang string, avail map[string]V) string {
	if c, err := r.Cookie(langCookie); err == nil {
		if _, ok := avail[c.Value]; ok {
			return c.Value
		}
	}
	if _, ok := avail[flagLang]; ok {
		return flagLang
	}
	if first := firstAcceptLanguageTag(r.Header.Get("Accept-Language")); first != "" {
		if strings.HasPrefix(strings.ToLower(first), "zh") {
			if _, ok := avail["zh-Hans"]; ok {
				return "zh-Hans"
			}
		}
	}
	return defaultLocale
}

// firstAcceptLanguageTag returns the highest-priority tag from an
// Accept-Language header. Two locales do not justify RFC 4647 matching.
func firstAcceptLanguageTag(header string) string {
	if i := strings.IndexAny(header, ",;"); i >= 0 {
		header = header[:i]
	}
	return strings.TrimSpace(header)
}

// SupportedLocales lists the locale codes baked into fsys, sorted. Exported so
// main can validate --lang before the server starts.
func SupportedLocales(fsys fs.FS) ([]string, error) {
	cats, err := loadCatalogs(fsys)
	if err != nil {
		return nil, err
	}
	return sortedKeys(cats), nil
}

// localeCatalogKey marks a catalog key that is looked up from Go code
// outside a template's {{t}}/{{tmap}} call and outside httpErrorT — today
// that is only localeNameKey below. It is a plain identity function so the
// test-only coverage scanner in i18n_test.go (goKeyRe) can find the literal
// key the same way it already finds httpErrorT's key argument, keeping this
// consumer visible to TestCatalogsCoverEveryUsedKey and
// TestCatalogsHaveNoUnusedKeys instead of becoming another invisible
// tmap-bridge-shaped hole (see the tmap subtree check above).
func localeCatalogKey(k string) string { return k }

// localeNameKey is the key each locale's own catalog defines for its
// display name in the language switcher, e.g. cats["zh-Hans"].lookup(localeNameKey)
// == "中文" regardless of the viewer's current locale — which is exactly why
// this cannot be expressed as an ordinary {{t "locale.name"}} template call
// (that would only ever resolve in the CURRENT locale's catalog).
var localeNameKey = localeCatalogKey("locale.name")

// localeOption is one entry in the language switcher.
type localeOption struct {
	Code string
	Name string
}

// localeOptionsFrom returns every locale's code and self-described display
// name, sorted by code. The result is identical no matter which locale's
// template set asks for it — the switcher must list every locale regardless
// of which one is currently rendering — so New() computes it once from the
// full catalog map and shares it across every per-locale FuncMap.
func localeOptionsFrom(cats map[string]catalog) []localeOption {
	codes := sortedKeys(cats)
	opts := make([]localeOption, 0, len(codes))
	for _, code := range codes {
		opts = append(opts, localeOption{Code: code, Name: cats[code].lookup(localeNameKey)})
	}
	return opts
}
