// Package server — thumbnail-detect Web UI tab. Mirrors crosscheck.go shape.
//
// User flow: form (source + thresholds) → preview (dry-run) → results
// (cluster cards for L2/L3 + collapsible L1 suspects table) → apply
// (writes enhanced 6-column CSV + launches --thumb-confirm) → done.
// Apply runs join History for later Restore via the existing stage-6 wiring.
package server

import (
	"net/http"
	"os"
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

	args := []string{"--thumbnail-detect", "--source", source, "--dry-run", "--json-events"}
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

	tsvData, err := composeThumbnailConfirmTSV(view.Groups, r.Form)
	if err != nil {
		http.Error(w, "compose confirm TSV: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Pre-generate the run ID so we can write the TSV at a stable path and
	// pass that same ID to RunManager.Start. This avoids the TOCTOU race
	// the previous (Start-generates-ID-then-rename) approach exposed.
	applyRunID := newRunID()
	runsDir := filepath.Join(s.opts.StateDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		http.Error(w, "mkdir runs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tsvPath := filepath.Join(runsDir, applyRunID+".thumb-confirm.tsv")
	if err := os.WriteFile(tsvPath, tsvData, 0o644); err != nil {
		http.Error(w, "write confirm TSV: "+err.Error(), http.StatusInternalServerError)
		return
	}

	thumbDir := filepath.Join(source, "_thumbnails")
	args := []string{"--thumb-confirm", tsvPath, "--assume-yes", "--json-events", "--thumb-dir", thumbDir, "--source", source}
	run, err := s.runs.Start(StartOptions{ID: applyRunID, Mode: "thumbnail_detect_apply", Args: args})
	if err != nil {
		_ = os.Remove(tsvPath)  // best-effort cleanup; TSV is orphaned if Start fails
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
