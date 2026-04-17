package team

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/registry"
)

// List returns the team-installs manifest for the given project directory.
func List(cwd string) (*TeamInstallsManifest, error) {
	path := config.TeamInstallsManifest(cwd)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TeamInstallsManifest{}, nil
		}
		return nil, fmt.Errorf("read .team-installs.json: %w", err)
	}
	var m TeamInstallsManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse .team-installs.json: %w", err)
	}
	return &m, nil
}

// ListWithRegistry returns the installed teams plus a flag indicating
// whether each one is behind the registry's latestVersion.
type InstalledTeamStatus struct {
	Installed InstalledTeam
	Latest    string // empty if registry lookup unavailable
	Outdated  bool
}

func ListWithRegistry(cwd string) ([]InstalledTeamStatus, error) {
	m, err := List(cwd)
	if err != nil {
		return nil, err
	}
	reg, _ := registry.Fetch() // best-effort

	out := make([]InstalledTeamStatus, 0, len(m.Teams))
	for _, t := range m.Teams {
		row := InstalledTeamStatus{Installed: t}
		if reg != nil {
			if e := reg.Find(t.Name); e != nil {
				row.Latest = e.LatestVersion
				row.Outdated = t.Version != "" && !versionEqual(t.Version, e.LatestVersion)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func versionEqual(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}
