---
id: 8
title: Config file design
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

Now that [CLI command surface design](005-cli-command-surface-design.md)
has fixed the commands (`init`, `claim`, `release`, `status`, `enter`,
`prune`, `destroy`) and [Multi-agent conflict policy](004-multi-agent-conflict-policy.md)
has fixed the pool-growth policy (auto-grow to a configurable max), what
does `canopy init`'s config file actually contain and where does it live?

Covers: repo-level config (checked in vs. gitignored) vs. user-level
config (`~/.config/canopy/`) vs. both with a precedence order; what's
configurable (`--max` pool size, worktree base directory, branch-naming
scheme for auto-created branches); and whether canopy needs
`post_create`/`pre_destroy`-style hooks like treehouse (and if so, whether
those are honored only at user-level for safety, matching treehouse's own
precedent).

## Resolution

**Two-tier config, repo-level taking precedence over user-level:**

- **Repo-level:** checked-in `canopy.toml` at the repo root (mirroring
  treehouse's own `treehouse.toml`). Holds project policy that should be
  consistent for every agent session against the repo — pool `max`,
  branch-naming scheme for auto-created branches.
- **User-level:** optional `~/.config/canopy/config.toml`. Holds
  machine-local defaults — worktree base directory (where checkouts land
  on *this* machine) — plus `post_create`/`pre_destroy`-style hooks.

Split rationale: pool size and naming conventions are project policy and
belong in version control so they're consistent across everyone/every
agent session on that repo; filesystem layout is inherently per-machine,
so forcing it into the repo file would be wrong. Mirrors the same
repo-vs-user split already made for state in
[State/lease data model](006-state-lease-data-model.md) (state is
`.git/`-local; config for shareable *policy* is repo-level, config for
*machine* specifics is user-level).

**Hooks are user-level only, never repo-level** — matching treehouse's own
precedent exactly, for the same reason: a repo-level hook would mean
cloning a repo and running `canopy claim`/`init` in it could execute
arbitrary shell commands the repo author chose, a real supply-chain risk.
Hooks are something you configure for repos *you* trust, never something a
repo can impose on you by being checked out.
