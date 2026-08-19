---
name: canopy
description: "Install, configure, and operate canopy — the git-worktree pool manager for agentic coding sessions. Use when the user wants to set up canopy in a repo, wire its session-lifecycle hooks (Claude Code and/or Codex, the only two canopy currently supports), check/inspect worktree pool state (status/prune/destroy), or troubleshoot claim/release/hook behavior. Also use if canopy commands are missing or failing and need installing/reinstalling. Tool-agnostic: every step is a plain shell command, so this skill works the same regardless of which coding agent is running it."
---

# canopy

`canopy` manages a pool of git worktrees for agentic coding sessions. It
lets multiple concurrent agent sessions each get their own worktree
without colliding, and automatically frees worktrees back to the pool
when a session ends. This skill covers installing the `canopy` binary,
wiring it into hooks, and driving its day-to-day commands.

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

Also check whether hooks are wired (only relevant if the user wants
automatic claim/release, not just manual pool management):

```sh
grep -o 'canopy/hooks/[a-z]*\.sh' ~/.claude/settings.json 2>/dev/null
grep -o 'canopy/hooks/[a-z]*\.sh' ~/.codex/hooks.json 2>/dev/null
```

No output from either means hooks aren't wired yet.

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

## 3. Wire agent hooks (optional, only if the user wants auto claim/release)

This makes Claude Code (and/or Codex) automatically `canopy claim` a
worktree when a session/subagent starts and `canopy release` it when
that session/subagent stops — no manual claim/release calls needed.
Requires `jq` on `PATH`.

```sh
git clone https://github.com/nexus-lab-org/canopy.git   # if not already checked out
cd canopy
./hooks/install.sh              # wires both Claude Code and Codex, if present
./hooks/install.sh --claude     # Claude Code only
./hooks/install.sh --codex      # Codex only
./hooks/install.sh --uninstall  # remove canopy's entries again
```

This edits the **user-level** hook config (`~/.claude/settings.json`,
`~/.codex/hooks.json`), never a project-level one — that's deliberate,
so opening an untrusted repo can never smuggle in its own hook
commands. The installer is idempotent and leaves unrelated hooks in
those files untouched. Confirm with the user before running it, since
it edits their global config. Full mechanics: `hooks/README.md` in this
repo (payload fields, the `--pid "$PPID"` reasoning, crash/never-
configured fallback).

## 4. Set up a repo

Run once per repo that should have a worktree pool:

```sh
canopy init
```

## 5. Day-to-day commands

| Task | Command |
|---|---|
| Claim a worktree (manual, hooks normally do this) | `canopy claim --holder <id>` |
| Release a worktree (manual, hooks normally do this) | `canopy release --holder <id>` |
| List pool state | `canopy status` (add `--json` for scripting) |
| Reclaim stale-but-clean worktrees into the pool | `canopy prune` |
| Permanently delete a worktree | `canopy destroy <path>` (`--include-dirty`, `--include-unlanded`, `--all-idle` as needed) |
| Open a shell in a worktree for manual inspection | `canopy enter <path>` |

Notes:

- `claim`/`release`/`destroy` take `--holder`; `claim` also accepts
  `--max` (pool auto-grow cap) and `--pid` (defaults to the parent
  process's PID — hooks pass `$PPID` explicitly so the claim tracks the
  long-lived Claude Code/Codex process rather than the short-lived hook
  script).
- `destroy` refuses dirty or unlanded (unmerged) worktrees unless told
  otherwise via `--include-dirty` / `--include-unlanded` — treat those
  as safety rails, not obstacles to route around by default. Confirm
  with the user before overriding them, since it discards uncommitted
  or unmerged work.
- `release --force` overrides a live-PID check and is meant as a human
  override only — hooks never pass it. Don't reach for it reflexively
  if a normal `release` is refused; that usually means the session it
  belongs to is still alive.

## Troubleshooting

- **`canopy: command not found` after install.sh** — the install
  directory isn't on `PATH`; re-source the shell profile or export the
  path the script printed.
- **Hook install fails with a `jq` error** — install `jq` first
  (https://jqlang.org/download/), then re-run `./hooks/install.sh`.
- **Claims never release / pool looks stuck** — run `canopy status` to
  see recorded PIDs and liveness, then `canopy prune` to reclaim stale
  clean worktrees, or `canopy release --holder <id> --force` as a last
  resort for a genuinely dead session.
