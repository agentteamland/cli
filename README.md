<p align="center">
  <img src="https://raw.githubusercontent.com/agentteamland/workspace/main/assets/demo.gif" width="820" alt="atl demo — search, install, inherit"/>
</p>

<h1 align="center">atl</h1>

<p align="center">
  <b>AI agent teams, installed like packages.</b><br/>
  <sub>A package manager CLI for the <a href="https://github.com/agentteamland">AgentTeamLand</a> ecosystem.</sub>
</p>

<p align="center">
  <a href="https://github.com/agentteamland/cli/releases/latest"><img alt="latest release" src="https://img.shields.io/github/v/release/agentteamland/cli?style=flat-square"/></a>
  <a href="https://github.com/agentteamland/cli/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/agentteamland/cli/ci.yml?branch=main&style=flat-square&label=ci"/></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/github/license/agentteamland/cli?style=flat-square"/></a>
</p>

---

## In 30 seconds

```bash
# macOS / Linux
brew install agentteamland/tap/atl

# Windows
scoop bucket add agentteamland https://github.com/agentteamland/scoop-bucket
scoop install atl

# Then, in any project:
atl install software-project-team          # 13 specialized agents arrive
#   → .NET API + Flutter + React + Postgres + RabbitMQ + Redis + Elasticsearch + MinIO
```

`atl` installs **teams** — curated sets of AI agents (plus their skills and rules) — from a public registry or any git URL, into your current project's `.claude/` directory. Teams can extend other teams, override agents by name, or exclude inherited agents they don't need.

## Install

### macOS / Linux (Homebrew — recommended)

```bash
brew install agentteamland/tap/atl
```

or:

```bash
brew tap agentteamland/tap
brew install atl
```

### Windows (PowerShell one-liner — recommended)

```powershell
irm https://raw.githubusercontent.com/agentteamland/cli/main/scripts/install.ps1 | iex
```

Downloads the latest `atl.exe`, installs it to `%LOCALAPPDATA%\Programs\atl\`, adds that directory to your user PATH, and verifies the install. Zero admin rights, zero package-manager prerequisites, works from a fresh Windows machine.

### Windows (Scoop)

For users who already have scoop:

```powershell
scoop bucket add agentteamland https://github.com/agentteamland/scoop-bucket
scoop install atl
```

Don't have scoop? The PowerShell one-liner above is simpler — no need to install a package manager first.

### Windows (winget)

```powershell
winget install agentteamland.atl
```

Available in the Microsoft winget catalog since 2026-04-24. Note that winget may lag one or two releases behind the latest `v*` tag — there is a manual review step on every submission to `microsoft/winget-pkgs`. If you need the absolute latest release, use the PowerShell one-liner or scoop above.

### One-liner fallback (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/agentteamland/cli/main/scripts/install.sh | sh
```

### Manual (any platform)

Download the latest release for your platform from [releases](https://github.com/agentteamland/cli/releases), extract, and move the binary into your `$PATH`. On Windows, the Releases page has `atl_*_windows_{amd64,arm64}.zip` artifacts — extract the `atl.exe` inside to any folder on your PATH.

### Build from source (no Go on host)

```bash
git clone https://github.com/agentteamland/cli.git
cd cli
./scripts/build.sh                   # Dockerized build → bin/atl
```

## Usage

```bash
# Five accepted install sources:
atl install <team>                      # registry lookup by short name
atl install <team>@^1.2.0               # with version constraint
atl install agentteamland/<team>        # owner/repo shorthand (GitHub)
atl install <https-or-ssh-git-url>      # direct URL (works for any host, public or private)
atl install <local-filesystem-path>     # ./rel, /abs, ~/path, file://URL  (atl ≥ 0.1.4)

atl list                                # show installed teams + effective counts
atl install <team> --refresh            # force overwrite of an already-installed
                                        # team (discards local changes)         (atl ≥ 1.0)
atl remove <team>                       # delete project copies; confirm if locally
                                        # modified; cached repo stays            (atl ≥ 1.0)
atl remove <team> --force               # skip the confirm prompt                (atl ≥ 1.0)
atl update                              # pull global cache + auto-refresh unmodified
                                        # project copies; skip modified ones     (atl ≥ 1.0)
atl update --silent-if-clean            # hook-friendly: no output if nothing changed
atl update --check-only                 # dry-run: what WOULD update
atl update --throttle=30m               # skip if last run <30m ago
atl search <keyword>                    # search the public registry
atl setup-hooks                         # install Claude Code hooks for auto-update +
                                        # learning capture (4 hooks: SessionStart,
                                        # UserPromptSubmit, SessionEnd, PreCompact;
                                        # idempotent, opt-in)                   (atl ≥ 0.2.0)
atl setup-hooks --remove                # uninstall the hooks
atl learning-capture --silent-if-empty  # scan session transcript for <!-- learning -->
                                        # markers and print a report; hook-driven,
                                        # silent when no markers found           (atl ≥ 0.2.0)
atl --version
atl --help
```

### Examples

```bash
# From the registry:
atl install software-project-team
atl install design-system-team@^0.4.0

# Inheritance — starter-extended extends software-project-team:
atl install starter-extended            # adds stripe-agent, excludes ux-agent

# Private team from GitHub:
atl install git@github.com:your-org/your-team.git

# Your own local team (no remote needed) — atl ≥ 0.1.4:
cd ~/projects/my-team
git init -b main && git add . && git commit -m "init"
cd ~/projects/some-app
atl install ~/projects/my-team          # absolute path
atl install ./my-team                   # relative path
atl install file:///Users/you/projects/my-team   # explicit file:// URL

# Stay current without manual intervention — atl ≥ 0.1.5:
atl setup-hooks                         # Claude Code auto-checks on every session + every
                                        # prompt (30m throttled). Your teammates who run
                                        # `atl install <team>` once are auto-updated
                                        # every time you ship a new version.
```

Full guide: [docs.agentteamland — Creating a team](https://agentteamland.github.io/docs/authoring/creating-a-team).

## How it works

Every installable **team** is a git repository with a `team.json` at its root declaring its identity, agents/skills/rules, and (optionally) an `extends` parent.

`atl install <name>` walks the inheritance chain (unlimited depth, cycle-detected), merges the effective agent/skill/rule set with *child overrides parent* semantics (and honoring `excludes`), then **copies** every resolved resource from the cached source repos into your project's `.claude/agents/`, `.claude/skills/`, `.claude/rules/`.

Cached source repos live in `~/.claude/repos/agentteamland/` and are shared across all projects on the machine. Each project keeps its own self-contained copy of the resources, so local changes (from `/save-learnings`, hand edits, or `self-updating-learning-loop` auto-grown content) never leak back into the shared cache.

`atl update` keeps copies in sync without manual work: it pulls the global cache, detects whether each project copy still matches its install-time baseline, and **refreshes unmodified copies** silently with the new cache content. Modified copies are left alone, with a one-line hint pointing at `atl install <team> --refresh` for explicit force-overwrite.

## Submit your team to the registry

Open a PR against [agentteamland/registry](https://github.com/agentteamland/registry). Full guide: [CONTRIBUTING.md](https://github.com/agentteamland/registry/blob/main/CONTRIBUTING.md).

## Status

**Current: v1.0.0** — install topology overhaul: every team resource (agents, rules, skills) now installs as a project-local copy. Auto-refresh on `atl update` keeps unmodified copies in sync without manual intervention; modified copies are protected.

  - **Self-contained projects.** Agents and rules join skills in copy-mode install — no more symlinks back to `~/.claude/repos/agentteamland/`. Mutations from `/save-learnings`, hand edits, or future `self-updating-learning-loop` auto-grown content stay isolated to the project; the global cache is never polluted.
  - **One-time auto-migration.** Existing projects on legacy symlink topology auto-convert on the next `atl update`. Single info line surfaces the count; no manual action.
  - **Auto-refresh of unmodified copies on `atl update`.** Three-way hash check (project copy vs. install-time baseline vs. current global cache) decides per-resource: unmodified → silently refresh, modified → skip with a per-team hint pointing at `atl install <team> --refresh`. Keeps the zero-effort auto-update UX symlinks gave for free, but with local-change protection.
  - **Idempotent `atl install`.** Re-running `atl install <team>` on an already-installed team is now a no-op + info line. Pass `--refresh` to force overwrite (with a "Discarding local changes (N modified)" warning when applicable). The legacy "every install silently overwrites" semantic is gone.
  - **Confirm gate on `atl remove`.** Removing a team with locally-modified copies prompts for confirmation; `--force` bypasses. Bonus latent-bug fix: user-authored project-local files (not registered with atl) are now correctly preserved across `atl remove`.

**⚠️ Breaking changes from v0.x:**
1. `atl install <existing-team>` is no longer a silent reinstall. Use `atl install <team> --refresh` for the old behavior, or rely on `atl update` to auto-refresh unmodified copies.
2. `atl remove <team>` may prompt before destructive ops. Use `--force` for non-interactive scripts.

**v0.2.0** — everything in v0.1.5 plus learning-capture automation:
  - **`atl learning-capture`** — new command that scans the Claude Code session transcript for inline `<!-- learning -->` markers and reports what was found. Silent when no markers; produces a short report when markers exist so `/save-learnings` can process them into wiki + memory + doc drafts.
  - **`atl setup-hooks` now installs four hooks** (up from two):
    - `SessionStart` → `atl update --silent-if-clean`
    - `UserPromptSubmit` → `atl update --silent-if-clean --throttle=30m`
    - `SessionEnd` → `atl learning-capture --silent-if-empty`
    - `PreCompact` → `atl learning-capture --silent-if-empty`
  - Boring sessions stay free: learning-capture exits silently when no markers are found — zero tokens, zero noise.
  - Marker-aware `/save-learnings --from-markers` only re-analyzes marked regions of the transcript, not the full conversation. Cheaper than manual save-learnings and more reliable (nothing gets forgotten).
  - Paired with two new core rules: `learning-capture` (inline marker protocol) and `docs-sync` (proactive README / doc-site updates in the same turn as user-facing changes). Ship in `core@1.3.0`.

**v0.1.5** — hook-driven auto-update (`atl setup-hooks`, SessionStart + UserPromptSubmit, throttled self-check).

**v0.1.4** — local-filesystem install (`./path`, `/abs/path`, `~/path`, `file://...`).

**v0.1.x baseline** — install / list / remove / update / search; registry name-resolution; unlimited-depth `extends` with `excludes` + override + circular detection.

**Roadmap:**
- `atl doctor` (diagnostics — includes wiki / docs-sync lint)
- `atl team submit` (interactive registry PR)
- `atl new-project` (team-scoped scaffolder dispatch)
- Version-constraint enforcement (caret/tilde/exact)

## License

MIT.
