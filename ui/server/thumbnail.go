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
		args = append(args, "--max-edge", v)
	}
	if v := strings.TrimSpace(r.FormValue("maybe_max_edge")); v != "" {
		args = append(args, "--maybe-max-edge", v)
	}
	if r.FormValue("require_exif_match") == "on" {
		args = append(args, "--require-exif-match")
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
	// Derive source path from the preview run's args — not from the submitted
	// form — to prevent pairing a benign preview with malicious path overrides.
	previewArgs := previewRun.Snapshot().Args
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

	csvData, err := composeThumbnailConfirmCSV(view.Groups, r.Form)
	if err != nil {
		http.Error(w, "compose confirm CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write CSV before starting the run so we have a stable file path.
	// Generate an ID, write the CSV, then start the run. If the run gets
	// a different ID, rename the CSV to match run.ID so the name stays
	// consistent for downstream tooling.
	applyRunID := newRunID()
	runsDir := filepath.Join(s.opts.StateDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		http.Error(w, "mkdir runs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	csvPath := filepath.Join(runsDir, applyRunID+".thumb-confirm.csv")
	if err := os.WriteFile(csvPath, csvData, 0o644); err != nil {
		http.Error(w, "write confirm CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}

	args := []string{"--thumb-confirm", csvPath, "--assume-yes", "--json-events"}
	run, err := s.runs.Start(StartOptions{Mode: "thumbnail_detect_apply", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// If the run manager assigned a different ID, rename the CSV to match.
	if run.ID != applyRunID {
		newCSVPath := filepath.Join(runsDir, run.ID+".thumb-confirm.csv")
		if renameErr := os.Rename(csvPath, newCSVPath); renameErr != nil {
			// Non-fatal: log but continue — CSV still exists under applyRunID name.
			_ = renameErr
		}
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
