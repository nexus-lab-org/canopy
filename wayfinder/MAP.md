---
label: wayfinder:map
status: open
tracker: local-markdown
---

# Map: canopy (Agentic Worktree Wrapper Spec)

## Destination

A build-ready spec for a new, from-scratch CLI tool that manages a pool of
git worktrees for agentic coding sessions (Claude Code, Codex, and similar),
including a mechanism to mark a worktree as in-use for the duration of an
agent session. The map is done when name, stack, session-marking mechanism,
conflict policy, and command surface are all decided — nothing left for an
implementer to guess.

## Notes

- Domain: local dev tooling for agentic coding workflows (worktree pooling,
  session lifecycle, filesystem isolation).
- Prior art: [treehouse](https://github.com/kunchenguid/treehouse) (Go CLI,
  MIT-style pool-of-worktrees manager with process-based in-use detection,
  durable leases, prune/destroy safety, hooks). We are building independently
  from scratch, not forking or wrapping it — but its command surface
  (`get`/`return`/`status`/`prune`/`destroy`, `--lease`/`--lease-holder`,
  dirty/unmerged/in-use safety checks) is a strong reference point and
  tickets should explicitly compare against it rather than re-deriving from
  zero.
- Use `/grilling` and `/domain-modeling` for tickets marked `grilling`.
- Tracker: no GitHub remote exists yet for this project, so this map uses
  the local-markdown fallback under `wayfinder/`. Tickets live in
  `wayfinder/tickets/`; blocking is recorded as a `blocked_by:` list in each
  ticket's frontmatter (ids). A ticket is unblocked when every id in its
  `blocked_by` list refers to a closed ticket.

## Decisions so far

- [Session in-use marking mechanism](tickets/002-session-in-use-marking-mechanism.md) — explicit claim/release via `SessionStart`/`Stop` and `SubagentStart`/`SubagentStop` hooks (Claude Code + Codex), one independent holder per hook firing (subagents can claim their own worktree, not just ride the parent's), PID-liveness as the only fallback for missed release hooks.
- [Name the tool](tickets/001-name-the-tool.md) — **canopy**. Tree/structure-metaphor family distinct from `treehouse`; collision check on npm/crates.io/Homebrew found no popular conflicting package.
- [Language / runtime choice](tickets/003-language-runtime-choice.md) — **Go**, targeting **macOS + Linux** only. Chosen for library maturity (flock, PID-signal liveness, cobra) and trivial cross-compilation, not just the treehouse precedent.
- [Multi-agent conflict policy](tickets/004-multi-agent-conflict-policy.md) — auto-grow pool up to a configurable max then block; live claims are never force-reassigned (manual `--force` override only); safe-to-reclaim means clean (no uncommitted changes), merged not required.
- [CLI command surface design](tickets/005-cli-command-surface-design.md) — `init`/`claim`/`release`/`status`/`enter`/`prune`/`destroy`; no `--lease` flag (claims are always holder-scoped); `prune` never destroys; treehouse's `--force` split into `--include-unlanded`/`--include-dirty`. Full reference: [assets/005-cli-reference.md](assets/005-cli-reference.md).
- [State/lease data model](tickets/006-state-lease-data-model.md) — single `.git/canopy/state.json` (catalog + claims together), colocated with git's own `.git/worktrees/`, protected by one whole-file `flock`.
- [Distribution/install story](tickets/007-distribution-install-story.md) — `go install` as primary, curl script + GitHub Releases binaries for non-Go users. Homebrew/Nix skipped for now.
- [Config file design](tickets/008-config-file-design.md) — repo-level checked-in `canopy.toml` for shared policy (pool max, branch naming), user-level `~/.config/canopy/config.toml` for machine-local defaults; hooks are user-level only, matching treehouse's safety precedent.

## Not yet specified

(none — remaining open questions are all tickets now)

## Out of scope

(none yet)
