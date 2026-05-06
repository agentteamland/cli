package commands

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/configui"
	"github.com/spf13/cobra"
)

func newConfigInit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Walk through a guided welcome + Q&A and write ~/.atl/config.json",
		Long: `First-time setup. Renders a welcome screen, then asks the 9 user-tunable
keys one screen at a time. Defaults are pre-selected; press Enter to keep
each one. The summary screen lets you Save, Edit-a-value, or Cancel.

Cancelling produces no file. Re-run anytime with 'atl config edit' or
'atl config init' (the latter overwrites — confirm before doing so).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GlobalAtlConfigPath()
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("%s already exists. Re-running init will overwrite it.\n", path)
				if !confirmYesNo("Continue?") {
					fmt.Println("aborted.")
					return nil
				}
			}

			result, err := configui.Run(configui.ModeInit, config.DefaultAtlConfig(), path)
			if err != nil {
				return err
			}
			if !result.Saved {
				fmt.Println("init cancelled — no file written.")
				return nil
			}
			if err := config.WriteAtlConfigFile(path, result.Cfg); err != nil {
				return err
			}
			fmt.Printf("init: wrote %s\n", path)
			return nil
		},
	}
	return cmd
}
