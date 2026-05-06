package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/configui"
	"github.com/spf13/cobra"
)

func newConfigEdit() *cobra.Command {
	var projectFlag bool

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit ~/.atl/config.json (or ./.atl/config.json with --project) via the Q&A flow",
		Long: `Without --project, edits the global config at ~/.atl/config.json. The
file must exist (run 'atl config init' first if not).

With --project, edits the project's ./.atl/config.json. If the project
file does not exist yet, defaults are used as the starting point and a
new file is created on Save.

Cancelling leaves the existing file untouched.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			var existing config.AtlConfig
			var err error

			if projectFlag {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				root, ok, err := config.FindProjectRoot(cwd)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no project .atl/ directory found above %s — create one or drop --project", cwd)
				}
				path = config.ProjectAtlConfigPath(root)
			} else {
				path = config.GlobalAtlConfigPath()
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("%s does not exist. Run 'atl config init' first", path)
				}
			}

			if existing, err = loadOrDefault(path); err != nil {
				return err
			}

			result, err := configui.Run(configui.ModeEdit, existing, path)
			if err != nil {
				return err
			}
			if !result.Saved {
				fmt.Println("edit cancelled — file unchanged.")
				return nil
			}
			if err := config.WriteAtlConfigFile(path, result.Cfg); err != nil {
				return err
			}
			fmt.Printf("edit: wrote %s\n", path)
			return nil
		},
	}

	cmd.Flags().BoolVar(&projectFlag, "project", false, "Edit the project's ./.atl/config.json instead of the global file")
	return cmd
}

// loadOrDefault reads the config.json at path (must already pass shape
// and schemaVersion checks), and returns it as a fully-populated typed
// AtlConfig with missing fields filled from schema defaults.
//
// Missing file → schema defaults (callers may detect "first edit" by
// passing through to the Q&A's edit-of-defaults flow).
func loadOrDefault(path string) (config.AtlConfig, error) {
	raw, ok, err := config.LoadAtlConfigFile(path)
	if err != nil {
		return config.AtlConfig{}, err
	}
	if !ok {
		return config.DefaultAtlConfig(), nil
	}

	// Merge raw onto schema defaults so any field omitted in the file
	// is populated with the schema's default rather than Go's zero.
	defaultsBytes, err := json.Marshal(config.DefaultAtlConfig())
	if err != nil {
		return config.AtlConfig{}, err
	}
	var defaultsMap map[string]any
	if err := json.Unmarshal(defaultsBytes, &defaultsMap); err != nil {
		return config.AtlConfig{}, err
	}
	merged := deepMergeMapEdit(defaultsMap, raw)

	mergedBytes, err := json.Marshal(merged)
	if err != nil {
		return config.AtlConfig{}, err
	}
	var out config.AtlConfig
	if err := json.Unmarshal(mergedBytes, &out); err != nil {
		return config.AtlConfig{}, err
	}
	return out, nil
}

// deepMergeMapEdit overlays b on a recursively. Same semantics as the
// internal deepMergeMaps in internal/config — duplicated here to avoid
// re-exporting an internal helper.
func deepMergeMapEdit(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if existing, ok := out[k]; ok {
			if em, eok := existing.(map[string]any); eok {
				if vm, vok := v.(map[string]any); vok {
					out[k] = deepMergeMapEdit(em, vm)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}
