// Command twincut-ui is the local Web UI server for the twincut deduplication
// tool. It binds to 127.0.0.1, opens the user's browser, and shells out to
// twincut.sh for actual scan work. See docs/superpowers/specs/ for the design.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/MicHuang/twincut/ui/server"
)

// All UI assets are baked into the binary. The build is a single-file drop-in.
//
//go:embed templates/*.html static/*
var assets embed.FS

func main() {
	var (
		port     = flag.Int("port", 7681, "HTTP port (falls back to a free port if taken)")
		noOpen   = flag.Bool("no-open", false, "Do not open the browser on launch")
		stateDir = flag.String("state-dir", "", "State directory (default ~/.twincut-ui)")
		lang     = flag.String("lang", "", "Force language: en | zh-Hans (default: auto from Accept-Language)")
	)
	flag.Parse()

	sd, err := resolveStateDir(*stateDir)
	if err != nil {
		log.Fatalf("state dir: %v", err)
	}

	addr, listener, err := pickPort(*port)
	if err != nil {
		log.Fatalf("bind: %v", err)
	}

	srv := server.New(server.Options{
		Assets:   assets,
		StateDir: sd,
		Lang:     *lang,
	})

	httpSrv := &http.Server{
		Handler:     srv.Handler(),
		ReadTimeout: 30 * time.Second,
		// WriteTimeout deliberately zero — SSE responses are long-lived.
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("twincut-ui listening on http://%s  (state: %s)", addr, sd)
		if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	if !*noOpen {
		go openBrowserAfter(150*time.Millisecond, "http://"+addr)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Printf("got signal %s, shutting down", s)
	case err := <-serverErr:
		if err != nil {
			log.Printf("server error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// resolveStateDir picks the user-supplied path or defaults to ~/.twincut-ui.
// Creates the directory if missing.
func resolveStateDir(want string) (string, error) {
	if want == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		want = filepath.Join(home, ".twincut-ui")
	}
	if err := os.MkdirAll(want, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", want, err)
	}
	return want, nil
}

// pickPort tries the requested port first; on conflict, falls back to a
// kernel-assigned free port. Always binds to 127.0.0.1 — never the LAN.
func pickPort(want int) (string, net.Listener, error) {
	tryAddr := fmt.Sprintf("127.0.0.1:%d", want)
	if l, err := net.Listen("tcp", tryAddr); err == nil {
		return l.Addr().String(), l, nil
	}
	log.Printf("port %d in use, picking a free one", want)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	return l.Addr().String(), l, nil
}

// openBrowserAfter waits briefly so the listener is accepting, then asks the
// OS to open the URL. macOS uses `open`; Linux `xdg-open`; Windows uses
// rundll32. Errors are logged and otherwise ignored — the user can still
// open the URL manually.
func openBrowserAfter(delay time.Duration, url string) {
	time.Sleep(delay)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		log.Printf("unknown OS, please open %s manually", url)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser (%v); open %s manually", err, url)
	}
}
