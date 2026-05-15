package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirEntry is a minimal listing record for the directory browser.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	IsDir bool  `json:"is_dir"`
	// Hidden is true for dot-files; the UI may hide them by default.
	Hidden bool `json:"hidden"`
}

// DirListing is the JSON / template payload for /fs/list.
type DirListing struct {
	Path    string     `json:"path"`
	Parent  string     `json:"parent,omitempty"` // empty when at an allowlist root
	Entries []DirEntry `json:"entries"`
	// Roots is populated only when Path is empty (initial render).
	// Lists the user's home and any /Volumes/* mount.
	Roots []DirEntry `json:"roots,omitempty"`
}

// allowedRoots returns the set of paths the directory browser is permitted
// to descend from. Currently: $HOME, /Volumes itself, and everything
// directly under /Volumes/.
//
// This is intentionally generous for a single-user local tool — the user
// owns the box. The point is to prevent foot-guns (`/`, `/System`,
// `/private`, `/etc`) rather than enforce a security boundary.
func allowedRoots() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home: %w", err)
	}
	roots := []string{home}
	if _, err := os.Stat("/Volumes"); err == nil {
		roots = append(roots, "/Volumes")
		if entries, err := os.ReadDir("/Volumes"); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					roots = append(roots, filepath.Join("/Volumes", e.Name()))
				}
			}
		}
	}
	return roots, nil
}

// IsAllowedPath reports whether the absolute, canonical path lies under one
// of the allowlist roots. Empty path is treated as "show roots", so it is
// allowed.
func IsAllowedPath(p string) (bool, error) {
	if p == "" {
		return true, nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false, err
	}
	abs = filepath.Clean(abs)
	roots, err := allowedRoots()
	if err != nil {
		return false, err
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return true, nil
		}
	}
	return false, nil
}

// ErrPathDisallowed is returned by ListDir when the requested path is
// outside the allowlist.
var ErrPathDisallowed = errors.New("path is outside the allowlist (must be under $HOME or /Volumes)")

// ListDir produces a DirListing for path. If path is empty, returns the
// allowlist roots instead of a directory's contents. Hidden entries (names
// starting with ".") are returned with Hidden=true so the UI can choose
// to filter them.
func ListDir(path string) (DirListing, error) {
	if path == "" {
		return rootsListing()
	}

	allowed, err := IsAllowedPath(path)
	if err != nil {
		return DirListing{}, err
	}
	if !allowed {
		return DirListing{}, ErrPathDisallowed
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return DirListing{}, err
	}
	abs = filepath.Clean(abs)

	info, err := os.Stat(abs)
	if err != nil {
		return DirListing{}, err
	}
	if !info.IsDir() {
		return DirListing{}, fmt.Errorf("not a directory: %s", abs)
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return DirListing{}, err
	}

	out := DirListing{Path: abs, Entries: make([]DirEntry, 0, len(entries))}
	for _, e := range entries {
		// We only show directories in the picker; the user picks a folder
		// to scan, not individual files.
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		out.Entries = append(out.Entries, DirEntry{
			Name:   name,
			Path:   filepath.Join(abs, name),
			IsDir:  true,
			Hidden: strings.HasPrefix(name, "."),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})

	// Populate Parent only when it is itself allowed (otherwise we'd
	// surface a button that 403s).
	parent := filepath.Dir(abs)
	if parent != abs {
		if ok, _ := IsAllowedPath(parent); ok {
			out.Parent = parent
		}
	}

	return out, nil
}

func rootsListing() (DirListing, error) {
	roots, err := allowedRoots()
	if err != nil {
		return DirListing{}, err
	}
	listing := DirListing{Roots: make([]DirEntry, 0, len(roots))}
	for _, r := range roots {
		listing.Roots = append(listing.Roots, DirEntry{
			Name:  filepath.Base(r),
			Path:  r,
			IsDir: true,
		})
	}
	return listing, nil
}
