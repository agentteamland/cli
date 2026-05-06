package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/agentteamland/cli/internal/config"
	"github.com/spf13/cobra"
)

func newConfigShow() *cobra.Command {
	var (
		table   bool
		global  bool
		project bool
	)

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective config (or a single layer) as JSON or a table",
		Long: `Output formats (default JSON unless --table):
  atl config show                # effective config (defaults <- global <- project), pretty JSON
  atl config show --table        # table: key, value, source layer
  atl config show --global       # raw ~/.atl/config.json contents
  atl config show --project      # raw ./.atl/config.json contents

The effective config is the merge stack returned by Load. For inspecting
"what is atl actually using right now?" — the default mode answers it
verbatim.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if global && project {
				return fmt.Errorf("--global and --project are mutually exclusive")
			}
			if global {
				return showSingleLayer(config.GlobalAtlConfigPath(), "global")
			}
			if project {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				root, ok, err := config.FindProjectRoot(cwd)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no project .atl/ directory found above %s", cwd)
				}
				return showSingleLayer(config.ProjectAtlConfigPath(root), "project")
			}
			return showEffective(table)
		},
	}

	cmd.Flags().BoolVar(&table, "table", false, "Render as a table (key / value / source) instead of JSON")
	cmd.Flags().BoolVar(&global, "global", false, "Show only ~/.atl/config.json (raw)")
	cmd.Flags().BoolVar(&project, "project", false, "Show only ./.atl/config.json (raw)")
	return cmd
}

func showSingleLayer(path, label string) error {
	raw, ok, err := config.LoadAtlConfigFile(path)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Printf("# no %s config at %s (defaults apply)\n", label, path)
		return nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func showEffective(asTable bool) error {
	cwd, _ := os.Getwd()
	root, _, _ := config.FindProjectRoot(cwd)
	cfg, err := config.LoadEffectiveAtlConfig(root)
	if err != nil {
		return err
	}

	if !asTable {
		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	// Compute per-key source layer by inspecting each input file's keys.
	sources := computeSources(root)
	rows := flattenConfig(cfg)
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, r := range rows {
		src := sources[r.key]
		if src == "" {
			src = "default"
		}
		fmt.Fprintf(tw, "%s\t%v\t%s\n", r.key, r.value, src)
	}
	return tw.Flush()
}

type configRow struct {
	key   string
	value any
}

func flattenConfig(cfg config.AtlConfig) []configRow {
	return []configRow{
		{"schemaVersion", cfg.SchemaVersion},
		{"cli.locale", cfg.CLI.Locale},
		{"autoUpdate.sessionStartEnabled", cfg.AutoUpdate.SessionStartEnabled},
		{"autoUpdate.promptSubmitEnabled", cfg.AutoUpdate.PromptSubmitEnabled},
		{"autoUpdate.throttleMinutes", cfg.AutoUpdate.ThrottleMinutes},
		{"autoUpdate.selfCheckEnabled", cfg.AutoUpdate.SelfCheckEnabled},
		{"autoUpdate.selfCheckHours", cfg.AutoUpdate.SelfCheckHours},
		{"learningCapture.autoScanEnabled", cfg.LearningCapture.AutoScanEnabled},
		{"learningCapture.firstRunLookbackDays", cfg.LearningCapture.FirstRunLookbackDays},
		{"brainstorm.markerBulletCap", cfg.Brainstorm.MarkerBulletCap},
	}
}

// computeSources returns a map from dotted-key to source layer name
// ("global", "project", or "" for default). For each layer, we walk the
// raw map and tag every key it provides — project overrides global.
func computeSources(projectRoot string) map[string]string {
	out := make(map[string]string)

	if g, ok, err := config.LoadAtlConfigFile(config.GlobalAtlConfigPath()); err == nil && ok {
		for _, k := range collectKeys(g, "") {
			out[k] = "global"
		}
	}
	if projectRoot != "" {
		if p, ok, err := config.LoadAtlConfigFile(config.ProjectAtlConfigPath(projectRoot)); err == nil && ok {
			for _, k := range collectKeys(p, "") {
				out[k] = "project"
			}
		}
	}
	return out
}

func collectKeys(m map[string]any, prefix string) []string {
	var keys []string
	for k, v := range m {
		dotted := k
		if prefix != "" {
			dotted = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			keys = append(keys, collectKeys(nested, dotted)...)
			continue
		}
		keys = append(keys, dotted)
	}
	return keys
}
