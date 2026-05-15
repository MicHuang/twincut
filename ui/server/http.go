// Package server hosts the HTTP layer of twincut-ui.
//
// Stage 3 adds the run manager + SSE plumbing on top of stage 2's static
// shell. The HTMX-driven workflow forms arrive in stage 4.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
)

// Options configures a Server. All fields are required unless documented
// otherwise.
type Options struct {
	// Assets is the embedded filesystem holding templates/ and static/.
	Assets embed.FS
	// StateDir is the resolved ~/.twincut-ui (or user override). Used for
	// run journals, recents, settings, thumb cache.
	StateDir string
	// Lang is the forced language code from the CLI flag. Empty means
	// "auto-detect from Accept-Language".
	Lang string
	// TwincutPath is the resolved path to twincut.sh.
	TwincutPath string
}

// Server is the long-lived HTTP layer.
type Server struct {
	opts Options
	tmpl *template.Template
	runs *RunManager
}

// New constructs a Server from the given options. Panics on template parse
// errors — these are baked-in assets, so failure means the binary itself is
// broken.
func New(opts Options) *Server {
	tmpl, err := template.ParseFS(opts.Assets, "templates/*.html")
	if err != nil {
		panic("twincut-ui: parse embedded templates: " + err.Error())
	}
	rm, err := NewRunManager(opts.StateDir, opts.TwincutPath)
	if err != nil {
		panic("twincut-ui: run manager: " + err.Error())
	}
	return &Server{opts: opts, tmpl: tmpl, runs: rm}
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static shell.
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.Handle("/static/", http.FileServer(http.FS(s.opts.Assets)))

	// API: stage 3 surface. Stage 4 will add /api/runs/{id}/apply,
	// /thumb, /fs, etc.
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancelRun)
	mux.HandleFunc("GET /sse/{id}", s.handleSSE)

	// Debug surface — exists to verify the run manager + SSE plumbing
	// end-to-end without a real workflow form. Slated for removal once
	// stage 4 ships, but useful during development.
	mux.HandleFunc("GET /debug", s.handleDebug)
	mux.HandleFunc("GET /debug/run/{id}", s.handleDebugRun)

	return mux
}

// ----------------------------------------------------------------------------
// Static handlers
// ----------------------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","twincut":"` + s.opts.TwincutPath + `"}` + "\n"))
}

// ----------------------------------------------------------------------------
// Run-management API
// ----------------------------------------------------------------------------

type startRunRequest struct {
	Mode string   `json:"mode"`
	Args []string `json:"args"`
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Args) == 0 {
		http.Error(w, "args is required", http.StatusBadRequest)
		return
	}
	run, err := s.runs.Start(StartOptions{Mode: req.Mode, Args: req.Args})
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, run.Snapshot())
}

func (s *Server) handleListRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.runs.List())
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run := s.runs.Get(r.PathValue("id"))
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, run.Snapshot())
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	run := s.runs.Get(r.PathValue("id"))
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	cancelled := run.Cancel()
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled, "id": run.ID})
}

// ----------------------------------------------------------------------------
// Debug surface (drops out in stage 4)
// ----------------------------------------------------------------------------

type debugPageData struct {
	TwincutPath string
	Runs        []Snapshot
}

func (s *Server) handleDebug(w http.ResponseWriter, _ *http.Request) {
	data := debugPageData{
		TwincutPath: s.opts.TwincutPath,
		Runs:        s.runs.List(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "debug.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type debugRunPageData struct {
	RunID string
}

func (s *Server) handleDebugRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.runs.Get(id) == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "debug_run.html", debugRunPageData{RunID: id}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(body); err != nil {
		// Body already started; nothing useful to do besides log.
		fmt.Fprintln(w, `{"error":"encode failed"}`)
	}
}
