package server

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// LocateTwincut finds the twincut.sh script. Resolution order:
//  1. explicit override (typically the --twincut-bin flag)
//  2. TWINCUT_BIN environment variable
//  3. `twincut` on PATH (the install symlink, post `make install`)
//  4. sibling of the running binary: <dir-of-twincut-ui>/twincut.sh
//     (covers the dev case where bin/twincut-ui sits next to bin/twincut.sh)
//
// Returns an absolute, executable path or an error describing what was tried.
func LocateTwincut(override string) (string, error) {
	tried := []string{}

	check := func(p string) (string, bool) {
		if p == "" {
			return "", false
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (abs failed: %v)", p, err))
			return "", false
		}
		info, err := os.Stat(abs)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%v)", abs, err))
			return "", false
		}
		if info.IsDir() {
			tried = append(tried, fmt.Sprintf("%s (is a directory)", abs))
			return "", false
		}
		if info.Mode()&0o111 == 0 {
			tried = append(tried, fmt.Sprintf("%s (not executable)", abs))
			return "", false
		}
		return abs, true
	}

	if p, ok := check(override); ok {
		return p, nil
	}
	if p, ok := check(os.Getenv("TWINCUT_BIN")); ok {
		return p, nil
	}
	if p, err := exec.LookPath("twincut"); err == nil {
		// LookPath already verifies executability.
		return p, nil
	} else {
		tried = append(tried, fmt.Sprintf("PATH lookup 'twincut' (%v)", err))
	}
	if exe, err := os.Executable(); err == nil {
		// Resolve symlinks so we land in the real bin/ dir even when the
		// user launched via the install symlink.
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		sibling := filepath.Join(filepath.Dir(exe), "twincut.sh")
		if p, ok := check(sibling); ok {
			return p, nil
		}
	}

	return "", fmt.Errorf("could not locate twincut.sh; tried: %v", tried)
}

// ErrTwincutNotFound is returned when LocateTwincut exhausts all candidates.
var ErrTwincutNotFound = errors.New("twincut.sh not found")
