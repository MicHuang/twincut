package server

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ThumbCacheDir returns the directory under which generated thumbnails live.
// It does not create the directory; the caller (server) does that lazily on
// first miss.
func ThumbCacheDir(stateDir string) string {
	return filepath.Join(stateDir, "cache", "thumbs")
}

// thumbSizeMin / thumbSizeMax bound the requested edge length in pixels. The
// upper bound stops a hostile request from triggering a multi-MB thumbnail.
const (
	thumbSizeMin = 64
	thumbSizeMax = 1024
	thumbDefault = 256
)

// thumbCacheKey combines the absolute path, mtime, and requested edge length
// into a stable cache filename. Mtime in the key means edits invalidate the
// cache automatically.
func thumbCacheKey(absPath string, mtime int64, size int) string {
	sum := sha1.Sum([]byte(absPath))
	return fmt.Sprintf("%s_%d_%d.jpg", hex.EncodeToString(sum[:]), mtime, size)
}

// videoExts is the set of extensions we treat as video for thumb generation.
// Mirrors the bash side roughly; we only need this to pick the ffmpeg seek
// strategy (still images don't get -ss).
var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".avi": true,
	".m4v": true, ".webm": true, ".mts": true, ".m2ts": true,
	".wmv": true, ".flv": true, ".3gp": true,
}

func isVideoExt(p string) bool {
	return videoExts[strings.ToLower(filepath.Ext(p))]
}

// generateThumb shells out to ffmpeg to produce a JPEG thumbnail. ffmpeg
// covers both stills (jpg/png/heic/webp/…) and videos with one toolchain,
// which is friendlier than juggling Go image libs per format. dst is the
// final cache path; we write to a sibling tempfile and rename for atomicity.
func generateThumb(src, dst string, size int) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".thumb-*.jpg")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	scale := fmt.Sprintf("scale='min(%d,iw)':-2", size)
	args := []string{"-y", "-loglevel", "error"}
	if isVideoExt(src) {
		// -ss before -i is the fast input-seek; falls back to first frame
		// if the clip is shorter than 1s.
		args = append(args, "-ss", "1", "-i", src, "-vf", scale, "-frames:v", "1", "-q:v", "3", tmpPath)
	} else {
		args = append(args, "-i", src, "-vf", scale, "-frames:v", "1", "-q:v", "3", tmpPath)
	}
	cmd := exec.Command("ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// For videos shorter than the seek point, ffmpeg may produce an empty
	// file; retry once without the seek so we still get a frame.
	if info, statErr := os.Stat(tmpPath); statErr == nil && info.Size() == 0 && isVideoExt(src) {
		retry := []string{"-y", "-loglevel", "error", "-i", src, "-vf", scale, "-frames:v", "1", "-q:v", "3", tmpPath}
		if out, err := exec.Command("ffmpeg", retry...).CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg (retry): %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return os.Rename(tmpPath, dst)
}

// ThumbHandler serves thumbnails for paths inside the allowlist.
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	abs = filepath.Clean(abs)

	allowed, err := IsAllowedPath(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "path is outside the allowlist", http.StatusForbidden)
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}

	size := thumbDefault
	if v := r.URL.Query().Get("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < thumbSizeMin {
				n = thumbSizeMin
			} else if n > thumbSizeMax {
				n = thumbSizeMax
			}
			size = n
		}
	}

	cacheDir := ThumbCacheDir(s.opts.StateDir)
	cachePath := filepath.Join(cacheDir, thumbCacheKey(abs, info.ModTime().Unix(), size))

	if _, err := os.Stat(cachePath); err != nil {
		if err := generateThumb(abs, cachePath, size); err != nil {
			http.Error(w, "thumb: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	f, err := os.Open(cachePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}
