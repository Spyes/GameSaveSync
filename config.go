package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Sync is a single configured save folder <-> GitHub repo mapping.
// Name doubles as the subfolder inside the repo, so one repo can hold
// many games (each under its own named subfolder) or one repo per game.
type Sync struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RepoURL    string `json:"repoUrl"`
	LocalPath  string `json:"localPath"`
	Branch     string `json:"branch"`
	LastAction string `json:"lastAction,omitempty"` // human-readable status of the last upload/download
	// LastSyncedRemote is the commit hash (touching this game's subfolder) that
	// this device last uploaded or downloaded. Polling compares it against the
	// remote tip to decide whether an update is available. Never triggers an
	// automatic download — sync stays manual.
	LastSyncedRemote string `json:"lastSyncedRemote,omitempty"`
}

// Config is the whole persisted state: server port, GitHub token, and syncs.
type Config struct {
	Port        int     `json:"port"`
	GithubToken string  `json:"githubToken"`
	Syncs       []*Sync `json:"syncs"`

	mu   sync.Mutex `json:"-"`
	path string     `json:"-"`
}

const defaultPort = 8787

// expandPath resolves a user-entered local folder: it expands a leading "~"
// to the home directory and requires the result to be absolute, so save files
// can never be written relative to wherever the binary happens to run.
func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve home directory: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("please use an absolute path (e.g. /home/deck/... or C:\\Users\\you\\...), got %q", p)
	}
	return filepath.Clean(p), nil
}

// configDir is where config.json + managed clones live. It honors an explicit
// SAVESYNC_CONFIG_DIR override (used by the Decky plugin, which runs as a
// different user than the desktop app and must point at the deck user's dir);
// otherwise it defaults to <UserConfigDir>/save-sync.
func configDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SAVESYNC_CONFIG_DIR")); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "save-sync"), nil
}

// reposDir is where managed clones live, keyed by a hash of the repo URL.
func reposDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repos"), nil
}

// repoCachePath returns the local clone directory for a given repo URL.
func repoCachePath(repoURL string) (string, error) {
	base, err := reposDir()
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(repoURL))
	return filepath.Join(base, hex.EncodeToString(sum[:])), nil
}

// LoadConfig reads config.json, creating a default if it does not exist.
func LoadConfig() (*Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating config dir: %w", err)
	}
	path := filepath.Join(dir, "config.json")

	cfg := &Config{Port: defaultPort, path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := cfg.save(); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	cfg.path = path
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	return cfg, nil
}

// save writes config.json atomically with 0600 perms (it holds the PAT).
// Callers must hold cfg.mu.
func (c *Config) save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return os.Rename(tmp, c.path)
}

// Save persists the config under lock.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.save()
}

func (c *Config) findSync(id string) *Sync {
	for _, s := range c.Syncs {
		if s.ID == id {
			return s
		}
	}
	return nil
}
