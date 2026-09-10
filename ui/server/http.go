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
	"net"
	"net/http"
	"net/url"
	"strings"
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
	opts    Options
	tmpls   map[string]*template.Template
	cats    map[string]catalog
	runs    *RunManager
	recents *RecentsStore
}

// baseFuncMap holds the template helpers that do not depend on locale. New()
// adds t/tmap/lang per locale on top; tests build from the same base so the
// two cannot drift.
func baseFuncMap() template.FuncMap {
	return template.FuncMap{
		"dict": func(args ...any) (map[string]any, error) {
			if len(args)%2 != 0 {
				return nil, fmt.Errorf("dict requires even number of args")
			}
			m := make(map[string]any, len(args)/2)
			for i := 0; i < len(args); i += 2 {
				key, ok := args[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key %v is not a string", args[i])
				}
				m[key] = args[i+1]
			}
			return m, nil
		},
		"hasPrefix": strings.HasPrefix,
	}
}

// New constructs a Server from the given options. Panics on template parse
// errors — these are baked-in assets, so failure means the binary itself is
// broken.
func New(opts Options) *Server {
	cats, err := loadCatalogs(opts.Assets)
	if err != nil {
		panic("twincut-ui: load locale catalogs: " + err.Error())
	}
	// Key coverage is asserted by TestCatalogsCoverEveryUsedKey, which scans the
	// working tree. New() only checks that the locales agree with each other —
	// the shipped binary has no source to scan.
	if err := validateCatalogs(cats, nil); err != nil {
		panic("twincut-ui: locale catalogs: " + err.Error())
	}
	tmpls := make(map[string]*template.Template, len(cats))
	for code, cat := range cats {
		fm := baseFuncMap()
		fm["t"] = cat.lookup
		fm["tmap"] = cat.subtree
		fm["lang"] = func() string { return code }
		tm, err := template.New("").Funcs(fm).ParseFS(opts.Assets, "templates/*.html")
		if err != nil {
			panic("twincut-ui: parse embedded templates: " + err.Error())
		}
		tmpls[code] = tm
	}
	rm, err := NewRunManager(opts.StateDir, opts.TwincutPath)
	if err != nil {
		panic("twincut-ui: run manager: " + err.Error())
	}
	return &Server{
		opts:    opts,
		tmpls:   tmpls,
		cats:    cats,
		runs:    rm,
		recents: NewRecentsStore(opts.StateDir),
	}
}

// tmplFor returns the template set for the request's locale. Because t is
// bound into each set at parse time, a handler cannot render a mixed-language
// page.
func (s *Server) tmplFor(r *http.Request) *template.Template {
	return s.tmpls[resolveLocale(r, s.opts.Lang, s.tmpls)]
}

// httpErrorT writes a translated error. Use it ONLY for 4xx a user can
// trigger. Internal 500s keep their raw Go error: translating them obstructs
// debugging and puts uninterpolatable text in the catalog (spec §8).
func (s *Server) httpErrorT(w http.ResponseWriter, r *http.Request, key string, status int) {
	code := resolveLocale(r, s.opts.Lang, s.cats)
	http.Error(w, s.cats[code].lookup(key), status)
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static shell.
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.Handle("/static/", http.FileServer(http.FS(s.opts.Assets)))

	// Tab content (htmx loads these into #tab-content).
	mux.HandleFunc("GET /tab/self-check", s.handleSelfCheckTab)
	mux.HandleFunc("GET /tab/cross-check", s.handleCrossCheckTab)
	mux.HandleFunc("GET /tab/history", s.handleHistoryTab)
	mux.HandleFunc("GET /history/{id}/preview", s.handleHistoryPreview)
	mux.HandleFunc("POST /history/{id}/restore", s.handleHistoryRestore)
	mux.HandleFunc("GET /history/{id}/done/{restore_id}", s.handleHistoryRestoreDone)

	// Self-check workflow endpoints.
	mux.HandleFunc("POST /api/self-check/preview", s.handleSelfCheckPreview)
	mux.HandleFunc("GET /api/self-check/results/{id}", s.handleSelfCheckResults)
	mux.HandleFunc("POST /api/self-check/apply", s.handleSelfCheckApply)
	mux.HandleFunc("GET /api/self-check/done/{id}", s.handleSelfCheckDone)

	// Cross-check workflow endpoints.
	mux.HandleFunc("POST /api/cross-check/preview", s.handleCrossCheckPreview)
	mux.HandleFunc("GET /api/cross-check/results/{id}", s.handleCrossCheckResults)
	mux.HandleFunc("POST /api/cross-check/apply", s.handleCrossCheckApply)
	mux.HandleFunc("GET /api/cross-check/done/{id}", s.handleCrossCheckDone)
	mux.HandleFunc("GET /api/cross-check/add-backup-row", s.handleCrossCheckAddBackupRow)

	// Thumbnail-detect workflow endpoints.
	mux.HandleFunc("GET /tab/thumbnails", s.handleThumbnailsTab)
	mux.HandleFunc("POST /api/thumbnails/preview", s.handleThumbnailsPreview)
	mux.HandleFunc("GET /api/thumbnails/results/{id}", s.handleThumbnailsResults)
	mux.HandleFunc("POST /api/thumbnails/apply", s.handleThumbnailsApply)
	mux.HandleFunc("GET /api/thumbnails/done/{id}", s.handleThumbnailsDone)

	// Directory browser.
	mux.HandleFunc("GET /fs/list", s.handleFsList)

	// Thumbnail endpoint (used by similar-video / similar-image clusters).
	mux.HandleFunc("GET /thumb", s.handleThumb)

	// Reveal-in-Finder helper for post-apply convenience.
	mux.HandleFunc("POST /api/open", s.handleOpenPath)

	// Generic run-management API + SSE.
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancelRun)
	mux.HandleFunc("GET /sse/{id}", s.handleSSE)

	// Debug surface — kept for now; useful for raw event inspection.
	mux.HandleFunc("GET /debug", s.handleDebug)
	mux.HandleFunc("GET /debug/run/{id}", s.handleDebugRun)

	return originGuard(mux)
}

// loopbackHost reports whether host (no port) is a loopback name we serve.
func loopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// originGuard rejects (a) requests whose Host is not loopback (DNS-rebinding
// defense — we only ever bind 127.0.0.1) and (b) state-changing requests
// bearing a non-loopback Origin (CSRF defense; browsers attach Origin to
// cross-site POSTs, while curl/CLI send none and stay allowed).
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if !loopbackHost(host) {
			http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || !loopbackHost(u.Hostname()) {
					http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ----------------------------------------------------------------------------
// Static handlers
// ----------------------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// app.html boots the SPA shell and embeds the initial Self-check
	// form. Subsequent navigation happens via htmx fragment swaps.
	data := selfCheckFormData{}
	if recents, err := s.recents.List(); err == nil {
		data.Recents = recents
		if len(recents) > 0 {
			data.DefaultFolder = recents[0]
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplFor(r).ExecuteTemplate(w, "app.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "twincut": s.opts.TwincutPath})
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

func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	data := debugPageData{
		TwincutPath: s.opts.TwincutPath,
		Runs:        s.runs.List(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplFor(r).ExecuteTemplate(w, "debug.html", data); err != nil {
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
	if err := s.tmplFor(r).ExecuteTemplate(w, "debug_run.html", debugRunPageData{RunID: id}); err != nil {
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
