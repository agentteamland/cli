package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewRemove builds the `atl remove` command.
func NewRemove() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "remove <team-name>",
		Short: "Remove a team from the current project (deletes copies; keeps cached repo)",
		Long: `Remove an installed team from the current project.

Deletes the team's agents/rules/skills copies from .claude/. The cached
source repo at ~/.claude/repos/agentteamland/<team>/ is left intact (other
projects may still depend on it).

Safety: if any of the team's project copies have local modifications
(self-updating-learning-loop mutations or hand edits), removal stops with
an interactive confirm prompt. Pass --force to skip the prompt and remove
unconditionally.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			teamName := args[0]

			// Modified-content gate: warn before destruction unless --force.
			if !force {
				modified := team.CountLocalModifications(cwd, teamName)
				if modified > 0 {
					color.Yellow("⚠ %s has %d local modification%s in this project.",
						teamName, modified, plural(modified))
					fmt.Println("  Removing will discard those changes (the cached source repo is unaffected).")
					if !confirm("  Continue? [y/N]: ") {
						color.Red("✗ aborted")
						return nil
					}
				}
			}

			if err := team.Remove(teamName, cwd); err != nil {
				return err
			}
			color.Green("✓ removed %s from project", teamName)
			fmt.Println("  The cached source repository is left in ~/.claude/repos/agentteamland/")
			fmt.Println("  (other projects may still depend on it)")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip the confirm prompt for projects with local modifications")
	return cmd
}

// confirm reads a single line from stdin and returns true only when the
// user explicitly enters y / yes (case-insensitive). Empty input or any
// other answer is treated as "no" — the safe default for destructive
// operations.
//
// Non-interactive stdin (e.g. piped CI usage) returns false: in that case
// the caller should pass --force to express explicit intent.
func confirm(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
