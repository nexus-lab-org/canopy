# canopy — rough CLI reference (prototype)

Reference point: treehouse's surface is `get`/`get --lease`/`enter`/`status`/`return`/`prune`/`destroy`/`init`.
canopy diverges where the claim/release marking mechanism and the
auto-grow/clean-reclaim policy demand a different shape.

## Command table

| Command | Purpose | Key flags |
|---|---|---|
| `canopy init` | Set up `.canopy/` config in a repo (pool size, max, hook notes). | `--max <n>` |
| `canopy claim` | Get a worktree for the current holder. Pulls an available one, or auto-grows the pool (up to `--max`) if none is free. Called by `SessionStart`/`SubagentStart` hooks. | `--holder <id>` (required), `--branch <name>` (default: auto-generated), `--json` |
| `canopy release` | Release a claim, returning the worktree to the pool if clean. Called by `Stop`/`SubagentStop` hooks. | `--holder <id>` (required), `--force` (override a live claim — manual only, never called by hooks) |
| `canopy status` | List pool state: each worktree's path, branch, claim (holder/pid/live-or-stale), clean/dirty. | `--json` |
| `canopy enter <path>` | Drop a human into a worktree's shell (subshell with `cd`'d dir), for manual inspection — doesn't claim/release on its own. | — |
| `canopy prune` | Reclaim worktrees whose claims are stale (dead PID) and clean back into the available pool. Dirty ones are left alone and reported. | `--include-unlanded` (also prune stale claims on unmerged-but-clean branches — default already does this since merged isn't required; flag reserved for future stricter default), `--json` |
| `canopy destroy <path\|--all-idle>` | Actually remove a worktree from disk + git, freeing pool capacity permanently (not returning to pool). | `--include-unlanded`, `--include-dirty` (both required together to destroy a dirty/unmerged tree — no single `--force`) |

## Design choices vs. treehouse

- **`get` → `claim` / `return` → `release`.** treehouse's `get` optionally
  takes `--lease --lease-holder`; canopy's model is *always* an explicit
  holder-scoped claim (that's the whole marking mechanism), so there's no
  bare unmarked `get` — `--holder` is required on `claim`, not an opt-in
  lease mode.
- **No `--lease` flag.** Every claim already behaves like treehouse's
  durable lease (independent of any spawned subshell), so the flag would
  be redundant — it's the only mode.
- **`destroy` requires two explicit flags to override safety**, not one
  `--force`. treehouse removed a blanket `--force` for the same reason;
  canopy splits it further into `--include-unlanded` and `--include-dirty`
  so an operator has to consciously accept losing *uncommitted* work
  separately from accepting an *unmerged* branch — conflating the two was
  flagged as risky in [Multi-agent conflict policy](004-multi-agent-conflict-policy.md).
- **`prune` never destroys.** It only frees stale, clean claims back into
  the pool. Destroying worktrees (removing them from disk) is exclusively
  `destroy`'s job — keeps "give this worktree back for reuse" and
  "permanently delete this worktree" from being one blurry command.
- **`enter` is kept** as a human-only convenience (treehouse has it too)
  but explicitly does *not* claim on the caller's behalf — claiming is a
  hook-driven, holder-scoped act, not something a human dropping in for a
  look should trigger.

## Example transcripts

```
$ canopy claim --holder claude-session-9f2a
claimed /Users/asif/.canopy/worktrees/canopy-9f2a on branch agent/9f2a-untitled

$ canopy status
HOLDER                PATH                                      BRANCH              CLAIM     CLEAN
claude-session-9f2a   ~/.canopy/worktrees/canopy-9f2a            agent/9f2a-untitled live      no
codex-session-2b71    ~/.canopy/worktrees/canopy-2b71            agent/2b71-fix-lint stale     yes

$ canopy status --json
{"worktrees":[{"path":"...","branch":"...","holder":"claude-session-9f2a","pid":48213,"claim_state":"live","clean":false}, ...]}

$ canopy prune
reclaimed 1 worktree (codex-session-2b71: stale claim, clean) → returned to pool
skipped 1 worktree (claude-session-9f2a: live claim)

$ canopy release --holder claude-session-9f2a
released /Users/asif/.canopy/worktrees/canopy-9f2a (clean) → returned to pool

$ canopy destroy ~/.canopy/worktrees/canopy-2b71 --include-unlanded
error: worktree has uncommitted changes — pass --include-dirty to destroy anyway
```
