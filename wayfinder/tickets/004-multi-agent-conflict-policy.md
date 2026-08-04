---
id: 4
title: Multi-agent conflict policy
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

When multiple Claude/Codex sessions run concurrently against the same repo,
what's the policy for handing out and reclaiming worktrees so agents never
step on each other? Covers: what happens when the pool is exhausted (block,
error, auto-grow up to a max, evict oldest idle), whether a worktree can
ever be force-reassigned while in-use, and what "safe to reclaim" means
(clean + merged, like treehouse's prune/destroy checks, or something
looser/stricter for agent-generated branches that may never get merged
upstream).

Now informed by [Session in-use marking mechanism](002-session-in-use-marking-mechanism.md):
claims are per session/subagent (`{holder, pid, claimed_at}`), reclaimed
only once the recorded PID is dead — so "pool exhausted" and "safe to
reclaim" should be answered in terms of live vs. stale claims, not raw
process-scan state, and subagents claiming their own worktrees means pool
pressure can spike from a single top-level session fanning out.

## Resolution

- **Pool exhaustion:** auto-grow the pool up to a configurable max, then
  block/error once that max is hit. Chosen over a hard cap because agent
  fan-out (a session dispatching several subagents) is exactly the case
  that spikes demand unpredictably, and a hard cap would silently stall an
  agent mid-task. Chosen over oldest-idle eviction because idle time isn't
  a reliable "safe to take" signal for agent sessions — a session can be
  paused mid-task, not abandoned.
- **Force-reassignment:** a live claim (PID still alive) is absolute —
  never auto-force-reassigned during normal `claim` traffic. This follows
  directly from the marking-mechanism decision: PID-liveness is the only
  signal of "in use," so overriding it means guessing a running agent is
  safe to interrupt, risking corruption of an in-progress session's
  working tree. The only override is an explicit manual action —
  `canopy release --force --holder <id>` — operated by a human, not
  something the pool decides on its own.
- **Safe to reclaim:** clean (no uncommitted changes) is required; merged
  is not. A dirty worktree is never auto-reclaimed — it's surfaced in
  `status` for a human to look at, since uncommitted changes on a dead
  claim are exactly the "did it finish or crash mid-edit" signal that
  shouldn't be silently discarded. A clean-but-unmerged worktree is safe
  to reclaim into the pool: the branch ref and its commits survive, only
  the working-tree checkout is freed. Requiring merged-before-reclaim was
  rejected because most agent branches are reviewed/merged out-of-band,
  often long after the session that created them — gating reclaim on
  "merged" would permanently leak worktrees for every unmerged branch.
  `--include-unlanded` (borrowing treehouse's flag) opts into
  reclaiming/destroying even unmerged branches explicitly.
