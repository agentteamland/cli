// Command atl is the AgentTeamLand package manager CLI.
//
//	atl install <name|url>    Install a team (registry lookup or direct URL)
//	atl list                  Show installed teams
//	atl remove <name>         Remove an installed team
//	atl update [name]         Pull updates (all, or one)
//	atl search <keyword>      Search the registry
//	atl version               Show CLI version
package main

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/cmd/atl/commands"
)

func main() {
	if err := commands.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
