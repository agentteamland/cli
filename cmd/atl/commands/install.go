package commands

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewInstall builds the `atl install` command.
func NewInstall() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "install <team-name | git-url | owner/repo>",
		Short: "Install a team into this project's .claude/",
		Long: `Install a team into the current project.

Accepts three forms:

  atl install software-project-team             # registry lookup by short name
  atl install agentteamland/starter-extended    # owner/repo shorthand (GitHub)
  atl install https://github.com/you/team.git   # direct git URL

If the team has an 'extends' declaration, its parent is installed recursively.
Agents/skills/rules are merged with child-overrides-parent semantics; any names
listed in 'excludes' are dropped from the final set.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			target := args[0]
			color.Cyan("→ installing %s ...", target)

			result, err := team.Install(target, team.InstallOptions{
				CWD:     cwd,
				Verbose: verbose,
			})
			if err != nil {
				return err
			}

			// Summary.
			fmt.Println()
			color.Green("✓ installed: %s@%s", result.TopLevelName, result.TopLevelVersion)
			if len(result.Chain) > 1 {
				fmt.Printf("   chain:     %s\n", joinChain(result.Chain))
			}
			fmt.Printf("   effective: %d agents, %d skills, %d rules\n",
				result.AgentsCount, result.SkillsCount, result.RulesCount)
			if len(result.Excluded) > 0 {
				fmt.Printf("   excluded:  %s\n", joinStrings(result.Excluded))
			}
			if result.Status == "community" {
				color.Yellow("   status:    community (not reviewed)")
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print git operations and resolution details")
	return cmd
}

func joinChain(chain []string) string {
	// child → ... → root (reverse the ExtendsChain which is child-first)
	out := ""
	for i := 0; i < len(chain); i++ {
		if i > 0 {
			out += " → "
		}
		out += chain[i]
	}
	return out
}

func joinStrings(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
