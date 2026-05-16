package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenInOS asks the host OS to reveal the given path in the user's file
// manager. macOS → `open` (Finder), Linux → `xdg-open`, Windows → `explorer`.
// The path must already be allowlist-checked by the caller; this function
// only executes the spawn.
func OpenInOS(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", cmd.Path, err)
	}
	// We intentionally don't Wait — `open` returns immediately on macOS,
	// but `xdg-open` may keep running until the launched app exits.
	return nil
}

// handleOpenPath validates the requested path against the directory-browser
// allowlist and then asks the OS to reveal it. Used by the "Open quarantine
// folder" button on the post-apply done page.
//
// Body: form-encoded `path=<absolute path>`. Replies 200 with a tiny HTMX
// fragment on success so the button click can leave a confirmation in
// place; 4xx on validation failure.
func (s *Server) handleOpenPath(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	raw := r.FormValue("path")
	if raw == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	abs = filepath.Clean(abs)

	allowed, err := IsAllowedPath(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "path is outside the allowlist", http.StatusForbidden)
		return
	}

	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := OpenInOS(abs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// HTMX consumes the response body as the swap content — return a tiny
	// fragment so the button can be replaced with a confirmation.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<span class="muted small">Opened in Finder</span>`))
}
