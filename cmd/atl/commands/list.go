package commands

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewList builds the `atl list` command.
func NewList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show teams installed in the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			rows, err := team.ListWithRegistry(cwd)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Println("No teams installed in this project.")
				fmt.Println("Run: atl install <team-name>")
				return nil
			}

			fmt.Printf("Installed teams in %s:\n\n", cwd)
			for _, r := range rows {
				marker := color.GreenString("✓")
				if r.Outdated {
					marker = color.YellowString("↑")
				}
				fmt.Printf("  %s %s@%s", marker, r.Installed.Name, r.Installed.Version)
				if r.Installed.Status == "community" {
					fmt.Print(color.YellowString(" [community]"))
				}
				if r.Outdated {
					fmt.Printf(color.YellowString("  (latest: %s)"), r.Latest)
				}
				fmt.Println()

				if len(r.Installed.ExtendsChain) > 1 {
					fmt.Printf("     extends: %s\n", joinChain(r.Installed.ExtendsChain[1:]))
				}
				fmt.Printf("     effective: %d agents, %d skills, %d rules\n",
					len(r.Installed.Effective["agents"]),
					len(r.Installed.Effective["skills"]),
					len(r.Installed.Effective["rules"]))
			}
			fmt.Println()
			return nil
		},
	}
}
