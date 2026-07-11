package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	recentsFile = "recents.json"
	recentsMax  = 10
)

// RecentsStore persists the user's most recently used scan folders to
// <state-dir>/recents.json. Concurrency-safe.
type RecentsStore struct {
	path string
	mu   sync.Mutex
}

// NewRecentsStore opens the recents file under stateDir. Missing file is
// fine; it's created on first Add.
func NewRecentsStore(stateDir string) *RecentsStore {
	return &RecentsStore{path: filepath.Join(stateDir, recentsFile)}
}

// List returns the recents in most-recent-first order. Returns an empty
// slice if the file doesn't exist yet.
func (s *RecentsStore) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

// Add prepends path to the recents (deduping if it was already present)
// and trims the list to recentsMax entries. Empty paths are ignored.
func (s *RecentsStore) Add(path string) error {
	if path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.readLocked()
	if err != nil {
		return err
	}
	// Remove any existing occurrence so the new one floats to the top.
	out := make([]string, 0, len(current)+1)
	out = append(out, path)
	for _, p := range current {
		if p == path {
			continue
		}
		out = append(out, p)
		if len(out) >= recentsMax {
			break
		}
	}
	return s.writeLocked(out)
}

// Remove drops path from the recents if present. Missing path is a no-op.
func (s *RecentsStore) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return err
	}
	out := make([]string, 0, len(current))
	for _, p := range current {
		if p == path {
			continue
		}
		out = append(out, p)
	}
	return s.writeLocked(out)
}

func (s *RecentsStore) readLocked() ([]string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		// Corrupt file → start fresh rather than 500 forever.
		return nil, nil
	}
	return list, nil
}

func (s *RecentsStore) writeLocked(list []string) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
