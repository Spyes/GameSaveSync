package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseManifestDropsNilEntries ensures a remote manifest containing a null
// game element is sanitized rather than producing nil pointers (which would
// panic during discovery).
func TestParseManifestDropsNilEntries(t *testing.T) {
	m, err := parseManifest([]byte(`{"version":1,"games":[null,{"name":"Elden Ring"},{"name":""}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Games) != 1 || m.Games[0].Name != "Elden Ring" {
		t.Fatalf("expected only [Elden Ring], got %+v", m.Games)
	}
	// Must not panic when iterating (mirrors the discovery loops).
	for _, g := range m.Games {
		_ = g.Name
	}
}

// TestReadManifestCorruptErrors ensures a corrupt on-disk manifest returns an
// error (so Upload skips the write) rather than silently resetting to empty,
// which would drop other devices' games on the next push.
func TestReadManifestCorruptErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/"+manifestFile, "{ this is not json")
	if _, err := readManifest(dir); err == nil {
		t.Fatal("expected error for corrupt manifest, got nil")
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// Leading ~ expands to home.
	if got, err := expandPath("~/mydev/steamgames"); err != nil || got != filepath.Join(home, "mydev/steamgames") {
		t.Errorf("~/mydev/steamgames -> %q, %v", got, err)
	}
	if got, err := expandPath("~"); err != nil || got != home {
		t.Errorf("~ -> %q, %v", got, err)
	}
	// Absolute paths pass through (cleaned).
	if got, err := expandPath("/tmp/saves/"); err != nil || got != "/tmp/saves" {
		t.Errorf("/tmp/saves/ -> %q, %v", got, err)
	}
	// Relative paths are rejected so files never land next to the binary.
	if _, err := expandPath("mydev/steamgames"); err == nil {
		t.Error("expected relative path to be rejected")
	}
	if _, err := expandPath("./x"); err == nil {
		t.Error("expected ./x to be rejected")
	}
}
