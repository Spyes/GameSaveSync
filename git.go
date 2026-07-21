package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
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
	Message string `json:"message"`
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
// commits the change, and pushes it. Returns a human-readable result line.
func Upload(s *Sync, token, device, note string) (string, error) {
	cachePath, err := repoCachePath(s.RepoURL)
	if err != nil {
		return "", err
	}
	repo, err := ensureRepo(s.RepoURL, cachePath, resolveBranch(nil, s.Branch), token)
	if err != nil {
		return "", err
	}
	branch := resolveBranch(repo, s.Branch)

	if err := fetchOrigin(repo, token); err != nil {
		return "", err
	}
	// Base our commit on the latest remote state so history stays linear.
	if _, err := resetToRemote(repo, branch); err != nil {
		return "", err
	}

	if _, err := os.Stat(s.LocalPath); err != nil {
		return "", fmt.Errorf("local folder %q: %w", s.LocalPath, err)
	}
	subfolder := filepath.Join(cachePath, s.Name)
	if err := mirror(s.LocalPath, subfolder); err != nil {
		return "", err
	}

	w, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := w.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return "", fmt.Errorf("staging changes: %w", err)
	}
	status, err := w.Status()
	if err != nil {
		return "", fmt.Errorf("checking status: %w", err)
	}
	if status.IsClean() {
		return "Already up to date — nothing to upload.", nil
	}

	msg := fmt.Sprintf("Upload %q from %s @ %s", s.Name, device, time.Now().UTC().Format(time.RFC3339))
	if note = strings.TrimSpace(note); note != "" {
		msg += "\n\n" + note
	}
	commit, err := w.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "save-sync", Email: "save-sync@localhost", When: time.Now()},
	})
	if err != nil {
		return "", fmt.Errorf("committing: %w", err)
	}

	if err := push(repo, branch, token); err != nil {
		return "", err
	}
	return fmt.Sprintf("Uploaded (commit %s).", commit.String()[:7]), nil
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
// subfolder onto the sync's local folder. Returns a human-readable result line.
func Download(s *Sync, token string) (string, error) {
	cachePath, err := repoCachePath(s.RepoURL)
	if err != nil {
		return "", err
	}
	repo, err := ensureRepo(s.RepoURL, cachePath, resolveBranch(nil, s.Branch), token)
	if err != nil {
		return "", err
	}
	branch := resolveBranch(repo, s.Branch)

	if err := fetchOrigin(repo, token); err != nil {
		return "", err
	}
	hadRemote, err := resetToRemote(repo, branch)
	if err != nil {
		return "", err
	}
	if !hadRemote {
		return "", fmt.Errorf("remote branch %q has no commits yet — upload from another device first", branch)
	}

	subfolder := filepath.Join(cachePath, s.Name)
	if _, err := os.Stat(subfolder); err != nil {
		// Guard against wiping the local folder when the repo has no save for
		// this name yet.
		return "", fmt.Errorf("no save named %q found in this repo — check the name matches the uploading device", s.Name)
	}
	if err := mirror(subfolder, s.LocalPath); err != nil {
		return "", err
	}
	return "Downloaded latest save.", nil
}

// History returns the commit list touching the sync's <name>/ subfolder.
func History(s *Sync, token string, limit int) ([]Version, error) {
	cachePath, err := repoCachePath(s.RepoURL)
	if err != nil {
		return nil, err
	}
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
		versions = append(versions, Version{
			Hash:    c.Hash.String(),
			Short:   c.Hash.String()[:7],
			When:    c.Author.When.Format(time.RFC3339),
			Author:  c.Author.Name,
			Message: strings.SplitN(c.Message, "\n", 2)[0],
		})
	}
	return versions, nil
}
