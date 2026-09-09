package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
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

// validateCatalogs enforces the two invariants that let a missing translation
// fail the build instead of reaching a user: every locale defines exactly the
// default locale's key set, and every key in used is defined everywhere.
// Iteration is sorted so the reported key is deterministic.
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
