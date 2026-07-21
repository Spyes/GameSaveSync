package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// manifestFile is the self-describing index committed at each repo's root so
// other devices can discover which games live in the repo.
const manifestFile = ".savesync.json"

// GameEntry describes one game (one <name>/ subfolder) in a repo.
type GameEntry struct {
	Name string `json:"name"`
	// PathHints maps GOOS ("windows"/"linux"/"darwin") to the local folder the
	// uploading device used. Convenience for prefilling the folder field on
	// adopt — never authoritative.
	PathHints map[string]string `json:"pathHints,omitempty"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
	UpdatedBy string            `json:"updatedBy,omitempty"`
}

// Manifest is the contents of .savesync.json.
type Manifest struct {
	Version int          `json:"version"`
	Games   []*GameEntry `json:"games"`
}

// DiscoveredGame is a game surfaced by a discovery flow (repo import or GitHub
// scan), ready to be adopted by choosing a local folder.
type DiscoveredGame struct {
	Name              string            `json:"name"`
	RepoURL           string            `json:"repoUrl"`
	PathHints         map[string]string `json:"pathHints,omitempty"`
	AlreadyConfigured bool              `json:"alreadyConfigured"`
}

// sanitize drops nil or nameless entries so downstream consumers can iterate
// safely (a remote manifest may contain e.g. `"games":[null]`).
func (m *Manifest) sanitize() {
	games := m.Games[:0]
	for _, g := range m.Games {
		if g != nil && g.Name != "" {
			games = append(games, g)
		}
	}
	m.Games = games
	if m.Version == 0 {
		m.Version = 1
	}
}

// readManifest loads the manifest from a clone's working tree, returning an
// empty (version 1) manifest when the file is absent. A corrupt manifest
// returns an error so callers do NOT overwrite it — that would drop other
// devices' games.
func readManifest(cachePath string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(cachePath, manifestFile))
	if os.IsNotExist(err) {
		return &Manifest{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %w", manifestFile, err)
	}
	m.sanitize()
	return &m, nil
}

// writeManifest writes the manifest into the clone's working tree so the next
// commit picks it up.
func writeManifest(cachePath string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cachePath, manifestFile), append(data, '\n'), 0o644)
}

// parseManifest decodes raw .savesync.json bytes (used by the GitHub API path).
func parseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	m.sanitize()
	return &m, nil
}

// upsertGame inserts or updates the entry for name, recording this device's
// OS-specific local path as a hint.
func upsertGame(m *Manifest, name, goos, localPath, device string) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, g := range m.Games {
		if g.Name == name {
			if g.PathHints == nil {
				g.PathHints = map[string]string{}
			}
			g.PathHints[goos] = localPath
			g.UpdatedAt = now
			g.UpdatedBy = device
			return
		}
	}
	m.Games = append(m.Games, &GameEntry{
		Name:      name,
		PathHints: map[string]string{goos: localPath},
		UpdatedAt: now,
		UpdatedBy: device,
	})
}
