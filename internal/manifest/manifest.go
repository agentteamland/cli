// Package manifest handles parsing and validation of team.json files.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// TeamManifest is the parsed representation of a team.json file.
// Mirrors schemas/team.schema.json in the core repo.
type TeamManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Description   string            `json:"description"`
	Author        *Author           `json:"author,omitempty"`
	License       string            `json:"license,omitempty"`
	Keywords      []string          `json:"keywords,omitempty"`
	Repository    string            `json:"repository,omitempty"`
	Homepage      string            `json:"homepage,omitempty"`
	Agents        []Item            `json:"agents,omitempty"`
	Skills        []Item            `json:"skills,omitempty"`
	Rules         []Item            `json:"rules,omitempty"`
	Extends       *string           `json:"extends"`
	Excludes      []string          `json:"excludes,omitempty"`
	Dependencies  map[string]string `json:"dependencies,omitempty"`
	Requires      map[string]string `json:"requires,omitempty"`
}

// Author of a team.
type Author struct {
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// Item is a named entry (agent, skill, or rule) in a team manifest.
type Item struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// Read parses a team.json file from disk.
func Read(path string) (*TeamManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read team.json at %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses team.json bytes.
func Parse(data []byte) (*TeamManifest, error) {
	var m TeamManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse team.json: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReadFromRepo reads team.json from a cached repo directory.
func ReadFromRepo(repoDir string) (*TeamManifest, error) {
	return Read(filepath.Join(repoDir, "team.json"))
}

// Validate performs basic structural validation. Full JSON Schema validation is
// delegated to CI; this is the runtime sanity check.
func (m *TeamManifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schemaVersion %d (expected 1)", m.SchemaVersion)
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("invalid name %q (must be kebab-case, 3-40 chars)", m.Name)
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("invalid version %q (must be SemVer)", m.Version)
	}
	if len(m.Description) < 10 || len(m.Description) > 200 {
		return fmt.Errorf("description must be 10-200 chars (got %d)", len(m.Description))
	}
	if m.Extends != nil && *m.Extends != "" && !extendsRe.MatchString(*m.Extends) {
		return fmt.Errorf("invalid extends %q (expected name@^version)", *m.Extends)
	}
	return nil
}

// ParseExtends splits "<name>@<constraint>" into its parts.
// Returns ("", "", false) if the manifest has no extends.
func (m *TeamManifest) ParseExtends() (name, constraint string, ok bool) {
	if m.Extends == nil || *m.Extends == "" {
		return "", "", false
	}
	match := extendsRe.FindStringSubmatch(*m.Extends)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

var (
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)
	semverRe  = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-.]+)?(?:\+[0-9A-Za-z-.]+)?$`)
	extendsRe = regexp.MustCompile(`^([a-z][a-z0-9-]*)@((?:\^|~|>=|<=|>|<|=)?[0-9].*)$`)
)
