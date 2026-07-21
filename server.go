package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"strings"
)

//go:embed web
var webFS embed.FS

// Server holds the shared config and serves the API + embedded UI.
type Server struct {
	cfg *Config
}

func newSubID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func deviceName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown-device"
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Embedded static UI (index.html at root).
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/settings/token", s.handleToken)
	mux.HandleFunc("/api/syncs", s.handleSyncs)     // GET list, POST add
	mux.HandleFunc("/api/syncs/", s.handleSyncItem) // /{id}, /{id}/upload, /{id}/download, /{id}/history
	mux.HandleFunc("/api/discover/repo", s.handleDiscoverRepo)
	mux.HandleFunc("/api/discover/github", s.handleDiscoverGitHub)

	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"device":   deviceName(),
		"os":       runtime.GOOS,
		"hasToken": s.cfg.GithubToken != "",
	})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.cfg.mu.Lock()
	s.cfg.GithubToken = strings.TrimSpace(body.Token)
	err := s.cfg.save()
	s.cfg.mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"hasToken": s.cfg.GithubToken != ""})
}

func (s *Server) handleSyncs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Syncs)
	case http.MethodPost:
		var in Sync
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		in.RepoURL = strings.TrimSpace(in.RepoURL)
		in.LocalPath = strings.TrimSpace(in.LocalPath)
		in.Branch = strings.TrimSpace(in.Branch)
		if in.Name == "" || in.RepoURL == "" || in.LocalPath == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("name, repoUrl and localPath are required"))
			return
		}
		if strings.ContainsAny(in.Name, `/\`) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("name cannot contain slashes"))
			return
		}
		s.cfg.mu.Lock()
		for _, existing := range s.cfg.Syncs {
			if existing.RepoURL == in.RepoURL && existing.Name == in.Name {
				s.cfg.mu.Unlock()
				writeErr(w, http.StatusConflict, fmt.Errorf("a save named %q for this repo already exists", in.Name))
				return
			}
		}
		in.ID = newSubID()
		s.cfg.Syncs = append(s.cfg.Syncs, &in)
		err := s.cfg.save()
		s.cfg.mu.Unlock()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, &in)
	default:
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("unsupported method"))
	}
}

// handleSyncItem routes /api/syncs/{id} and /{id}/{action}.
func (s *Server) handleSyncItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/syncs/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if id == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing sync id"))
		return
	}

	s.cfg.mu.Lock()
	sync := s.cfg.findSync(id)
	s.cfg.mu.Unlock()
	if sync == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("sync not found"))
		return
	}

	switch action {
	case "":
		s.handleSyncCRUD(w, r, sync)
	case "upload":
		s.handleUpload(w, r, sync)
	case "download":
		s.handleDownload(w, r, sync)
	case "history":
		s.handleHistory(w, r, sync)
	default:
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown action %q", action))
	}
}

func (s *Server) handleSyncCRUD(w http.ResponseWriter, r *http.Request, sync *Sync) {
	switch r.Method {
	case http.MethodPut:
		var in Sync
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.cfg.mu.Lock()
		if v := strings.TrimSpace(in.Name); v != "" && !strings.ContainsAny(v, `/\`) {
			sync.Name = v
		}
		if v := strings.TrimSpace(in.RepoURL); v != "" {
			sync.RepoURL = v
		}
		if v := strings.TrimSpace(in.LocalPath); v != "" {
			sync.LocalPath = v
		}
		sync.Branch = strings.TrimSpace(in.Branch)
		err := s.cfg.save()
		s.cfg.mu.Unlock()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, sync)
	case http.MethodDelete:
		s.cfg.mu.Lock()
		kept := s.cfg.Syncs[:0]
		for _, x := range s.cfg.Syncs {
			if x.ID != sync.ID {
				kept = append(kept, x)
			}
		}
		s.cfg.Syncs = kept
		err := s.cfg.save()
		s.cfg.mu.Unlock()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("unsupported method"))
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, sync *Sync) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // note is optional

	result, err := Upload(sync, s.cfg.GithubToken, deviceName(), body.Note)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAction(sync, result)
	writeJSON(w, http.StatusOK, map[string]string{"result": result})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, sync *Sync) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	result, err := Download(sync, s.cfg.GithubToken)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAction(sync, result)
	writeJSON(w, http.StatusOK, map[string]string{"result": result})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, sync *Sync) {
	versions, err := History(sync, s.cfg.GithubToken, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// recordAction stores a short status line on the sync for display.
func (s *Server) recordAction(sync *Sync, result string) {
	s.cfg.mu.Lock()
	sync.LastAction = result
	_ = s.cfg.save()
	s.cfg.mu.Unlock()
}

// markConfigured flags games that already exist locally (matched by repoUrl +
// name) so the UI can grey them out.
func (s *Server) markConfigured(games []DiscoveredGame) []DiscoveredGame {
	s.cfg.mu.Lock()
	defer s.cfg.mu.Unlock()
	for i := range games {
		for _, existing := range s.cfg.Syncs {
			if existing.RepoURL == games[i].RepoURL && existing.Name == games[i].Name {
				games[i].AlreadyConfigured = true
				break
			}
		}
	}
	return games
}

func (s *Server) handleDiscoverRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var body struct {
		RepoURL string `json:"repoUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	body.RepoURL = strings.TrimSpace(body.RepoURL)
	if body.RepoURL == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("repoUrl is required"))
		return
	}
	games, err := DiscoverRepo(body.RepoURL, s.cfg.GithubToken)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"games": s.markConfigured(games)})
}

func (s *Server) handleDiscoverGitHub(w http.ResponseWriter, r *http.Request) {
	games, failed, err := DiscoverGitHub(s.cfg.GithubToken)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := map[string]any{"games": s.markConfigured(games)}
	if failed > 0 {
		resp["warning"] = fmt.Sprintf("%d repo(s) couldn't be checked (network error or GitHub rate limit) — this list may be incomplete.", failed)
	}
	writeJSON(w, http.StatusOK, resp)
}
