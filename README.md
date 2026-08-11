# canopy

`canopy` manages a pool of git worktrees for agentic coding sessions —
`init` a pool in a repo, `claim`/`release` worktrees on behalf of agent
sessions (wired into Claude Code and Codex lifecycle hooks), and inspect
or clean up the pool with `status`, `prune`, and `destroy`. See
[`.context/spec-canopy.md`](.context/spec-canopy.md) for the full design,
or the project site at
[nexus-lab-org.github.io/canopy](https://nexus-lab-org.github.io/canopy).

## Installation

Two supported install paths (see
[`wayfinder/tickets/007-distribution-install-story.md`](wayfinder/tickets/007-distribution-install-story.md)
for the reasoning): `go install` for anyone with a Go toolchain, and a
curl-to-install script for everyone else. No Homebrew or Nix packaging
for now.

### Option 1: `go install` (requires a Go toolchain)

```sh
go install github.com/nexus-lab-org/canopy/cmd/canopy@latest
```

This installs a `canopy` binary to `$(go env GOPATH)/bin` (typically
`~/go/bin`) — make sure that directory is on your `PATH`. Note the
`cmd/canopy` subpath: `main.go` lives at `cmd/canopy/main.go`, not the
module root.

### Option 2: curl-to-install script (no Go toolchain required)

```sh
curl -fsSL https://raw.githubusercontent.com/nexus-lab-org/canopy/main/install.sh | sh
```

This downloads the prebuilt `canopy` binary matching your OS/architecture
from the latest [GitHub Release](https://github.com/nexus-lab-org/canopy/releases)
and installs it to `/usr/local/bin` (if writable) or `~/.local/bin`
otherwise. Supported platforms: Linux and macOS, amd64 and arm64 (no
Windows). If the install directory isn't already on your `PATH`, the
script prints the `export PATH=...` line to add.

Prebuilt binaries are built via the [`goreleaser`](https://goreleaser.com)
pipeline configured in [`.goreleaser.yaml`](.goreleaser.yaml).

## Agent hooks setup

Claim and release can be wired into Claude Code's and Codex's session
lifecycle hooks, so a worktree is claimed when an agent session starts
and released when it stops — no manual `claim`/`release` calls. Requires
[`jq`](https://jqlang.org/download/) on `PATH`. One command wires both:

```sh
git clone https://github.com/nexus-lab-org/canopy.git
cd canopy
./hooks/install.sh              # wires both Claude Code and Codex
./hooks/install.sh --claude     # Claude Code only
./hooks/install.sh --codex      # Codex only
./hooks/install.sh --uninstall  # remove canopy's entries again
```

This edits your **user-level** hook config (`~/.claude/settings.json`,
`~/.codex/hooks.json`) — never a project-level one, so opening an
untrusted repo can never smuggle in its own hook commands. The installer
is idempotent (safe to re-run) and leaves any unrelated hooks in those
files untouched. See [`hooks/README.md`](hooks/README.md) for the full
payload-field reference, the crash/never-configured fallback, and why
user-level wiring matters.

## Agent skill

This repo ships a [`canopy` skill](.claude/skills/canopy/SKILL.md) that
teaches a coding agent how to install canopy, wire its hooks, and drive
`init`/`status`/`prune`/`destroy` on its own — hand an agent the repo
and it can set canopy up for you instead of you running the commands
above by hand. It's a standard [Agent Skill](https://skills.sh) (a
`SKILL.md` under `.claude/skills/`), so it installs with the
[`skills` CLI](https://github.com/vercel-labs/skills) like any other:

```sh
npx skills add nexus-lab-org/canopy --skill canopy
```

This copies the skill into whichever coding agents you have installed
(Claude Code, Codex, Cursor, and 70+ others — see `npx skills add
--help` for the full list), after which asking your agent to "set up
canopy in this repo" or "install canopy" is enough for it to run the
skill. See [the blog post](docs/blog/agent-skill.md) for a ready-to-paste
prompt if you'd rather skip the CLI and just point an agent at this repo
directly.

## Verifying an install

```sh
canopy init                       # inside a git repo
canopy claim --holder my-session  # prints a worktree path
canopy status                     # lists pool state
```

## Development

```sh
go build ./...
go vet ./...
gofmt -l .
go test ./... -race -count=1
```

Distribution-specific tests live in `test/install_test.go` (the
`go build`/`go install` paths, run through an init/claim/status smoke
test) and `test/install_script_test.sh` (unit tests for `install.sh`'s
OS/arch detection and download-URL construction, with `uname` stubbed).

## License

[GPL-3.0](LICENSE).
