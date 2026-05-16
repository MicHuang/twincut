package server

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// selfCheckFormData feeds the initial form template.
type selfCheckFormData struct {
	DefaultFolder string
	Recents       []string
}

// selfCheckRunningData feeds the running-panel template.
type selfCheckRunningData struct {
	RunID   string
	Folder  string
	IsApply bool
}

func (s *Server) handleSelfCheckTab(w http.ResponseWriter, _ *http.Request) {
	data := selfCheckFormData{}
	if recents, err := s.recents.List(); err == nil {
		data.Recents = recents
		if len(recents) > 0 {
			data.DefaultFolder = recents[0]
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_form.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSelfCheckPreview spawns a self-check dry-run and returns the running
// panel. The panel's JS subscribes to /sse/{run_id} and swaps to the results
// fragment when run_end arrives.
func (s *Server) handleSelfCheckPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	folder := strings.TrimSpace(r.FormValue("folder"))
	if folder == "" {
		http.Error(w, "folder is required", http.StatusBadRequest)
		return
	}
	if ok, err := IsAllowedPath(folder); err != nil || !ok {
		http.Error(w, "folder is outside the allowlist (must be under $HOME or /Volumes)", http.StatusForbidden)
		return
	}

	args := []string{"--self-check", folder, "--dry-run"}
	args = appendCommonOptions(args, r)
	if r.FormValue("include_similar_video") == "1" {
		args = append(args, "--include-similar-video")
	}

	run, err := s.runs.Start(StartOptions{Mode: "self_check_preview", Args: args})
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.recents.Add(folder); err != nil {
		log.Printf("recents add: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:   run.ID,
		Folder:  folder,
		IsApply: false,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSelfCheckResults renders the results panel for a finished preview
// run. Reads the run's accumulated events and structures them into groups.
func (s *Server) handleSelfCheckResults(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_results.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSelfCheckApply spawns the actual (non-dry-run) self-check using
// twincut.sh's --apply-list mode, so the user's per-cluster keep/quarantine
// selections are honored verbatim (including swapping which file is the
// keeper). Returns the same running-panel template with IsApply=true.
func (s *Server) handleSelfCheckApply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	folder := strings.TrimSpace(r.FormValue("folder"))
	if folder == "" {
		http.Error(w, "folder is required", http.StatusBadRequest)
		return
	}
	if ok, err := IsAllowedPath(folder); err != nil || !ok {
		http.Error(w, "folder is outside the allowlist", http.StatusForbidden)
		return
	}

	previewID := r.FormValue("preview_run_id")
	if previewID == "" {
		http.Error(w, "preview_run_id is required", http.StatusBadRequest)
		return
	}
	previewRun := s.runs.Get(previewID)
	if previewRun == nil {
		http.Error(w, "preview run not found", http.StatusNotFound)
		return
	}
	view, err := BuildResults(previewRun)
	if err != nil {
		http.Error(w, "build preview: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows := composeApplyList(view.Groups, r.Form)

	listPath, err := writeApplyList(s.opts.StateDir, rows)
	if err != nil {
		http.Error(w, "write apply-list: "+err.Error(), http.StatusInternalServerError)
		return
	}

	args := []string{"--self-check", folder, "--assume-yes", "--apply-list", listPath}
	args = appendCommonOptions(args, r)

	run, err := s.runs.Start(StartOptions{Mode: "self_check_apply", Args: args})
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:   run.ID,
		Folder:  folder,
		IsApply: true,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// composeApplyList walks the preview's groups and the form's selections to
// produce the rows that twincut --apply-list will execute. Each row:
//
//	move_path \t keep_path \t group_id \t match_reason \t hash
//
// Form contract:
//   - "quarantine" values list every path the user wants moved.
//   - "keep_<group_id>" identifies the user-chosen keeper per cluster
//     (defaults to the preview's keeper when absent).
//
// Selections are validated against each cluster's known paths so a malicious
// or stale form can't cause moves outside the preview's scope.
func composeApplyList(groups []ResultGroup, form url.Values) [][]string {
	wanted := map[string]bool{}
	for _, p := range form["quarantine"] {
		wanted[p] = true
	}
	var rows [][]string
	for _, g := range groups {
		clusterOrder := []string{g.Keep.Path}
		clusterSet := map[string]bool{g.Keep.Path: true}
		for _, rm := range g.Remove {
			clusterOrder = append(clusterOrder, rm.Path)
			clusterSet[rm.Path] = true
		}

		chosenKeep := form.Get("keep_" + strconv.Itoa(g.GroupID))
		if !clusterSet[chosenKeep] {
			chosenKeep = g.Keep.Path
		}

		for _, path := range clusterOrder {
			if path == chosenKeep {
				continue
			}
			if !wanted[path] {
				continue
			}
			rows = append(rows, []string{
				path,
				chosenKeep,
				strconv.Itoa(g.GroupID),
				g.MatchReason,
				g.Hash,
			})
		}
	}
	return rows
}

// writeApplyList serializes rows to a stable TSV file under
// <stateDir>/applylists/. Returns the absolute path. Each row's columns are
// already absolute paths and short identifiers — no escaping required for
// TSV (twincut splits on TAB and tolerates anything else inside a column).
func writeApplyList(stateDir string, rows [][]string) (string, error) {
	dir := filepath.Join(stateDir, "applylists")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "apply-*.tsv")
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, row := range rows {
		if _, err := fmt.Fprintln(f, strings.Join(row, "\t")); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

// handleSelfCheckDone renders the post-apply summary page.
func (s *Server) handleSelfCheckDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_done.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// appendCommonOptions reads the optional --algo / --min-size / --ext form
// values and appends them to args.
func appendCommonOptions(args []string, r *http.Request) []string {
	if v := strings.TrimSpace(r.FormValue("algo")); v != "" && v != "md5" {
		args = append(args, "--algo", v)
	}
	if v := strings.TrimSpace(r.FormValue("min_size")); v != "" && v != "0k" {
		args = append(args, "--min-size", v)
	}
	if v := strings.TrimSpace(r.FormValue("ext")); v != "" {
		args = append(args, "--ext", v)
	}
	if v := strings.TrimSpace(r.FormValue("size_pct")); v != "" {
		args = append(args, "--size-pct", v)
	}
	return args
}

// handleFsList serves the directory browser.
func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	listing, err := ListDir(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Empty path → show the allowlist roots.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "dir_listing.html", listing); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleTabPlaceholder serves a "coming soon" panel for tabs not yet built.
func (s *Server) handleTabPlaceholder(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<section class="tab-section">
  <header class="tab-section-header"><h2>%s</h2><p class="subtitle">Coming in a later stage.</p></header>
</section>`, name)
	}
}
