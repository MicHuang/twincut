// Package server hosts the HTTP layer of twincut-ui. Stage 2 ships only an
// index page served from the embedded filesystem; subsequent stages add the
// run manager, SSE streaming, directory browser, thumbnails, and so on.
package server

import (
	"embed"
	"html/template"
	"net/http"
)

// Options configures a Server. All fields are required unless documented
// otherwise.
type Options struct {
	// Assets is the embedded filesystem holding templates/ and static/.
	Assets embed.FS
	// StateDir is the resolved ~/.twincut-ui (or user override). Used in
	// later stages for run journals, recents, settings, thumb cache.
	StateDir string
	// Lang is the forced language code from the CLI flag. Empty means
	// "auto-detect from Accept-Language".
	Lang string
}

// Server is the long-lived HTTP layer. Construct via New and obtain the
// http.Handler via Handler.
type Server struct {
	opts Options
	tmpl *template.Template
}

// New constructs a Server from the given options. Panics on template parse
// errors — these are baked-in assets, so failure means the binary itself is
// broken and the only sane response is a fast crash.
func New(opts Options) *Server {
	tmpl, err := template.ParseFS(opts.Assets, "templates/*.html")
	if err != nil {
		panic("twincut-ui: parse embedded templates: " + err.Error())
	}
	return &Server{opts: opts, tmpl: tmpl}
}

// Handler returns the root http.Handler for the server. Routes are registered
// here so main.go stays focused on lifecycle.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.Handle("/static/", http.FileServer(http.FS(s.opts.Assets)))
	return mux
}

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
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}
