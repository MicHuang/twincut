package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocateTwincut_Override(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "twincut.sh")
	if err := os.WriteFile(bin, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := LocateTwincut(bin)
	if err != nil {
		t.Fatalf("LocateTwincut: %v", err)
	}
	if got != bin {
		t.Errorf("got %s, want %s", got, bin)
	}
}

func TestLocateTwincut_RejectsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "twincut.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("TWINCUT_BIN", bin)
	t.Setenv("PATH", "") // disable PATH lookup so we don't accidentally find a real install

	_, err := LocateTwincut("")
	if err == nil {
		t.Fatalf("want error for non-executable file, got nil")
	}
}

func TestLocateTwincut_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "twincut.sh")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("TWINCUT_BIN", subdir)
	t.Setenv("PATH", "")

	_, err := LocateTwincut("")
	if err == nil {
		t.Fatalf("want error for directory, got nil")
	}
}

func TestLocateTwincut_EnvVar(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "twincut.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("TWINCUT_BIN", bin)
	t.Setenv("PATH", "")

	got, err := LocateTwincut("")
	if err != nil {
		t.Fatalf("LocateTwincut: %v", err)
	}
	if got != bin {
		t.Errorf("got %s, want %s", got, bin)
	}
}
