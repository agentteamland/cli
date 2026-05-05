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

`atl` is a single static Go binary (~7 MB, zero runtime dependencies). It installs agent teams from a registry into any project, keeps them current, and wires Claude Code hooks so updates + learning capture happen silently in the background.

This repo holds the binary's source. Distribution is automated via [agentteamland/homebrew-tap](https://github.com/agentteamland/homebrew-tap) and [agentteamland/scoop-bucket](https://github.com/agentteamland/scoop-bucket) — every git tag triggers goreleaser, which ships binaries to both channels alongside GitHub Releases ZIP archives.

## 📚 Documentation

Full docs live at **[agentteamland.github.io/docs](https://agentteamland.github.io/docs/)**.

Most relevant sections:

- [Install `atl`](https://agentteamland.github.io/docs/guide/install) — every supported channel (brew, scoop, PowerShell one-liner, manual ZIP)
- [Quickstart](https://agentteamland.github.io/docs/guide/quickstart) — first install, first team, first session
- [CLI overview](https://agentteamland.github.io/docs/cli/overview) — every command in detail (`install`, `list`, `remove`, `update`, `search`, `setup-hooks`, `session-start`, `learning-capture`)
- [`atl setup-hooks`](https://agentteamland.github.io/docs/cli/setup-hooks) — auto-update + learning-capture wiring (the recommended one-time setup)

## License

MIT.
