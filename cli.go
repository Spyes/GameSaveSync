package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
)

// isCLICommand reports whether the first argument selects machine-readable CLI
// mode (used by the Decky plugin) rather than starting the web server.
func isCLICommand(cmd string) bool {
	switch cmd {
	case "list", "status", "upload", "download", "history":
		return true
	}
	return false
}

// runCLI executes a subcommand and writes JSON to out. It returns a process
// exit code; errors are emitted as {"error": "..."} with a non-zero code so
// callers (the plugin) can distinguish failure without parsing stderr.
func runCLI(args []string, out io.Writer) int {
	cfg, err := LoadConfig()
	if err != nil {
		return cliErr(out, err)
	}

	switch args[0] {
	case "list":
		type item struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			RepoURL   string `json:"repoUrl"`
			LocalPath string `json:"localPath"`
		}
		items := []item{}
		for _, s := range cfg.Syncs {
			items = append(items, item{s.ID, s.Name, s.RepoURL, s.LocalPath})
		}
		return cliJSON(out, items)

	case "status":
		snapshot := make([]Sync, len(cfg.Syncs))
		for i, s := range cfg.Syncs {
			snapshot[i] = *s
		}
		return cliJSON(out, RemoteStatuses(snapshot, cfg.GithubToken))

	case "upload":
		// Contract: upload <id> [--note "..."] — id is the fixed first arg so
		// flags after it parse correctly (Go's flag stops at the first non-flag).
		s, err := requireSync(cfg, arg(args, 1))
		if err != nil {
			return cliErr(out, err)
		}
		fs := flag.NewFlagSet("upload", flag.ContinueOnError)
		note := fs.String("note", "", "note to attach to this version")
		if err := fs.Parse(args[2:]); err != nil {
			return cliErr(out, err)
		}
		result, hash, err := Upload(s, cfg.GithubToken, deviceName(), *note)
		if err != nil {
			return cliErr(out, err)
		}
		persist(cfg, s, result, hash)
		return cliJSON(out, map[string]string{"result": result})

	case "download":
		// Contract: download <id> [--hash <commit>]
		s, err := requireSync(cfg, arg(args, 1))
		if err != nil {
			return cliErr(out, err)
		}
		fs := flag.NewFlagSet("download", flag.ContinueOnError)
		hash := fs.String("hash", "", "specific version (commit) to download")
		if err := fs.Parse(args[2:]); err != nil {
			return cliErr(out, err)
		}
		result, synced, err := Download(s, cfg.GithubToken, strings.TrimSpace(*hash))
		if err != nil {
			return cliErr(out, err)
		}
		persist(cfg, s, result, synced)
		return cliJSON(out, map[string]string{"result": result})

	case "history":
		s, err := requireSync(cfg, arg(args, 1))
		if err != nil {
			return cliErr(out, err)
		}
		versions, err := History(s, cfg.GithubToken, 100)
		if err != nil {
			return cliErr(out, err)
		}
		latest := ""
		if len(versions) > 0 {
			latest = versions[0].Hash
		}
		return cliJSON(out, map[string]any{
			"versions": versions,
			"current":  s.LastSyncedRemote,
			"latest":   latest,
		})
	}
	return cliErr(out, fmt.Errorf("unknown command %q", args[0]))
}

// requireSync resolves a sync by id, falling back to name.
func requireSync(cfg *Config, ref string) (*Sync, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("a sync id or name is required")
	}
	for _, s := range cfg.Syncs {
		if s.ID == ref {
			return s, nil
		}
	}
	for _, s := range cfg.Syncs {
		if s.Name == ref {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no sync with id or name %q", ref)
}

// persist records the result of an upload/download the same way the web server
// does, so the update-available check stays accurate across both front-ends.
func persist(cfg *Config, s *Sync, result, hash string) {
	s.LastAction = result
	if hash != "" {
		s.LastSyncedRemote = hash
	}
	_ = cfg.Save()
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func cliJSON(out io.Writer, v any) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return 0
}

func cliErr(out io.Writer, err error) int {
	_ = json.NewEncoder(out).Encode(map[string]string{"error": err.Error()})
	return 1
}
