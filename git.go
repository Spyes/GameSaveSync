package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Version is one entry in a sync's history list, shown in the UI.
type Version struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	When    string `json:"when"`
	Author  string `json:"author"`
	Message string `json:"message"` // commit subject (first line)
	Note    string `json:"note"`    // the note the user attached at upload (commit body)
}

// Per-clone locks serialize all git operations against a single managed clone,
// so background polling (fetch) can never run concurrently with an Upload or
// Download on the same repo and corrupt its working tree or index.
var (
	repoLocksMu sync.Mutex
	repoLocks   = map[string]*sync.Mutex{}
)

// lockRepo blocks until it holds the lock for cachePath and returns the unlock
// func (call via defer).
func lockRepo(cachePath string) func() {
	repoLocksMu.Lock()
	mu := repoLocks[cachePath]
	if mu == nil {
		mu = &sync.Mutex{}
		repoLocks[cachePath] = mu
	}
	repoLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// authFor builds HTTP basic auth from the PAT. GitHub accepts the token as the
// password with any non-empty username. Returns nil when no token is set so
// public read-only clones still work.
func authFor(token string) transport.AuthMethod {
	if token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: token}
}

// ensureRepo opens the managed clone for repoURL, cloning it on first use.
// An empty remote is handled by initialising a local repo with an "origin"
// remote so the very first Upload can create the initial commit.
func ensureRepo(repoURL, cachePath, branch, token string) (*git.Repository, error) {
	repo, err := git.PlainOpen(cachePath)
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, git.ErrRepositoryNotExists) {
		return nil, fmt.Errorf("opening local clone: %w", err)
	}

	repo, err = git.PlainClone(cachePath, false, &git.CloneOptions{
		URL:  repoURL,
		Auth: authFor(token),
	})
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		_ = os.RemoveAll(cachePath) // don't leave a half-written clone behind
		return nil, fmt.Errorf("cloning %s: %w", repoURL, err)
	}

	// Remote exists but has no commits yet: init locally and wire up origin.
	repo, err = git.PlainInit(cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("initialising local clone: %w", err)
	}
	if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	}); err != nil {
		return nil, fmt.Errorf("configuring origin: %w", err)
	}
	// Point HEAD at the desired branch so the first commit lands there.
	headRef := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branch))
	if err := repo.Storer.SetReference(headRef); err != nil {
		return nil, fmt.Errorf("setting HEAD: %w", err)
	}
	return repo, nil
}

// resolveBranch picks the branch to operate on: the configured one, else the
// clone's current HEAD, else "main".
func resolveBranch(repo *git.Repository, configured string) string {
	if configured != "" {
		return configured
	}
	if repo != nil {
		if head, err := repo.Head(); err == nil && head.Name().IsBranch() {
			return head.Name().Short()
		}
	}
	return "main"
}

// fetchOrigin fetches from origin, treating "already up to date" and an empty
// remote as success.
func fetchOrigin(repo *git.Repository, token string) error {
	err := repo.Fetch(&git.FetchOptions{RemoteName: "origin", Auth: authFor(token)})
	if err == nil || errors.Is(err, git.NoErrAlreadyUpToDate) || errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return nil
	}
	return fmt.Errorf("fetching from origin: %w", err)
}

// resetToRemote points the local branch at origin/<branch> and hard-resets the
// worktree to match. It is a no-op (returns hadRemote=false) when the remote
// branch does not exist yet.
func resetToRemote(repo *git.Repository, branch string) (hadRemote bool, err error) {
	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return false, nil // remote branch doesn't exist yet — fresh repo
	}
	w, err := repo.Worktree()
	if err != nil {
		return false, err
	}
	// Make the local branch exist and point at the remote tip, then check it out.
	localBranch := plumbing.NewBranchReferenceName(branch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(localBranch, remoteRef.Hash())); err != nil {
		return false, err
	}
	if err := w.Checkout(&git.CheckoutOptions{Branch: localBranch, Force: true}); err != nil {
		return false, fmt.Errorf("checking out %s: %w", branch, err)
	}
	if err := w.Reset(&git.ResetOptions{Commit: remoteRef.Hash(), Mode: git.HardReset}); err != nil {
		return false, fmt.Errorf("resetting to origin/%s: %w", branch, err)
	}
	return true, nil
}

// Upload mirrors the sync's local folder into the clone's <name>/ subfolder,
// commits the change, and pushes it. Returns a human-readable result line and
// the commit hash now in sync for this game (for the update-available check).
func Upload(s *Sync, token, device, note string) (string, string, error) {
	cachePath, err := repoCachePath(s.RepoURL)
	if err != nil {
		return "", "", err
	}
	unlock := lockRepo(cachePath)
	defer unlock()

	repo, err := ensureRepo(s.RepoURL, cachePath, resolveBranch(nil, s.Branch), token)
	if err != nil {
		return "", "", err
	}
	branch := resolveBranch(repo, s.Branch)

	if err := fetchOrigin(repo, token); err != nil {
		return "", "", err
	}
	// Base our commit on the latest remote state so history stays linear.
	if _, err := resetToRemote(repo, branch); err != nil {
		return "", "", err
	}

	local, err := expandPath(s.LocalPath)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(local); err != nil {
		return "", "", fmt.Errorf("local folder %q: %w", local, err)
	}
	subfolder := filepath.Join(cachePath, s.Name)
	if err := mirror(local, subfolder); err != nil {
		return "", "", err
	}

	// Update the self-describing manifest so other devices can discover this
	// game (see manifest.go). Failure here shouldn't abort the save upload.
	if m, err := readManifest(cachePath); err == nil {
		upsertGame(m, s.Name, runtime.GOOS, local, device)
		_ = writeManifest(cachePath, m)
	}

	w, err := repo.Worktree()
	if err != nil {
		return "", "", err
	}
	if err := w.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return "", "", fmt.Errorf("staging changes: %w", err)
	}
	status, err := w.Status()
	if err != nil {
		return "", "", fmt.Errorf("checking status: %w", err)
	}
	if status.IsClean() {
		// Nothing changed — local already matches the remote for this game.
		hash := ""
		if head, err := repo.Head(); err == nil {
			hash, _ = latestCommitForGame(repo, head.Hash(), s.Name)
		}
		return "Already up to date — nothing to upload.", hash, nil
	}

	msg := fmt.Sprintf("Upload %q from %s @ %s", s.Name, device, time.Now().UTC().Format(time.RFC3339))
	if note = strings.TrimSpace(note); note != "" {
		msg += "\n\n" + note
	}
	commit, err := w.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "save-sync", Email: "save-sync@localhost", When: time.Now()},
	})
	if err != nil {
		return "", "", fmt.Errorf("committing: %w", err)
	}

	if err := push(repo, branch, token); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("Uploaded (commit %s).", commit.String()[:7]), commit.String(), nil
}

// push pushes the branch, falling back to a force push if the remote diverged
// (the user chose force-overwrite semantics; history stays recoverable on the
// remote regardless).
func push(repo *git.Repository, branch, token string) error {
	spec := fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch)
	err := repo.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       authFor(token),
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec(spec)},
	})
	if err == nil || errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	if strings.Contains(err.Error(), "non-fast-forward") {
		forceErr := repo.Push(&git.PushOptions{
			RemoteName: "origin",
			Auth:       authFor(token),
			RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec("+" + spec)},
			Force:      true,
		})
		if forceErr == nil || errors.Is(forceErr, git.NoErrAlreadyUpToDate) {
			return nil
		}
		return fmt.Errorf("force pushing: %w", forceErr)
	}
	return fmt.Errorf("pushing: %w", err)
}

// Download fetches the latest remote state and mirrors the clone's <name>/
// subfolder onto the sync's local folder. When wantHash is empty it downloads
// the latest; otherwise it downloads that specific version (commit). Returns a
// human-readable result line and the commit hash now in sync for this game.
func Download(s *Sync, token, wantHash string) (string, string, error) {
	cachePath, err := repoCachePath(s.RepoURL)
	if err != nil {
		return "", "", err
	}
	unlock := lockRepo(cachePath)
	defer unlock()

	repo, err := ensureRepo(s.RepoURL, cachePath, resolveBranch(nil, s.Branch), token)
	if err != nil {
		return "", "", err
	}
	branch := resolveBranch(repo, s.Branch)

	local, err := expandPath(s.LocalPath)
	if err != nil {
		return "", "", err
	}

	if err := fetchOrigin(repo, token); err != nil {
		return "", "", err
	}

	var target plumbing.Hash
	if wantHash == "" {
		hadRemote, err := resetToRemote(repo, branch)
		if err != nil {
			return "", "", err
		}
		if !hadRemote {
			return "", "", fmt.Errorf("remote branch %q has no commits yet — upload from another device first", branch)
		}
		if head, err := repo.Head(); err == nil {
			target = head.Hash()
		}
	} else {
		target = plumbing.NewHash(wantHash)
		if _, err := repo.CommitObject(target); err != nil {
			return "", "", fmt.Errorf("version %s not found in this repo", short(wantHash))
		}
		w, err := repo.Worktree()
		if err != nil {
			return "", "", err
		}
		if err := w.Reset(&git.ResetOptions{Commit: target, Mode: git.HardReset}); err != nil {
			return "", "", fmt.Errorf("checking out version %s: %w", short(wantHash), err)
		}
	}

	subfolder := filepath.Join(cachePath, s.Name)
	if _, err := os.Stat(subfolder); err != nil {
		// Guard against wiping the local folder when there's no save for this
		// name at the chosen commit.
		return "", "", fmt.Errorf("no save named %q found at that version — check the name matches the uploading device", s.Name)
	}
	if err := mirror(subfolder, local); err != nil {
		return "", "", err
	}

	synced, _ := latestCommitForGame(repo, target, s.Name)
	if wantHash == "" {
		return "Downloaded latest save.", synced, nil
	}
	return fmt.Sprintf("Downloaded version %s.", short(wantHash)), synced, nil
}

// short trims a commit hash to 7 chars for display, tolerating short inputs.
func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// History returns the commit list touching the sync's <name>/ subfolder.
func History(s *Sync, token string, limit int) ([]Version, error) {
	cachePath, err := repoCachePath(s.RepoURL)
	if err != nil {
		return nil, err
	}
	unlock := lockRepo(cachePath)
	defer unlock()

	repo, err := ensureRepo(s.RepoURL, cachePath, resolveBranch(nil, s.Branch), token)
	if err != nil {
		return nil, err
	}
	branch := resolveBranch(repo, s.Branch)
	if err := fetchOrigin(repo, token); err != nil {
		return nil, err
	}
	if _, err := resetToRemote(repo, branch); err != nil {
		return nil, err
	}

	prefix := path.Clean(s.Name) + "/"
	iter, err := repo.Log(&git.LogOptions{
		PathFilter: func(p string) bool { return strings.HasPrefix(p, prefix) },
	})
	if err != nil {
		// A fresh repo with no commits reports an error here — treat as empty.
		return []Version{}, nil
	}
	defer iter.Close()

	versions := []Version{}
	for len(versions) < limit {
		c, err := iter.Next()
		if err != nil {
			break
		}
		subject, note := c.Message, ""
		if i := strings.Index(c.Message, "\n\n"); i >= 0 {
			subject = strings.TrimSpace(c.Message[:i])
			note = strings.TrimSpace(c.Message[i+2:])
		}
		versions = append(versions, Version{
			Hash:    c.Hash.String(),
			Short:   c.Hash.String()[:7],
			When:    c.Author.When.Format(time.RFC3339),
			Author:  c.Author.Name,
			Message: subject,
			Note:    note,
		})
	}
	return versions, nil
}

// DiscoverRepo clones/updates a repo and lists the games it contains, so a new
// device can adopt them. It prefers the committed manifest; for repos created
// before manifests existed it falls back to listing top-level directories.
func DiscoverRepo(repoURL, token string) ([]DiscoveredGame, error) {
	cachePath, err := repoCachePath(repoURL)
	if err != nil {
		return nil, err
	}
	unlock := lockRepo(cachePath)
	defer unlock()

	repo, err := ensureRepo(repoURL, cachePath, "main", token)
	if err != nil {
		return nil, err
	}
	branch := resolveBranch(repo, "")
	if err := fetchOrigin(repo, token); err != nil {
		return nil, err
	}
	hadRemote, err := resetToRemote(repo, branch)
	if err != nil {
		return nil, err
	}
	if !hadRemote {
		return nil, fmt.Errorf("repo has no commits yet — upload a save to it first")
	}

	games := []DiscoveredGame{}
	m, err := readManifest(cachePath)
	if err == nil && len(m.Games) > 0 {
		for _, g := range m.Games {
			games = append(games, DiscoveredGame{Name: g.Name, RepoURL: repoURL, PathHints: g.PathHints})
		}
		return games, nil
	}

	// Fallback: treat each top-level directory as a game.
	entries, err := os.ReadDir(cachePath)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		games = append(games, DiscoveredGame{Name: n, RepoURL: repoURL})
	}
	return games, nil
}

// latestCommitForGame returns the hash of the newest commit reachable from
// `from` that touches the game's <name>/ subfolder, or "" if none does.
func latestCommitForGame(repo *git.Repository, from plumbing.Hash, name string) (string, error) {
	prefix := path.Clean(name) + "/"
	iter, err := repo.Log(&git.LogOptions{
		From:       from,
		PathFilter: func(p string) bool { return strings.HasPrefix(p, prefix) },
	})
	if err != nil {
		return "", err
	}
	defer iter.Close()
	c, err := iter.Next()
	if err != nil {
		return "", nil // no commit touches this game
	}
	return c.Hash.String(), nil
}

// RemoteState is the poll result for one sync.
type RemoteState struct {
	Status string `json:"status"` // "in-sync" | "update-available" | "no-remote" | "error"
	Detail string `json:"detail,omitempty"`
}

// RemoteStatuses fetches each repo once and reports, per sync, whether the
// remote has a newer save for that game than this device last synced. It never
// downloads — it only compares commit hashes. `syncs` should be a snapshot
// (value copies) taken under the config lock so this can run in the background.
func RemoteStatuses(syncs []Sync, token string) map[string]RemoteState {
	out := make(map[string]RemoteState, len(syncs))

	// Group syncs by repo so each repo is fetched only once per poll.
	byRepo := map[string][]Sync{}
	order := []string{}
	for _, s := range syncs {
		if _, ok := byRepo[s.RepoURL]; !ok {
			order = append(order, s.RepoURL)
		}
		byRepo[s.RepoURL] = append(byRepo[s.RepoURL], s)
	}

	for _, repoURL := range order {
		group := byRepo[repoURL]
		cachePath, err := repoCachePath(repoURL)
		if err != nil {
			for _, s := range group {
				out[s.ID] = RemoteState{Status: "error", Detail: err.Error()}
			}
			continue
		}

		unlock := lockRepo(cachePath)
		repo, err := ensureRepo(repoURL, cachePath, resolveBranch(nil, group[0].Branch), token)
		if err == nil {
			err = fetchOrigin(repo, token)
		}
		if err != nil {
			unlock()
			for _, s := range group {
				out[s.ID] = RemoteState{Status: "error", Detail: err.Error()}
			}
			continue
		}
		for _, s := range group {
			out[s.ID] = gameRemoteState(repo, s)
		}
		unlock()
	}
	return out
}

// gameRemoteState computes one sync's status against the fetched remote refs.
func gameRemoteState(repo *git.Repository, s Sync) RemoteState {
	branch := resolveBranch(repo, s.Branch)
	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return RemoteState{Status: "no-remote"}
	}
	latest, err := latestCommitForGame(repo, remoteRef.Hash(), s.Name)
	if err != nil {
		return RemoteState{Status: "error", Detail: err.Error()}
	}
	if latest == "" {
		return RemoteState{Status: "no-remote"} // game not present in the repo yet
	}
	if latest != s.LastSyncedRemote {
		// Either this device has never synced the game, or the remote advanced.
		return RemoteState{Status: "update-available"}
	}
	return RemoteState{Status: "in-sync"}
}
