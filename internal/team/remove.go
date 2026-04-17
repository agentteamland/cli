package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentteamland/cli/internal/config"
)

// Remove deletes all symlinks belonging to the given team and removes it from
// .team-installs.json. The cached repo directory is left intact (may be shared
// with other projects).
func Remove(name, cwd string) error {
	m, err := List(cwd)
	if err != nil {
		return err
	}

	var target *InstalledTeam
	idx := -1
	for i := range m.Teams {
		if m.Teams[i].Name == name {
			target = &m.Teams[i]
			idx = i
			break
		}
	}
	if target == nil {
		return fmt.Errorf("team %q is not installed in this project", name)
	}

	projectClaude := config.ProjectClaudeDir(cwd)

	// Remove agent symlinks.
	for _, a := range target.Effective["agents"] {
		dst := filepath.Join(projectClaude, "agents", a+".md")
		_ = os.Remove(dst)
	}
	for _, s := range target.Effective["skills"] {
		dst := filepath.Join(projectClaude, "skills", s)
		_ = os.Remove(dst)
	}
	for _, r := range target.Effective["rules"] {
		dst := filepath.Join(projectClaude, "rules", r+".md")
		_ = os.Remove(dst)
	}

	// Remove from manifest.
	m.Teams = append(m.Teams[:idx], m.Teams[idx+1:]...)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(config.TeamInstallsManifest(cwd), data, 0o644)
}
