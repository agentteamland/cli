package commands

import "github.com/spf13/cobra"

// NewConfig builds the `atl config` subcommand tree.
//
// Subcommands (per workspace .claude/docs/atl-config-system.md):
//
//	atl config init                # first-time welcome + Q&A → write ~/.atl/config.json
//	atl config edit                # Q&A on existing global config
//	atl config edit --project      # Q&A on the project's ./.atl/config.json
//	atl config show                # pretty JSON of effective merged config
//	atl config show --table        # table view: key / value / source
//	atl config show --global       # only ~/.atl/config.json
//	atl config show --project      # only ./.atl/config.json
//	atl config reset               # interactive: confirm before resetting
//	atl config reset --yes         # skip confirmation
//	atl config reset --project     # reset project scope (interactive)
//
// The Q&A flow uses Bubbletea (internal/configui). All writes go through
// config.WriteAtlConfigFile (validation + atomic temp+rename).
func NewConfig() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect, edit, or reset atl's user/project configuration",
		Long: `Manage atl's own configuration (~/.atl/config.json), separate from Claude
Code's settings (~/.claude/settings.json).

The 9 user-tunable keys cover CLI locale, auto-update behavior, learning-
capture settings, and the brainstorm marker bullet cap. See
https://agentteamland.github.io/docs (when published) for the per-key
reference and the keystone test ("config vs rule vs hardcode").

Decision context: workspace .claude/docs/atl-config-system.md.`,
	}

	cmd.AddCommand(
		newConfigInit(),
		newConfigEdit(),
		newConfigShow(),
		newConfigReset(),
	)

	return cmd
}
