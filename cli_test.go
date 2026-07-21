package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCLI exercises the JSON CLI (used by the Decky plugin) end to end against
// a local bare repo, sharing a config dir via SAVESYNC_CONFIG_DIR.
func TestCLI(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("SAVESYNC_CONFIG_DIR", cfgDir)

	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	// Seed a sync directly in the shared config the CLI will read.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "save.dat"), "v1")
	cfg.Syncs = append(cfg.Syncs, &Sync{ID: "id1", Name: "GameA", RepoURL: remote, LocalPath: src})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		code := runCLI(args, &buf)
		if code != 0 {
			t.Fatalf("cli %v exited %d: %s", args, code, buf.String())
		}
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatalf("cli %v: bad JSON: %s", args, buf.String())
		}
		return m
	}

	// list → one game
	var buf bytes.Buffer
	if code := runCLI([]string{"list"}, &buf); code != 0 {
		t.Fatalf("list exited %d: %s", code, buf.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &list); err != nil || len(list) != 1 || list[0]["name"] != "GameA" {
		t.Fatalf("list = %s (err %v)", buf.String(), err)
	}

	// upload with a note
	if got := run("upload", "id1", "--note", "first save"); got["result"] == nil {
		t.Fatalf("upload result missing: %v", got)
	}

	// history → note surfaced, current == latest
	h := run("history", "id1")
	if h["current"] != h["latest"] {
		t.Errorf("after upload current(%v) != latest(%v)", h["current"], h["latest"])
	}
	versions, _ := h["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %v", h["versions"])
	}
	if v0 := versions[0].(map[string]any); v0["note"] != "first save" {
		t.Errorf("note not surfaced: %v", v0["note"])
	}

	// status → in-sync for this device
	st := run("status")
	entry, _ := st["id1"].(map[string]any)
	if entry == nil || entry["status"] != "in-sync" {
		t.Errorf("status = %v, want in-sync", st)
	}
}
