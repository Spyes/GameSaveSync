package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const githubAPI = "https://api.github.com"

// ghDo performs an authenticated GitHub API request and decodes the JSON body
// into out. It returns the status code so callers can treat 404 as "absent".
func ghDo(token, path string, out any) (int, error) {
	req, err := http.NewRequest(http.MethodGet, githubAPI+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp.StatusCode, fmt.Errorf("GitHub API %s: %s (check your token's scopes)", path, resp.Status)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

type ghRepo struct {
	FullName string `json:"full_name"` // owner/name
	CloneURL string `json:"clone_url"`
}

type ghContent struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// ghListRepos returns every repo the token can see, following pagination.
func ghListRepos(token string) ([]ghRepo, error) {
	var all []ghRepo
	for page := 1; page <= 20; page++ { // hard cap: 2000 repos
		var batch []ghRepo
		code, err := ghDo(token, "/user/repos?per_page=100&page="+strconv.Itoa(page), &batch)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf("listing repos: GitHub returned %d", code)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// ghGetManifest fetches and decodes .savesync.json from a repo, returning
// (nil, nil) when the repo has no manifest.
func ghGetManifest(token, fullName string) (*Manifest, error) {
	var c ghContent
	code, err := ghDo(token, "/repos/"+fullName+"/contents/"+manifestFile, &c)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("fetching manifest from %s: GitHub returned %d", fullName, code)
	}
	if c.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected manifest encoding %q", c.Encoding)
	}
	// GitHub wraps base64 content in newlines.
	data, err := base64.StdEncoding.DecodeString(stripNewlines(c.Content))
	if err != nil {
		return nil, err
	}
	return parseManifest(data)
}

func stripNewlines(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' && s[i] != '\r' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// DiscoverGitHub scans every repo the token can see for a save-sync manifest
// and returns all games across all of them. Repos without a manifest are
// skipped. Manifest probes run with bounded concurrency.
func DiscoverGitHub(token string) ([]DiscoveredGame, error) {
	if token == "" {
		return nil, fmt.Errorf("set a GitHub token first")
	}
	repos, err := ghListRepos(token)
	if err != nil {
		return nil, err
	}

	type result struct {
		games []DiscoveredGame
	}
	results := make([]result, len(repos))

	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func(i int, repo ghRepo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			m, err := ghGetManifest(token, repo.FullName)
			if err != nil || m == nil {
				return // probe error or no manifest — skip this repo
			}
			for _, g := range m.Games {
				results[i].games = append(results[i].games, DiscoveredGame{
					Name:      g.Name,
					RepoURL:   repo.CloneURL,
					PathHints: g.PathHints,
				})
			}
		}(i, repo)
	}
	wg.Wait()

	games := []DiscoveredGame{}
	for _, r := range results {
		games = append(games, r.games...)
	}
	return games, nil
}
