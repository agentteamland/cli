// Package commands wires the cobra command tree for the atl CLI.
package commands

import (
	"github.com/agentteamland/cli/internal/config"
	"github.com/spf13/cobra"
)

// NewRoot builds the root `atl` command.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "atl",
		Short: "AgentTeamLand package manager — install teams, scaffold projects, stay in sync",
		Long: `atl is the CLI for the AgentTeamLand ecosystem.

It installs teams (collections of AI agents + skills + rules) from the public
registry (or any git URL) into your current project's .claude/ directory,
tracks what you've installed, and keeps everything up to date.

Examples:
  atl install software-project-team       # registry lookup by name
  atl install agentteamland/starter-extended
  atl install https://github.com/you/your-team.git
  atl list
  atl update
  atl search dotnet

Registry: https://github.com/agentteamland/registry
Docs:     https://github.com/agentteamland`,
		Version:       config.Version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		NewInstall(),
		NewList(),
		NewRemove(),
		NewUpdate(),
		NewSearch(),
		NewSetupHooks(),
		NewSessionStart(),
		NewLearningCapture(),
		NewDocsSync(),
		NewMigrate(),
	)

	return root
}
