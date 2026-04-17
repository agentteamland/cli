// Package registry fetches and parses the AgentTeamLand team registry.
package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentteamland/cli/internal/config"
)

// Registry is the parsed teams.json from agentteamland/registry.
type Registry struct {
	SchemaVersion int         `json:"schemaVersion"`
	UpdatedAt     string      `json:"updatedAt"`
	Teams         []TeamEntry `json:"teams"`
}

// TeamEntry is one row in the registry.
type TeamEntry struct {
	Name             string   `json:"name"`
	Repo             string   `json:"repo"`
	Description      string   `json:"description"`
	LatestVersion    string   `json:"latestVersion"`
	Author           string   `json:"author"`
	AuthorURL        string   `json:"authorUrl,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Homepage         string   `json:"homepage,omitempty"`
	Status           string   `json:"status"`
	AddedAt          string   `json:"addedAt"`
	DeprecatedReason string   `json:"deprecatedReason,omitempty"`
	ReplacedBy       string   `json:"replacedBy,omitempty"`
}

// Fetch downloads the registry with a 10-second timeout. Returns a parsed Registry.
func Fetch() (*Registry, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(config.RegistryRawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read registry body: %w", err)
	}
	var r Registry
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	if r.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported registry schemaVersion %d", r.SchemaVersion)
	}
	return &r, nil
}

// Find returns the registry entry for a team name, or nil if absent.
func (r *Registry) Find(name string) *TeamEntry {
	for i := range r.Teams {
		if r.Teams[i].Name == name {
			return &r.Teams[i]
		}
	}
	return nil
}

// Search returns all entries whose name, description, or keywords contain the query (case-insensitive).
func (r *Registry) Search(query string) []TeamEntry {
	q := strings.ToLower(query)
	var out []TeamEntry
	for _, t := range r.Teams {
		hit := strings.Contains(strings.ToLower(t.Name), q) ||
			strings.Contains(strings.ToLower(t.Description), q)
		if !hit {
			for _, k := range t.Keywords {
				if strings.Contains(strings.ToLower(k), q) {
					hit = true
					break
				}
			}
		}
		if hit {
			out = append(out, t)
		}
	}
	return out
}

// CloneURL normalizes a TeamEntry.Repo to include .git suffix.
func (t *TeamEntry) CloneURL() string {
	if strings.HasSuffix(t.Repo, ".git") {
		return t.Repo
	}
	return t.Repo + ".git"
}
