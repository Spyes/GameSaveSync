package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestRoundTrip exercises Upload -> History -> Download against a local bare
// repo used as the "GitHub" remote. It covers: first push to an empty repo,
// modification + deletion propagation, linear history, and download pruning
// stale local files.
func TestRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Bare repo standing in for the GitHub remote.
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	repoURL := remote // plain local path — go-git uses the file transport

	// Source save folder for device A.
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "save.dat"), "v1")
	writeFile(t, filepath.Join(src, "sub", "inner.txt"), "hello")

	sA := &Sync{ID: "a", Name: "GameA", RepoURL: repoURL, LocalPath: src}

	if res, _, err := Upload(sA, "", "deviceA", "first upload"); err != nil {
		t.Fatalf("first upload: %v", err)
	} else {
		t.Logf("upload 1: %s", res)
	}

	// Second upload: change a file, add a file, delete a file.
	writeFile(t, filepath.Join(src, "save.dat"), "v2")
	writeFile(t, filepath.Join(src, "new.txt"), "added")
	if err := os.Remove(filepath.Join(src, "sub", "inner.txt")); err != nil {
		t.Fatal(err)
	}
	if res, _, err := Upload(sA, "", "deviceA", ""); err != nil {
		t.Fatalf("second upload: %v", err)
	} else {
		t.Logf("upload 2: %s", res)
	}

	// History should list both versions for GameA.
	vs, err := History(sA, "", 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("expected 2 versions, got %d: %+v", len(vs), vs)
	}

	// Download to a fresh folder that also holds a stale file to be pruned.
	dst := t.TempDir()
	writeFile(t, filepath.Join(dst, "stale.txt"), "should be deleted")
	sB := &Sync{ID: "b", Name: "GameA", RepoURL: repoURL, LocalPath: dst}
	if res, _, err := Download(sB, ""); err != nil {
		t.Fatalf("download: %v", err)
	} else {
		t.Logf("download: %s", res)
	}

	// Verify: latest content, added file present, deleted + stale files gone.
	if got := mustRead(t, filepath.Join(dst, "save.dat")); got != "v2" {
		t.Errorf("save.dat = %q, want v2", got)
	}
	if got := mustRead(t, filepath.Join(dst, "new.txt")); got != "added" {
		t.Errorf("new.txt = %q, want added", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "inner.txt")); !os.IsNotExist(err) {
		t.Errorf("deleted file inner.txt should not exist in download")
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("stale local file should have been pruned on download")
	}
}

// TestDownloadUnknownNameProtectsLocal ensures Download refuses (rather than
// wiping the local folder) when the repo has no subfolder for the given name.
func TestDownloadUnknownNameProtectsLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	// Seed the repo with GameA.
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "save.dat"), "v1")
	if _, _, err := Upload(&Sync{ID: "a", Name: "GameA", RepoURL: remote, LocalPath: src}, "", "deviceA", ""); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	// Download a name that doesn't exist in the repo.
	dst := t.TempDir()
	writeFile(t, filepath.Join(dst, "precious.txt"), "keep me")
	_, _, err := Download(&Sync{ID: "b", Name: "GameB", RepoURL: remote, LocalPath: dst}, "")
	if err == nil {
		t.Fatal("expected error downloading unknown name, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(dst, "precious.txt")); statErr != nil {
		t.Errorf("local file must be preserved when name not found: %v", statErr)
	}
}

// TestManifestAndDiscover verifies that Upload writes a discoverable manifest
// (with a path hint for this OS) and that DiscoverRepo surfaces the game.
func TestManifestAndDiscover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	src := t.TempDir()
	writeFile(t, filepath.Join(src, "save.dat"), "v1")
	if _, _, err := Upload(&Sync{ID: "a", Name: "Elden Ring", RepoURL: remote, LocalPath: src}, "", "deviceA", ""); err != nil {
		t.Fatalf("upload: %v", err)
	}

	games, err := DiscoverRepo(remote, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(games) != 1 || games[0].Name != "Elden Ring" {
		t.Fatalf("expected [Elden Ring], got %+v", games)
	}
	if got := games[0].PathHints[runtime.GOOS]; got != src {
		t.Errorf("path hint for %s = %q, want %q", runtime.GOOS, got, src)
	}
}

// TestDiscoverFallbackNoManifest verifies that a repo with a game subfolder but
// no manifest still lists the subfolder as a game.
func TestDiscoverFallbackNoManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Build a bare remote whose working content is a "GameX/" folder with no
	// .savesync.json, by committing via a throwaway clone.
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeFile(t, filepath.Join(work, "GameX", "save.dat"), "hi")
	run("add", "-A")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	run("remote", "add", "origin", remote)
	run("push", "-q", "origin", "main")

	games, err := DiscoverRepo(remote, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(games) != 1 || games[0].Name != "GameX" {
		t.Fatalf("expected fallback [GameX], got %+v", games)
	}
}

// TestRemoteStatus verifies the update-available detection: a device that has
// never synced (or is behind the remote tip for its game) reports
// "update-available", and one at the current tip reports "in-sync".
func TestRemoteStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	src := t.TempDir()
	writeFile(t, filepath.Join(src, "save.dat"), "v1")
	_, h1, err := Upload(&Sync{Name: "GameA", RepoURL: remote, LocalPath: src}, "", "deviceA", "")
	if err != nil {
		t.Fatalf("upload 1: %v", err)
	}

	get := func(s Sync) string {
		s.RepoURL = remote
		return RemoteStatuses([]Sync{s}, "")[s.ID].Status
	}

	// Never synced this game here → update available.
	if got := get(Sync{ID: "never", Name: "GameA"}); got != "update-available" {
		t.Errorf("never-synced: got %q, want update-available", got)
	}
	// At the current tip → in sync.
	if got := get(Sync{ID: "current", Name: "GameA", LastSyncedRemote: h1}); got != "in-sync" {
		t.Errorf("current: got %q, want in-sync", got)
	}
	// A game not in the repo → no-remote.
	if got := get(Sync{ID: "missing", Name: "GameB"}); got != "no-remote" {
		t.Errorf("missing game: got %q, want no-remote", got)
	}

	// Remote advances → a device pinned at h1 is now behind.
	writeFile(t, filepath.Join(src, "save.dat"), "v2")
	if _, _, err := Upload(&Sync{Name: "GameA", RepoURL: remote, LocalPath: src}, "", "deviceA", ""); err != nil {
		t.Fatalf("upload 2: %v", err)
	}
	if got := get(Sync{ID: "behind", Name: "GameA", LastSyncedRemote: h1}); got != "update-available" {
		t.Errorf("behind: got %q, want update-available", got)
	}
}
