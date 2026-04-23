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

### Windows (Scoop — recommended)

```powershell
scoop bucket add agentteamland https://github.com/agentteamland/scoop-bucket
scoop install atl
```

### Windows (winget — after first Microsoft review completes)

```powershell
winget install agentteamland.atl
```

### One-liner fallback (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/agentteamland/cli/main/scripts/install.sh | sh
```

### Manual

Download the latest release for your platform from [releases](https://github.com/agentteamland/cli/releases), extract, and move the binary into your `$PATH`.

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
atl remove <team>                       # unlinks symlinks; cached repo stays
atl update [team]                       # pull updates; refresh all symlinks
atl update --silent-if-clean            # hook-friendly: no output if nothing changed
atl update --check-only                 # dry-run: what WOULD update
atl update --throttle=30m               # skip if last run <30m ago
atl search <keyword>                    # search the public registry
atl setup-hooks                         # install Claude Code hooks so update checks
                                        # run automatically on session start and every
                                        # prompt (30m throttle, idempotent)    (atl ≥ 0.1.5)
atl setup-hooks --remove                # uninstall the hooks
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

`atl install <name>` walks the inheritance chain (unlimited depth, cycle-detected), merges the effective agent/skill/rule set with *child overrides parent* semantics (and honoring `excludes`), then creates symlinks from the cached source repos into your project's `.claude/agents/`, `.claude/skills/`, `.claude/rules/`.

Cached source repos live in `~/.claude/repos/agentteamland/` and are shared across all projects on the machine. Only the project-level symlinks differ.

## Submit your team to the registry

Open a PR against [agentteamland/registry](https://github.com/agentteamland/registry). Full guide: [CONTRIBUTING.md](https://github.com/agentteamland/registry/blob/main/CONTRIBUTING.md).

## Status

**Current: v0.1.5** — everything in v0.1.4 plus hook-driven auto-update:
  - `atl setup-hooks` writes Claude Code SessionStart + UserPromptSubmit hooks
  - `atl update --silent-if-clean --throttle=<dur>` (hook-friendly; fast-path 1ms, slow-path every throttle window)
  - `atl update` now iterates every cached repo under `~/.claude/repos/agentteamland/` (teams AND globals: core / brainstorm / rule / team-manager), in parallel
  - atl binary self-check against GitHub Releases (throttled to 24h); prints `⬆ atl X → Y available — run: brew upgrade atl` when outdated
  - First-install opt-in prompt for the auto-update hooks

**v0.1.4** — local-filesystem install (`./path`, `/abs/path`, `~/path`, `file://...`).

**v0.1.x baseline** — install / list / remove / update / search; registry name-resolution; unlimited-depth `extends` with `excludes` + override + circular detection.

**Roadmap:**
- `atl doctor` (diagnostics)
- `atl team submit` (interactive registry PR)
- `atl new-project` (team-scoped scaffolder dispatch)
- Version-constraint enforcement (caret/tilde/exact)

## License

MIT.
