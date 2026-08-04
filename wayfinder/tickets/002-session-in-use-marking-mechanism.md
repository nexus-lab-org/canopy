---
id: 2
title: Session in-use marking mechanism
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

How does the tool know a Claude Code or Codex session is running in a
worktree and mark it in-use for that duration? treehouse offers two
existing models worth grilling against:

- **Implicit process-scan** — treehouse detects in-use by scanning for
  running processes inside the worktree (works automatically, but only
  catches sessions launched interactively inside a subshell it controls).
- **Explicit durable lease** — treehouse's `get --lease --lease-holder
  <label>` marks a worktree in-use in persistent state regardless of
  whether any process is running, released via `return`.

Given Claude Code / Codex sessions may be launched headlessly, backgrounded,
or outside a `get`-spawned subshell, which model (or hybrid) fits, and what
hooks it into the session lifecycle: a Claude Code / Codex lifecycle hook
that calls the CLI on session start/stop, a wrapper that spawns the agent
itself, or something else? Resolve to a concrete mechanism, not just a
preference.

## Resolution

Explicit hook-triggered claiming, not process-scanning:

- **Trigger:** `SessionStart` and `SubagentStart` hooks (both Claude Code
  and Codex support these — confirmed via each tool's hooks docs) call
  `<tool> claim <path-or-pool> --holder <id>`; `Stop`/`SubagentStop` call
  `<tool> release --holder <id>`.
- **Granularity:** every hook firing — top-level session or subagent — is
  an independent holder. A subagent is not assumed to share its parent's
  worktree; it can claim its own from the pool when it needs isolated
  filesystem work. (This is what pushed the model past a simple
  session-level lease: subagents dispatched in parallel need their own
  isolated worktrees too.)
- **Holder identity:** derived from the session/subagent id the hook
  environment exposes. Claude Code confirmed: `CLAUDE_CODE_SESSION_ID` env
  var, present on hook invocation. Codex: has an analogous session/thread
  id in its hook payload; exact field name to confirm against Codex's hook
  JSON schema during implementation — not a blocker for this decision.
- **Crash fallback:** a claim records `{holder, pid, claimed_at}`. If
  `Stop`/`SubagentStop` never fires (crash, `kill -9`, hooks not
  configured), the claim is provably stale once its recorded PID is no
  longer alive. `status`, `prune`, and a `claim` call against an exhausted
  pool all check PID liveness and drop dead claims rather than trusting
  them indefinitely. This is the tool's only fallback for missed release
  hooks — no TTL/heartbeat model.

Confirmed with the user across three rounds: hook-triggered explicit
marking over process-scanning; PID-liveness over TTL/heartbeat for the
crash fallback; per-subagent claiming (not parent-scoped) for the
granularity.
