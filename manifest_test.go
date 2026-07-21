package main

import "testing"

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
