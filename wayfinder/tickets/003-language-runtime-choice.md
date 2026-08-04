---
id: 3
title: Language / runtime choice
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

What language/runtime is this CLI built in? treehouse is a single static Go
binary distributed via curl script / Nix / `go install`. Options to weigh:
Go (matches prior art, easy cross-compilation, single binary), Rust (single
binary, no runtime, steeper build), TypeScript/Bun or Node (fastest to
prototype, matches the user's other repos in this workspace, but adds a
runtime dependency for end users unless bundled/compiled). Decide the
language and the target platforms (macOS/Linux/Windows, matching
treehouse's scope, or narrower).

## Resolution

**Go**, targeting **macOS + Linux** (no Windows).

Language comparison, focused on the libraries this tool actually needs
(git worktree ops, locked/atomic state-file writes, PID liveness checks,
CLI framework, cross-compilation):

| Need | Go | Rust | TypeScript/Bun |
|---|---|---|---|
| Git worktree ops | shell out or `go-git` (pure-Go, mature) | shell out or `git2-rs` (libgit2 bindings) | shell out via `simple-git`/`execa` |
| State-file locking | `github.com/gofrs/flock` | `fs4`/`fs2` crate | `proper-lockfile` npm, less battle-tested |
| PID liveness check | `os.FindProcess` + signal 0 — standard daemon pattern | `sysinfo`/`nix` crate | `process.kill(pid, 0)`, a JS idiom rather than a hardened primitive |
| CLI framework | `spf13/cobra` (same one `kubectl`/`gh`/`hugo` use) | `clap` (nicest ergonomics of the three) | `commander`/`oclif` |
| Cross-compilation | trivial, built into toolchain (`GOOS`/`GOARCH`) | possible, more manual setup | `bun build --compile` is newer, less proven |

Go wins on library maturity specifically for locking + PID checks +
cross-compilation, not just the treehouse precedent (treehouse itself is
Go, but that alone wasn't the deciding factor).

Platforms: macOS + Linux only, not Windows. Matches where Claude
Code/Codex sessions actually run (dev machines, CI); Windows would add
real complexity since both PID-signal liveness and file-locking need
Windows-specific code paths, and there's no concrete demand for it yet.
