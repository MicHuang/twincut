// Package server — cross-check Web UI tab. Mirrors selfcheck.go shape.
//
// User flow: form (source + 1+ backups) → preview (dry-run) → results
// (asymmetric cluster cards with [SOURCE] checkboxes and [BACKUP · keep]
// read-only rows) → apply → done. Apply runs join History (stage 6) for
// later Restore via the same code path self-check apply runs use.
//
// Reuses generic infrastructure: run manager (runs.go), SSE (sse.go),
// events parser (events.go), results builder (results.go) — the only
// cross-check-specific code lives here and in the cross-check templates.
package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// parseCrossCheckForm extracts the source path and the non-empty backup
// paths from a submitted cross-check form. Whitespace-only entries are
// dropped. Returns error if source is empty or no non-empty backup
// remains; both are required.
func parseCrossCheckForm(form map[string][]string) (string, []string, error) {
	source := ""
	if v, ok := form["source"]; ok && len(v) > 0 {
		source = strings.TrimSpace(v[0])
	}
	if source == "" {
		return "", nil, errors.New("source folder is required")
	}
	var backups []string
	for _, b := range form["backup"] {
		t := strings.TrimSpace(b)
		if t == "" {
			continue
		}
		backups = append(backups, t)
	}
	if len(backups) == 0 {
		return "", nil, errors.New("at least one backup folder is required")
	}
	return source, backups, nil
}

func (s *Server) handleCrossCheckTab(w http.ResponseWriter, r *http.Request) {
	recents, err := s.recents.List()
	if err != nil {
		http.Error(w, "list recents: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "crosscheck_form.html", map[string]any{
		"Recents": recents,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckAddBackupRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "crosscheck_backup_row.html", nil); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	source, backups, err := parseCrossCheckForm(r.Form)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist (must be under $HOME or /Volumes)", http.StatusForbidden)
		return
	}
	for _, b := range backups {
		if ok, err := IsAllowedPath(b); err != nil || !ok {
			http.Error(w, fmt.Sprintf("backup %q is outside the allowlist", b), http.StatusForbidden)
			return
		}
	}

	args := []string{"--source", source}
	for _, b := range backups {
		args = append(args, "--backup", b)
	}
	args = append(args, "--dry-run")
	args = appendCrossCheckOptions(args, r)

	run, err := s.runs.Start(StartOptions{Mode: "cross_check_preview", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.recents.Add(source)
	for _, b := range backups {
		_ = s.recents.Add(b)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "cross_check_preview",
		NextURL:     "/api/cross-check/results/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckApply(w http.ResponseWriter, r *http.Request) {
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
	if prevSnap.Mode != "cross_check_preview" {
		http.Error(w, "preview_run_id refers to a non-cross-check-preview run", http.StatusUnprocessableEntity)
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
	// Derive source + backups from the preview run's args, not the
	// submitted form. Trusting the form would let an attacker pair a
	// benign preview run with malicious source/backup paths and apply
	// the preview's selections under a different tree.
	previewArgs := prevSnap.Args
	source, ok := extractArgValue(previewArgs, "--source")
	if !ok || source == "" {
		http.Error(w, "preview run is missing --source arg", http.StatusInternalServerError)
		return
	}
	backups := extractArgValues(previewArgs, "--backup")
	if len(backups) == 0 {
		http.Error(w, "preview run is missing --backup args", http.StatusInternalServerError)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist", http.StatusForbidden)
		return
	}
	for _, b := range backups {
		if ok, err := IsAllowedPath(b); err != nil || !ok {
			http.Error(w, fmt.Sprintf("backup %q is outside the allowlist", b), http.StatusForbidden)
			return
		}
	}
	view, err := BuildResults(previewRun)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows := composeApplyList(view.Groups, r.Form, "cross_check")
	listPath, err := writeApplyList(s.opts.StateDir, rows)
	if err != nil {
		http.Error(w, "write apply-list: "+err.Error(), http.StatusInternalServerError)
		return
	}

	args := []string{"--source", source}
	for _, b := range backups {
		args = append(args, "--backup", b)
	}
	args = append(args,
		"--quarantine", source+"/_QUARANTINE",
		"--assume-yes",
		"--apply-list", listPath,
	)

	run, err := s.runs.Start(StartOptions{Mode: "cross_check_apply", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "cross_check_apply",
		NextURL:     "/api/cross-check/done/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckResults(w http.ResponseWriter, r *http.Request) {
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
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_results.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckDone(w http.ResponseWriter, r *http.Request) {
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
	view.Mode = "cross_check"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_done.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

// appendCrossCheckOptions reads the form's advanced flags and appends the
// corresponding CLI args. Mirrors appendCommonOptions in selfcheck.go but
// scoped to cross-check's slightly different option set.
func appendCrossCheckOptions(args []string, r *http.Request) []string {
	switch strings.ToLower(strings.TrimSpace(r.FormValue("matching_mode"))) {
	case "exact":
		args = append(args, "--exact")
	case "strict":
		args = append(args, "--video-fast-strict")
	}
	if v := strings.TrimSpace(r.FormValue("min_size")); v != "" {
		args = append(args, "--min-size", v)
	}
	if v := strings.TrimSpace(r.FormValue("ext")); v != "" {
		args = append(args, "--ext", v)
	}
	if v := strings.TrimSpace(r.FormValue("quarantine")); v != "" {
		args = append(args, "--quarantine", v)
	}
	return args
}
