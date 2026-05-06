package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/agentteamland/cli/internal/config"
	"github.com/spf13/cobra"
)

func newConfigReset() *cobra.Command {
	var (
		yes     bool
		project bool
	)

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset config to schema defaults (interactive by default)",
		Long: `Without --project, rewrites ~/.atl/config.json with schema defaults.
With --project, deletes the project's ./.atl/config.json (project files are
overlays — "reset" means removing the override layer entirely).

Without --yes, a y/N prompt confirms before any destructive action. Use
--yes in scripts or CI.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project {
				return resetProject(yes)
			}
			return resetGlobal(yes)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&project, "project", false, "Reset the project's ./.atl/config.json (deletes the file)")
	return cmd
}

func resetGlobal(yes bool) error {
	path := config.GlobalAtlConfigPath()
	if !yes {
		fmt.Printf("This will overwrite %s with schema defaults.\n", path)
		if !confirmYesNo("Continue?") {
			fmt.Println("aborted.")
			return nil
		}
	}
	cfg := config.DefaultAtlConfig()
	if err := config.WriteAtlConfigFile(path, cfg); err != nil {
		return err
	}
	fmt.Printf("reset: wrote schema defaults to %s\n", path)
	return nil
}

func resetProject(yes bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, ok, err := config.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no project .atl/ directory found above %s — nothing to reset", cwd)
	}
	path := config.ProjectAtlConfigPath(root)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("reset: %s does not exist — nothing to do\n", path)
		return nil
	}

	if !yes {
		fmt.Printf("This will delete %s (project overlay removed; effective config falls back to global + defaults).\n", path)
		if !confirmYesNo("Continue?") {
			fmt.Println("aborted.")
			return nil
		}
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	fmt.Printf("reset: removed %s\n", path)
	return nil
}

func confirmYesNo(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}
