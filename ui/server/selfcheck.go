package server

import (
	"fmt"
	"log"
	"net/http"
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

// handleSelfCheckApply spawns the actual (non-dry-run) self-check, passing
// --exclude-path for any files the user unchecked in the results screen.
// Returns the same running-panel template, with IsApply=true so the JS
// hand-off targets the done page.
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

	// All files the user wants quarantined arrive as quarantine=PATH form
	// values. Anything in the original group set that is NOT in this list
	// must be excluded. The results template defaults to checking every
	// file, so the typical case sends the full set; unchecks become
	// exclusions.
	wanted := map[string]bool{}
	for _, p := range r.Form["quarantine"] {
		wanted[p] = true
	}

	// Find the original preview run to know what the full set was.
	previewID := r.FormValue("preview_run_id")
	excluded := []string{}
	if previewID != "" {
		if previewRun := s.runs.Get(previewID); previewRun != nil {
			view, err := BuildResults(previewRun)
			if err == nil {
				for _, g := range view.Groups {
					for _, rm := range g.Remove {
						if !wanted[rm.Path] {
							excluded = append(excluded, rm.Path)
						}
					}
				}
			}
		}
	}

	args := []string{"--self-check", folder, "--assume-yes"}
	args = appendCommonOptions(args, r)
	if r.FormValue("include_similar_video") == "1" {
		args = append(args, "--include-similar-video")
	}
	for _, p := range excluded {
		args = append(args, "--exclude-path", p)
	}

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
