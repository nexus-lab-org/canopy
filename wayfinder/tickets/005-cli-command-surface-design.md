---
id: 5
title: CLI command surface design
type: wayfinder:prototype
status: closed
assignee: asif (claude session)
blocked_by: [2, 3]
---

## Question

What are the actual commands, flags, and output shapes (human + `--json`)
for this tool? Should read as a deliberate response to treehouse's surface
(`get`/`get --lease`/`enter`/`status`/`return`/`prune`/`destroy`/`init`),
not a blind copy — reuse what's genuinely good (dry-run-by-default destroy,
`--json` machine output, stderr-for-banners/stdout-for-payload discipline)
and diverge where the marking mechanism from
[[002-session-in-use-marking-mechanism]] demands a different shape (e.g. a
`claim`/`release` pair instead of `--lease`, if sessions are marked
explicitly by a hook rather than via a subshell-scoped `get`). Produce a
rough CLI reference (command table + example transcripts) as the resolving
artifact.

## Resolution

CLI reference: [wayfinder/assets/005-cli-reference.md](../assets/005-cli-reference.md).

Command surface: `init`, `claim --holder <id>`, `release --holder <id>
[--force]`, `status [--json]`, `enter <path>`, `prune`, `destroy <path>
[--include-unlanded] [--include-dirty]`.

Key departures from treehouse, confirmed with the user:
- `get`/`return` renamed to `claim`/`release`; no `--lease` flag since
  every claim is already holder-scoped and durable (that's the whole
  marking mechanism from [Session in-use marking mechanism](002-session-in-use-marking-mechanism.md)).
- `prune` only reclaims stale+clean claims back into the pool — it never
  destroys. Destroying disk state is exclusively `destroy`'s job.
- treehouse's removed `--force` becomes two explicit flags on `destroy`:
  `--include-unlanded` (unmerged branch) and `--include-dirty`
  (uncommitted changes), so an operator can't accidentally lose
  uncommitted work while only meaning to accept an unmerged branch — per
  [Multi-agent conflict policy](004-multi-agent-conflict-policy.md).
- `enter` is kept as a human-only convenience and does not claim on the
  caller's behalf.
