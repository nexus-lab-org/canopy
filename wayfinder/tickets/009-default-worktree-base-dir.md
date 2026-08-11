---
id: 9
title: Default worktree base dir
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: [8]
---

## Question

[Config file design](008-config-file-design.md) fixed *that* a user-level
`worktree_base_dir` override exists, but the unconfigured default —
`<repo>-canopy-worktrees` as a sibling of the repo root (`internal/pool/pool.go`)
— has three problems: the doubled `canopy` in the name reads awkwardly,
sibling-of-repo-root placement clutters the parent directory, and
worktrees for every repo canopy manages end up scattered across the
filesystem instead of living under one common root. What should the
unconfigured default actually be?

## Resolution

**A single common root under `$XDG_DATA_HOME`, not a sibling of each repo
root:**

```
$XDG_DATA_HOME/canopy/worktrees/<repo-basename>-<hash>/<branch-subdir>
```

falling back to `~/.local/share/canopy/worktrees/...` when
`XDG_DATA_HOME` is unset. `<hash>` is a short deterministic hash of the
repo's absolute toplevel path; `<branch-subdir>` is the existing
per-worktree naming, untouched by this change.

- **Data dir, not cache dir.** Worktrees hold real uncommitted work — that's
  state, not disposable cache. `$XDG_CACHE_HOME` implies something a
  cache-cleaner cron can freely purge; only canopy's own `prune`/`destroy`
  should ever remove a worktree.
- **Hash-disambiguated per-repo subdirectory, not raw-path-encoding or
  remote-identity.** Two different repos can share a basename (two
  `canopy` clones under different orgs). A hash of the absolute toplevel
  path is always available (works for local-only repos with no remote)
  and short; encoding the raw path with slashes-as-dashes is readable but
  ambiguous when a path segment itself contains a dash, and remote-based
  `org/repo` identity fails for repos without a configured remote.
- **`worktree_base_dir` override still gets the `<repo-basename>-<hash>`
  subdirectory appended underneath it.** The setting is user-level —
  shared across every repo canopy manages on that machine, not
  repo-scoped — so if an override replaced the whole path instead of just
  relocating the root, one override value could only ever safely serve
  one repo before a second repo's worktrees collided inside it,
  reintroducing the same collision problem this design solves.
- **No runtime migration.** Existing installs with worktrees already
  living at the old sibling-dir default are left as-is; canopy does not
  probe for and special-case the old location. Called out in release
  notes instead. Old worktrees are cleaned up manually via `prune`/`destroy`
  if desired. Two live default-resolution code paths selected by
  filesystem probing would make `pool.Open` harder to reason about for a
  one-time transition on what's still an early-stage user base.
