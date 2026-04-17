package commands

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewRemove builds the `atl remove` command.
func NewRemove() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <team-name>",
		Short: "Remove a team from the current project (unlinks symlinks; keeps cached repo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := team.Remove(args[0], cwd); err != nil {
				return err
			}
			color.Green("✓ removed %s from project", args[0])
			fmt.Println("  The cached source repository is left in ~/.claude/repos/agentteamland/")
			fmt.Println("  (other projects may still depend on it)")
			return nil
		},
	}
}
