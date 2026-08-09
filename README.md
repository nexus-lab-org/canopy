# canopy

`canopy` manages a pool of git worktrees for agentic coding sessions —
`init` a pool in a repo, `claim`/`release` worktrees on behalf of agent
sessions (wired into Claude Code and Codex lifecycle hooks), and inspect
or clean up the pool with `status`, `prune`, and `destroy`. See
[`.context/spec-canopy.md`](.context/spec-canopy.md) for the full design.

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
