package configui

import "github.com/agentteamland/cli/internal/config"

// questionList returns the canonical Q&A sequence for the atl-config v1
// schema. Order matches the brainstorm's "locale first, then group by
// group" rule; within each group, the parent toggle precedes its
// dependent parameters.
//
// `visible` predicates implement skip-when-disabled: a parameter only
// appears when its parent toggle is true.
func questionList() []question {
	return []question{
		{
			id:       "cli.locale",
			label:    "Locale",
			help:     "User-facing language for atl prompts. Currently only English is wired up; Turkish is reserved for the cli-localization brainstorm.",
			template: tplEnum,
			enumOptions: []enumOption{
				{value: "en", desc: "English"},
				{value: "tr", desc: "Türkçe (currently behaves as English; full TR ships later)"},
			},
			get: func(cfg config.AtlConfig) any { return cfg.CLI.Locale },
			set: func(cfg *config.AtlConfig, v any) { cfg.CLI.Locale = v.(string) },
		},
		{
			id:         "autoUpdate.sessionStartEnabled",
			label:      "Auto-update on session start",
			help:       "Run atl session-start (cache pull + previous-transcript marker scan) when Claude Code starts a new session. Recommended for everyday use.",
			template:   tplBool,
			defaultYes: true,
			get:        func(cfg config.AtlConfig) any { return cfg.AutoUpdate.SessionStartEnabled },
			set:        func(cfg *config.AtlConfig, v any) { cfg.AutoUpdate.SessionStartEnabled = v.(bool) },
		},
		{
			id:         "autoUpdate.promptSubmitEnabled",
			label:      "Auto-update on prompt submit",
			help:       "Run atl update (throttled) on every UserPromptSubmit hook. Keeps the cache continuously fresh during long sessions; small per-message git fetch cost.",
			template:   tplBool,
			defaultYes: true,
			get:        func(cfg config.AtlConfig) any { return cfg.AutoUpdate.PromptSubmitEnabled },
			set:        func(cfg *config.AtlConfig, v any) { cfg.AutoUpdate.PromptSubmitEnabled = v.(bool) },
		},
		{
			id:       "autoUpdate.throttleMinutes",
			label:    "Auto-update throttle (minutes)",
			help:     "Minimum time between consecutive prompt-submit auto-updates. Lower = fresher but more git fetches; default 30 minutes is a good balance.",
			template: tplInt,
			minInt:   1,
			maxInt:   1440,
			visible: func(cfg config.AtlConfig) bool {
				return cfg.AutoUpdate.PromptSubmitEnabled
			},
			get: func(cfg config.AtlConfig) any { return cfg.AutoUpdate.ThrottleMinutes },
			set: func(cfg *config.AtlConfig, v any) { cfg.AutoUpdate.ThrottleMinutes = v.(int) },
		},
		{
			id:         "autoUpdate.selfCheckEnabled",
			label:      "Self-check (atl binary version)",
			help:       "Have atl session-start poll GitHub releases for a newer atl binary. Surfaces an upgrade banner when one is available; disable on machines where brew/scoop manage upgrades centrally.",
			template:   tplBool,
			defaultYes: true,
			get:        func(cfg config.AtlConfig) any { return cfg.AutoUpdate.SelfCheckEnabled },
			set:        func(cfg *config.AtlConfig, v any) { cfg.AutoUpdate.SelfCheckEnabled = v.(bool) },
		},
		{
			id:       "autoUpdate.selfCheckHours",
			label:    "Self-check interval (hours)",
			help:     "Minimum hours between consecutive self-check polls. Default 24h matches binary release cadence.",
			template: tplInt,
			minInt:   1,
			maxInt:   168,
			visible: func(cfg config.AtlConfig) bool {
				return cfg.AutoUpdate.SelfCheckEnabled
			},
			get: func(cfg config.AtlConfig) any { return cfg.AutoUpdate.SelfCheckHours },
			set: func(cfg *config.AtlConfig, v any) { cfg.AutoUpdate.SelfCheckHours = v.(int) },
		},
		{
			id:         "learningCapture.autoScanEnabled",
			label:      "Auto-scan transcripts for learning markers",
			help:       "Have atl session-start scan previous-session transcripts for <!-- learning --> markers and report unprocessed ones in the new session's additionalContext.",
			template:   tplBool,
			defaultYes: true,
			get:        func(cfg config.AtlConfig) any { return cfg.LearningCapture.AutoScanEnabled },
			set:        func(cfg *config.AtlConfig, v any) { cfg.LearningCapture.AutoScanEnabled = v.(bool) },
		},
		{
			id:       "learningCapture.firstRunLookbackDays",
			label:    "First-run transcript lookback (days)",
			help:     "On the first scan for a project, how many days of transcripts to consider. Subsequent scans use the per-project lastProcessedAt timestamp.",
			template: tplInt,
			minInt:   1,
			maxInt:   365,
			visible: func(cfg config.AtlConfig) bool {
				return cfg.LearningCapture.AutoScanEnabled
			},
			get: func(cfg config.AtlConfig) any { return cfg.LearningCapture.FirstRunLookbackDays },
			set: func(cfg *config.AtlConfig, v any) { cfg.LearningCapture.FirstRunLookbackDays = v.(int) },
		},
		{
			id:       "brainstorm.markerBulletCap",
			label:    "Brainstorm active marker cap",
			help:     "Maximum number of active brainstorm bullets pinned to a scope's CLAUDE.md/README at once. Older bullets get dropped from the marker block (the brainstorm files themselves are never deleted).",
			template: tplInt,
			minInt:   1,
			maxInt:   50,
			get:      func(cfg config.AtlConfig) any { return cfg.Brainstorm.MarkerBulletCap },
			set:      func(cfg *config.AtlConfig, v any) { cfg.Brainstorm.MarkerBulletCap = v.(int) },
		},
	}
}
