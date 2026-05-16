package server

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFolderHasVideos_ImageOnly(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a.jpg"))
	touch(t, filepath.Join(dir, "b.png"))
	touch(t, filepath.Join(dir, "sub", "c.heic"))
	got, err := FolderHasVideos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Errorf("FolderHasVideos = true; want false (no video files)")
	}
}

func TestFolderHasVideos_TopLevelVideo(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a.jpg"))
	touch(t, filepath.Join(dir, "clip.MP4")) // case-insensitive
	got, err := FolderHasVideos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Errorf("FolderHasVideos = false; want true (.MP4 should be detected)")
	}
}

func TestFolderHasVideos_NestedVideo(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "2024", "trip", "v.mov"))
	got, err := FolderHasVideos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Errorf("FolderHasVideos = false; want true (nested .mov)")
	}
}

func TestFolderHasVideos_AllExtensions(t *testing.T) {
	for _, ext := range []string{"mp4", "mov", "m4v", "avi", "mkv", "webm", "hevc", "h265", "3gp", "mts", "m2ts"} {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "x."+ext))
		got, err := FolderHasVideos(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Errorf("FolderHasVideos for ext .%s = false; want true", ext)
		}
	}
}

func TestFolderHasVideos_NonExistent(t *testing.T) {
	_, err := FolderHasVideos("/no/such/path/exists")
	if err == nil {
		t.Errorf("FolderHasVideos on missing dir = nil err; want error")
	}
}

func TestFolderHasVideos_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := FolderHasVideos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Errorf("FolderHasVideos on empty dir = true; want false")
	}
}
