// Package server — thumbnail-detect Web UI tab. Mirrors crosscheck.go shape.
//
// User flow: form (source + thresholds) → preview (dry-run) → results
// (cluster cards for L2/L3 + collapsible L1 suspects table) → apply
// (writes enhanced 6-column CSV + launches --thumb-confirm) → done.
// Apply runs join History for later Restore via the existing stage-6 wiring.
package server

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
)

func (s *Server) handleThumbnailsTab(w http.ResponseWriter, r *http.Request) {
	recents, err := s.recents.List()
	if err != nil {
		http.Error(w, "list recents: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "thumbnails_form.html", map[string]any{
		"Recents": recents,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	source := strings.TrimSpace(r.FormValue("source"))
	if source == "" {
		http.Error(w, "source folder is required", http.StatusUnprocessableEntity)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist (must be under $HOME or /Volumes)", http.StatusForbidden)
		return
	}

	args := []string{"--thumbnail-detect", "--source", source, "--dry-run"}
	if v := strings.TrimSpace(r.FormValue("max_edge")); v != "" {
		args = append(args, "--thumb-max-edge", v)
	}
	if v := strings.TrimSpace(r.FormValue("maybe_max_edge")); v != "" {
		args = append(args, "--thumb-maybe-max-edge", v)
	}
	if r.FormValue("require_exif_match") == "on" {
		args = append(args, "--thumb-require-exif-match")
	}

	run, err := s.runs.Start(StartOptions{Mode: "thumbnail_detect_preview", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.recents.Add(source)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "thumbnail_detect_preview",
		NextURL:     "/api/thumbnails/results/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsResults(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "run not found: "+id, http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "thumbnails_results.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsApply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	previewID := r.FormValue("preview_run_id")
	if previewID == "" {
		http.Error(w, "missing preview_run_id", http.StatusBadRequest)
		return
	}
	previewRun := s.runs.Get(previewID)
	if previewRun == nil {
		http.Error(w, "preview run not found: "+previewID, http.StatusNotFound)
		return
	}

	prevSnap := previewRun.Snapshot()
	if prevSnap.Mode != "thumbnail_detect_preview" {
		http.Error(w, "preview_run_id refers to a non-thumbnail-preview run (mode="+prevSnap.Mode+")", http.StatusUnprocessableEntity)
		return
	}
	if prevSnap.Status == RunStatusRunning {
		http.Error(w, "preview run is still in progress; wait for it to finish before applying", http.StatusConflict)
		return
	}
	if prevSnap.Status != RunStatusSucceeded {
		http.Error(w, "preview run did not succeed (status="+string(prevSnap.Status)+"); cannot apply", http.StatusUnprocessableEntity)
		return
	}

	// Derive source path from the preview run's args — not from the submitted
	// form — to prevent pairing a benign preview with malicious path overrides.
	previewArgs := prevSnap.Args
	source, ok := extractArgValue(previewArgs, "--source")
	if !ok || source == "" {
		http.Error(w, "preview run is missing --source arg", http.StatusInternalServerError)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist", http.StatusForbidden)
		return
	}

	view, err := BuildResults(previewRun)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}

	dstDir := filepath.Join(source, "_QUARANTINE", "_thumbs")
	cmds := composeApplyCommands(view.Groups, dstDir)

	applyRunID := newRunID()
	args := []string{
		"--thumbnail-detect-apply",
		"--json-events",
		"--json-in",
		"--source", source,
	}
	run, err := s.runs.Start(StartOptions{
		ID:    applyRunID,
		Mode:  "thumbnail_detect_apply",
		Args:  args,
		Stdin: bytes.NewReader(cmds),
	})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "thumbnail_detect_apply",
		NextURL:     "/api/thumbnails/done/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleThumbnailsDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "run not found: "+id, http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view.Mode = "thumbnail_detect"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_done.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}
