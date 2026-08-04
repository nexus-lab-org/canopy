---
id: 6
title: State/lease data model
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

Now that [Language / runtime choice](003-language-runtime-choice.md) has
landed on Go + `gofrs/flock`, and [Session in-use marking mechanism](002-session-in-use-marking-mechanism.md)
has fixed the claim record shape (`{holder, pid, claimed_at}`, one per
session/subagent), what's the concrete on-disk format and locking strategy
for the pool state file?

Covers: single JSON file vs. one file per worktree/claim, where it lives
(repo-local `.canopy/` vs. `~/.config/canopy/` vs. XDG state dir per-repo
keyed by repo path), what `flock` covers (whole-file lock around
read-modify-write vs. finer-grained), and how the pool's own catalog of
managed worktrees (paths, branch, created_at) sits alongside the claim
records — same file or separate.

## Resolution

**`.git/canopy/state.json`** — a single JSON file, colocated with git's own
`.git/worktrees/` bookkeeping in the primary repository's `.git/`
directory, protected by one whole-file `flock` (`gofrs/flock`) around
every read-modify-write.

- **Location:** inside the primary repo's `.git/canopy/`, not a
  user-level XDG state dir and not repo-root. This colocates canopy's
  pool state with the exact thing it manages (mirroring where git
  already keeps its own worktree metadata), avoids any need for a
  repo-path hashing/keying scheme to disambiguate multiple repos, and
  `.git/` is never committed or shown by `git status`, so there's no risk
  of it leaking into the tracked repo the way a repo-root file would.
- **Format/granularity:** one JSON file holding both the worktree catalog
  (paths, branch, `created_at`) and the claim records (`{holder, pid,
  claimed_at}` per [Session in-use marking mechanism](002-session-in-use-marking-mechanism.md))
  together — not split into per-worktree files. The pool is small
  (bounded by the `--max` from [Multi-agent conflict policy](004-multi-agent-conflict-policy.md),
  realistically dozens of entries per repo at most), and claim/release
  operations are infrequent (session start/stop, not a hot path), so
  there's no real contention or scale pressure to justify finer-grained
  files — doing so would only add risk of the catalog and claim state
  drifting out of sync across separate lock cycles.
- **Locking:** one whole-file lock wraps every read-modify-write cycle
  (`claim`, `release`, `prune`, `destroy`'s bookkeeping update). No
  finer-grained per-worktree locking.
