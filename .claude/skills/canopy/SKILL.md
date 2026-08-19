---
name: canopy
description: "Install, configure, and operate canopy — the git-worktree pool manager for agentic coding sessions. Use when the user wants to set up canopy in a repo, check/inspect worktree pool state (status/prune/destroy), or troubleshoot claim/release behavior. Also use if canopy commands are missing or failing and need installing/reinstalling. Tool-agnostic: every step is a plain shell command, so this skill works the same regardless of which coding agent is running it."
---

# canopy

`canopy` manages a pool of git worktrees for agentic coding sessions. It
lets multiple concurrent agent sessions each get their own worktree
without colliding, and automatically frees worktrees back to the pool
when a session ends. This skill covers installing the `canopy` binary
and driving its day-to-day commands.

## 1. Check what's already there

```sh
command -v canopy && canopy status --json
```

- `canopy` missing → go to **Install**.
- `canopy` present but `canopy status` errors with something like "no
  pool" or "not initialized" → the repo needs `canopy init` (see
  **Set up a repo**).
- Otherwise canopy is already installed and this repo already has a
  pool — skip to **Day-to-day commands**.

## 2. Install

Two supported paths — pick based on what's available. Prefer `go
install` if a Go toolchain is present (zero extra download, always
builds from source); otherwise use the curl script.

```sh
# Path A: Go toolchain available
go install github.com/nexus-lab-org/canopy/cmd/canopy@latest
# binary lands in $(go env GOPATH)/bin — confirm that's on PATH

# Path B: no Go toolchain
curl -fsSL https://raw.githubusercontent.com/nexus-lab-org/canopy/main/install.sh | sh
# installs to /usr/local/bin (if writable) or ~/.local/bin — the script
# prints an `export PATH=...` line if that dir isn't already on PATH
```

Before running the curl-to-shell form, show the user the command and
get explicit confirmation — don't silently pipe a remote script into
`sh`. `go install` is lower-risk (it builds from the module's published
source) but still confirm which install path you're taking.

Verify after installing:

```sh
canopy --help
```

## 3. Set up a repo

Run once per repo that should have a worktree pool:

```sh
canopy init
```

## 4. Day-to-day commands

| Task | Command |
|---|---|
| Claim a worktree | `canopy claim --holder <id>` |
| Release a worktree | `canopy release --holder <id>` |
| List pool state | `canopy status` (add `--json` for scripting) |
| Reclaim stale-but-clean worktrees into the pool | `canopy prune` |
| Permanently delete a worktree | `canopy destroy <path>` (`--include-dirty`, `--include-unlanded`, `--all-idle` as needed) |
| Open a shell in a worktree for manual inspection | `canopy enter <path>` |

Notes:

- `claim`/`release`/`destroy` take `--holder`; `claim` also accepts
  `--max` (pool auto-grow cap) and `--pid` (defaults to the parent
  process's PID).
- `destroy` refuses dirty or unlanded (unmerged) worktrees unless told
  otherwise via `--include-dirty` / `--include-unlanded` — treat those
  as safety rails, not obstacles to route around by default. Confirm
  with the user before overriding them, since it discards uncommitted
  or unmerged work.
- `release --force` overrides a live-PID check and is meant as a human
  override only. Don't reach for it reflexively if a normal `release`
  is refused; that usually means the session it belongs to is still
  alive.

## Troubleshooting

- **`canopy: command not found` after install.sh** — the install
  directory isn't on `PATH`; re-source the shell profile or export the
  path the script printed.
- **Claims never release / pool looks stuck** — run `canopy status` to
  see recorded PIDs and liveness, then `canopy prune` to reclaim stale
  clean worktrees, or `canopy release --holder <id> --force` as a last
  resort for a genuinely dead session.
