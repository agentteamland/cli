package commands

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewUpdate builds the `atl update` command.
//
// With no argument, updates every installed team (and their parents) and refreshes
// all symlinks. With a team name, updates only that team's chain.
func NewUpdate() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "update [team-name]",
		Short: "Pull updates for installed teams and refresh symlinks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			m, err := team.List(cwd)
			if err != nil {
				return err
			}
			if len(m.Teams) == 0 {
				fmt.Println("No teams installed in this project.")
				return nil
			}

			targets := make([]string, 0)
			if len(args) == 0 {
				for _, t := range m.Teams {
					targets = append(targets, t.Name)
				}
			} else {
				name := args[0]
				found := false
				for _, t := range m.Teams {
					if t.Name == name {
						targets = append(targets, name)
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("%q is not installed in this project", name)
				}
			}

			for _, name := range targets {
				color.Cyan("→ updating %s ...", name)
				_, err := team.Install(name, team.InstallOptions{CWD: cwd, Verbose: verbose})
				if err != nil {
					color.Red("  failed: %v", err)
					continue
				}
				color.Green("  ✓ updated")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print git operations")
	return cmd
}
