package commands

import (
	"fmt"

	"github.com/agentteamland/cli/internal/registry"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewSearch builds the `atl search` command.
func NewSearch() *cobra.Command {
	return &cobra.Command{
		Use:   "search <keyword>",
		Short: "Search the AgentTeamLand registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := registry.Fetch()
			if err != nil {
				return err
			}
			hits := reg.Search(args[0])
			if len(hits) == 0 {
				fmt.Printf("No teams matching %q.\n", args[0])
				fmt.Println("Full catalog: https://github.com/agentteamland/registry")
				return nil
			}
			fmt.Printf("Found %d team(s) matching %q:\n\n", len(hits), args[0])
			for _, e := range hits {
				statusBadge := ""
				switch e.Status {
				case "verified":
					statusBadge = color.GreenString(" [verified]")
				case "community":
					statusBadge = color.YellowString(" [community]")
				case "deprecated":
					statusBadge = color.RedString(" [deprecated]")
				}
				fmt.Printf("  %s@%s%s\n", e.Name, e.LatestVersion, statusBadge)
				fmt.Printf("    %s\n", e.Description)
				fmt.Printf("    %s\n", e.Repo)
				if len(e.Keywords) > 0 {
					fmt.Printf("    keywords: %s\n", joinStrings(e.Keywords))
				}
				fmt.Println()
			}
			return nil
		},
	}
}
