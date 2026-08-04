---
id: 1
title: Name the tool
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

What is this tool called? Needs a name that's distinct from `treehouse`
(the prior art it's inspired by but not built on), reads well as a CLI verb
(`<name> get`, `<name> status`, ...), and isn't already taken by an
unrelated popular package on the likely install paths (npm/crates.io/Go
module proxy/Homebrew), so a quick collision check is part of resolving
this ticket.

## Resolution

**canopy.**

Collision check:
- **npm** — `canopy` is taken by a small PEG parser compiler for JavaScript.
  Unrelated domain, low collision risk.
- **crates.io** — `canopy` is taken by a Rust TUI framework ("framework for
  capable terminal applications"), ~1,714 all-time downloads. Thematically
  adjacent (both terminal tooling) but a different purpose (TUI framework,
  not a worktree pool manager) and far too small to cause real confusion.
- **Homebrew** — unclaimed.
- **Go module proxy** — not informative as a namespace check; Go module
  names are just repo paths, not a global registry the way npm/crates.io
  are.

Ruled out along the way:
- **burrow** — initial recommendation; user wanted a name in the tree
  family instead of a burrow/den metaphor.
- **sapling** — dropped outright: it's the name of a real, well-known
  source-control tool (Meta's Sapling SCM). Too close to this tool's own
  domain to risk, regardless of what the npm listing under that name
  actually is.
- **grove** — cleanest collision profile (npm/crates.io listings under
  that name are empty placeholder stubs) and a strong pool-of-worktrees
  metaphor, but not the one chosen.
- **arbor**, **outpost**, **claimyard** — considered, not chosen.

CLI verbs read as `canopy claim`, `canopy status`, `canopy release`,
`canopy get`, etc. — distinct from `treehouse` while staying in the same
tree/structure metaphor family.
