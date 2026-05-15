package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsAllowedPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this system")
	}
	cases := []struct {
		path string
		want bool
	}{
		{"", true},
		{home, true},
		{filepath.Join(home, "Pictures"), true},
		{"/Volumes", true},
		{"/", false},
		{"/etc", false},
		{"/etc/passwd", false},
		{"/System", false},
		{"/private/var", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got, err := IsAllowedPath(c.path)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("IsAllowedPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestListDir_RootsWhenEmpty(t *testing.T) {
	got, err := ListDir("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got.Roots) == 0 {
		t.Fatal("expected at least one root (the home dir)")
	}
	if len(got.Entries) != 0 {
		t.Errorf("expected no Entries on root listing, got %d", len(got.Entries))
	}
}

func TestListDir_DenyOutsideAllowlist(t *testing.T) {
	_, err := ListDir("/etc")
	if err == nil {
		t.Fatal("want error for /etc, got nil")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("err = %v, want allowlist message", err)
	}
}

func TestListDir_HidesNonDirs(t *testing.T) {
	// Override HOME so t.TempDir() lands inside the allowlist.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, "fixture")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	// Create a sub-dir, a regular file, and a hidden dir.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatalf("mkdir .hidden: %v", err)
	}

	listing, err := ListDir(dir)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	names := []string{}
	for _, e := range listing.Entries {
		if !e.IsDir {
			t.Errorf("non-dir in entries: %s", e.Name)
		}
		names = append(names, e.Name+":"+boolStr(e.Hidden))
	}
	want := []string{".hidden:true", "sub:false"}
	if !sliceEq(names, want) {
		t.Errorf("entries = %v, want %v", names, want)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
