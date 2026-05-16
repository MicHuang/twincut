package server

import (
	"os"
	"path/filepath"
	"strings"
)

// simVideoExts mirrors VIDEO_EXTS in bin/twincut.sh — extensions twincut
// treats as video for similar-video detection. Distinct from thumb.go's
// videoExts which only drives ffmpeg seek strategy (and may diverge).
var simVideoExts = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".m4v":  true,
	".avi":  true,
	".mkv":  true,
	".webm": true,
	".hevc": true,
	".h265": true,
	".3gp":  true,
	".mts":  true,
	".m2ts": true,
}

// FolderHasVideos reports whether dir contains at least one video file.
// Walks recursively and stops at the first match. Bounded at 5000 entries
// scanned so a huge tree can't stall the form-submit handler — past that
// budget the answer is "didn't see one yet, treat as no".
//
// Skips dot-dirs and underscore-prefixed dirs (twincut's own _QUARANTINE,
// _similar_video, etc.) so a previous run's quarantine doesn't trigger a
// false positive.
func FolderHasVideos(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}

	const maxEntries = 5000
	seen := 0
	found := false
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != dir {
				name := d.Name()
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if simVideoExts[ext] {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, walkErr
	}
	return found, nil
}
