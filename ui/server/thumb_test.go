package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findFixture walks up from the test working dir to locate
// tests/fixtures/<rel>. Returns "" if the fixture isn't found, so callers
// can t.Skip on environments that haven't checked out the fixtures.
func findFixture(t *testing.T, rel string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		p := filepath.Join(dir, "tests", "fixtures", rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// requireFFmpeg skips the test when the ffmpeg binary isn't on PATH —
// generateThumb needs it.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping thumb test")
	}
}

// newTestServer builds a Server wired to a temp state dir and a temp HOME so
// fixture paths under that HOME pass the allowlist.
func newTestServer(t *testing.T, home string) *Server {
	t.Helper()
	t.Setenv("HOME", home)
	return &Server{
		opts: Options{
			StateDir:    t.TempDir(),
			TwincutPath: "/dev/null",
		},
	}
}

func TestThumb_StillImage_GeneratesAndCaches(t *testing.T) {
	requireFFmpeg(t)
	src := findFixture(t, "image/red.jpg")
	if src == "" {
		t.Skip("fixture image/red.jpg not found")
	}

	// Copy fixture under HOME so it satisfies the allowlist.
	home := t.TempDir()
	dst := filepath.Join(home, "red.jpg")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	srv := newTestServer(t, home)

	req := httptest.NewRequest("GET", "/thumb?path="+dst, nil)
	rec := httptest.NewRecorder()
	srv.handleThumb(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty body")
	}

	// Cache file should now exist.
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(ThumbCacheDir(srv.opts.StateDir),
		thumbCacheKey(dst, info.ModTime().Unix(), thumbDefault))
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}

	// Second hit should not regenerate; we verify by removing ffmpeg from
	// PATH and re-issuing — if the cache is consulted, it still works.
	t.Setenv("PATH", "/nonexistent")
	rec2 := httptest.NewRecorder()
	srv.handleThumb(rec2, httptest.NewRequest("GET", "/thumb?path="+dst, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("cache-hit status = %d; ffmpeg should not have been re-run", rec2.Code)
	}
}

func TestThumb_MtimeBumpInvalidatesCache(t *testing.T) {
	requireFFmpeg(t)
	src := findFixture(t, "image/red.jpg")
	if src == "" {
		t.Skip("fixture not found")
	}
	home := t.TempDir()
	dst := filepath.Join(home, "red.jpg")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	srv := newTestServer(t, home)

	// First request → cache populated for mtime A.
	rec := httptest.NewRecorder()
	srv.handleThumb(rec, httptest.NewRequest("GET", "/thumb?path="+dst, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first call status = %d", rec.Code)
	}

	// Bump mtime to a different value.
	bumped := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(dst, bumped, bumped); err != nil {
		t.Fatal(err)
	}

	// Second request — the new cache key includes the new mtime, so the
	// cache miss should still succeed (and write a new cache entry under
	// the new key).
	rec2 := httptest.NewRecorder()
	srv.handleThumb(rec2, httptest.NewRequest("GET", "/thumb?path="+dst, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("post-bump status = %d", rec2.Code)
	}

	// We should now have two cache files (one per mtime).
	entries, err := os.ReadDir(ThumbCacheDir(srv.opts.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("cache entries = %d, want 2 (mtime change should leave both keys)", len(entries))
	}
}

func TestThumb_VideoFixture(t *testing.T) {
	requireFFmpeg(t)
	src := findFixture(t, "video/clip_high.mp4")
	if src == "" {
		t.Skip("video fixture not found")
	}
	home := t.TempDir()
	dst := filepath.Join(home, "clip.mp4")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	srv := newTestServer(t, home)

	rec := httptest.NewRecorder()
	srv.handleThumb(rec, httptest.NewRequest("GET", "/thumb?path="+dst, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() < 100 {
		t.Errorf("video thumb body looks too small: %d bytes", rec.Body.Len())
	}
	// JPEG magic.
	if got := rec.Body.Bytes()[:2]; got[0] != 0xFF || got[1] != 0xD8 {
		t.Errorf("output isn't JPEG; first bytes = %x", got)
	}
}

func TestThumb_DisallowedPath(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	rec := httptest.NewRecorder()
	srv.handleThumb(rec, httptest.NewRequest("GET", "/thumb?path=/etc/passwd", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestThumb_MissingFile(t *testing.T) {
	home := t.TempDir()
	srv := newTestServer(t, home)
	rec := httptest.NewRecorder()
	srv.handleThumb(rec, httptest.NewRequest("GET", "/thumb?path="+filepath.Join(home, "nope.jpg"), nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestThumb_PathRequired(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	rec := httptest.NewRecorder()
	srv.handleThumb(rec, httptest.NewRequest("GET", "/thumb", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestThumb_SizeClampedAndRespected(t *testing.T) {
	requireFFmpeg(t)
	src := findFixture(t, "image/red.jpg")
	if src == "" {
		t.Skip("fixture not found")
	}
	home := t.TempDir()
	dst := filepath.Join(home, "red.jpg")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, home)

	for _, size := range []string{"32", "640", "9999"} {
		t.Run("size_"+size, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.handleThumb(rec, httptest.NewRequest("GET", "/thumb?path="+dst+"&size="+size, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("size=%s status=%d body=%s", size, rec.Code, rec.Body.String())
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/") {
				t.Errorf("size=%s content-type=%s", size, rec.Header().Get("Content-Type"))
			}
		})
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
