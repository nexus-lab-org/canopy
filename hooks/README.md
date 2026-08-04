# canopy agent hook wiring

Two scripts, [`claim.sh`](./claim.sh) and [`release.sh`](./release.sh),
wire canopy into Claude Code's and Codex's session/subagent lifecycle
hooks (per the decision in `wayfinder/tickets/002-session-in-use-marking-mechanism.md`
and `.context/spec-canopy.md`'s "Session/worktree marking mechanism").
Both scripts:

- read the hook's JSON payload from stdin,
- resolve the holder id as `agent_id` if present (a subagent's own id,
  distinct from its parent session), else `session_id` (a top-level
  session's id — identical to Claude Code's `CLAUDE_CODE_SESSION_ID` env
  var),
- `cd` into the payload's `cwd` so `canopy claim`/`canopy release`
  resolve the right repo regardless of what directory the hook process
  itself launches in,
- call `canopy claim --holder <id> --pid "$PPID"` or
  `canopy release --holder <id>`.

The explicit `--pid "$PPID"` on `claim.sh` matters: `canopy claim`
defaults its recorded PID to its own parent process, which without this
flag would be the hook script itself — and the hook script exits the
instant it finishes, so the claim would look stale (dead PID) within
moments, long before `Stop`/`SubagentStop` ever fires, and any later
`canopy release` call for that holder would be refused as stale without
`--force`. `$PPID` instead points at whatever process actually invoked
the hook script — Claude Code's or Codex's own long-running process —
which is what genuinely stays alive for the session/subagent's whole
lifetime.

Both require `jq` and `canopy` on `PATH`. Neither script fails the hook
on error (missing pool, exhausted pool, no matching claim, missing
dependency) — a warning goes to stderr instead, since a broken/missing
canopy setup should never block an agent session from starting or
finishing. See "Crash / never-configured fallback" below for how orphaned
claims are still caught.

## Claude Code

Confirmed against Claude Code's own hooks reference
(https://code.claude.com/docs/en/hooks,
https://code.claude.com/docs/en/sub-agents,
https://code.claude.com/docs/en/env-vars):

- `SessionStart` payload: `{"session_id", "transcript_path", "cwd", "hook_event_name", "source", "model"}`.
- `SubagentStart` payload: same common fields plus `"agent_id"` and `"agent_type"` — the subagent's own id, separate from `session_id`.
- `Stop` payload: `{"session_id", ..., "hook_event_name": "Stop", "stop_hook_active", "last_assistant_message", ...}` — no `agent_id`.
- `SubagentStop` payload: same as `Stop` plus `"agent_id"`, `"agent_type"`, `"agent_transcript_path"`.
- `CLAUDE_CODE_SESSION_ID` is set in every hook command's subprocess environment and is documented to match the payload's `session_id` field exactly — so using `session_id` from the JSON is equivalent to reading that env var, and additionally covers `agent_id` for subagents (which has no env var equivalent).

Add to **user-level** `~/.claude/settings.json` (not a project
`.claude/settings.json` — see "Why user-level only" below):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/claim.sh" }
        ]
      }
    ],
    "SubagentStart": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/claim.sh" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/release.sh" }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/release.sh" }
        ]
      }
    ]
  }
}
```

Replace `/absolute/path/to/canopy` with wherever you keep this repo (or a
copy of just the `hooks/` scripts) checked out. No `matcher` is needed —
each of these events fires once per relevant lifecycle transition.

**Status: verified.** All four field names above (`session_id`,
`agent_id`, `cwd`, `hook_event_name`, plus `CLAUDE_CODE_SESSION_ID`'s
documented equivalence to `session_id`) come directly from Claude Code's
published docs, not assumption.

## Codex

Confirmed by reading Codex's hooks implementation source
(`codex-rs/hooks/src/schema.rs`, `codex-rs/hooks/src/engine/discovery.rs`
in https://github.com/openai/codex) — Codex ships a hooks system whose
JSON payload shape for these four events is, field-for-field, the same
as Claude Code's:

- `SessionStart` input (`SessionStartCommandInput`): `session_id`, `turn_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `permission_mode`, `source`. No `agent_id`.
- `SubagentStart` input (`SubagentStartCommandInput`): same, plus required `agent_id` and `agent_type`.
- `Stop` input (`StopCommandInput`): `session_id`, `turn_id`, `cwd`, `hook_event_name`, `stop_hook_active`, `last_assistant_message`, etc. No `agent_id`.
- `SubagentStop` input (`SubagentStopCommandInput`): same, plus required `agent_id`, `agent_type`, `agent_transcript_path`.

So `claim.sh`/`release.sh`'s `agent_id // session_id` holder resolution
works unmodified for Codex too.

Add to **user-level** `~/.codex/hooks.json` (`$CODEX_HOME`, which
defaults to `~/.codex`; Codex also supports a project-level
`.codex/hooks.json`, but per the same reasoning as Claude Code below,
wire this at the user level, not the project level):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/claim.sh" }
        ]
      }
    ],
    "SubagentStart": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/claim.sh" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/release.sh" }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/canopy/hooks/release.sh" }
        ]
      }
    ]
  }
}
```

**Status: best-effort, not independently verified against a running
Codex install.** Unlike the Claude Code section above, this is read
directly from Codex's Rust source rather than a stable published hooks
reference doc, because Codex's lifecycle-hooks feature is newer and its
public docs don't yet enumerate the exact payload schema the way Claude
Code's do. The struct/field names quoted above are exact as of the
`openai/codex` source read during this ticket, but:

- confirm your installed Codex version actually supports `hooks.json`
  and these four event names before relying on this in production —
  run `codex --version` and check `~/.codex/hooks.json` is picked up
  (e.g. via whatever hook-listing/debug command your version ships, if
  any);
- unlike Claude Code, Codex does **not** inject a session-id environment
  variable into user-configured hook subprocesses (confirmed from
  `codex-rs/hooks/src/engine/discovery.rs`: the env map for user/project
  hooks is empty) — so there is no Codex analog to
  `CLAUDE_CODE_SESSION_ID`; the JSON payload's `session_id`/`agent_id`
  fields are the *only* way to get the holder id for Codex, which is
  exactly what these scripts already do.

If a given Codex build doesn't support one of these events (e.g. no
`SubagentStart`), that gap is honest: subagents launched under it will
have no independent claim of their own, and only the top-level session's
`SessionStart`/`Stop` wiring will apply.

## Why user-level only

Both snippets above go in a **user-level** config file
(`~/.claude/settings.json`, `~/.codex/hooks.json`), never a project-level
one. This mirrors canopy's own hook design exactly (see
`internal/config`'s package doc and the "Config file design" decision in
`.context/spec-canopy.md`): a repo-level hook config would let cloning
and opening an untrusted repo run arbitrary shell commands (this hook
script, or anything else) the repo's author chose, just by starting an
agent session in it. Wiring canopy's hooks at the user level means you're
opting a machine into this behavior once, for every repo you choose to
work in with canopy — not something a checked-out repo can impose on
you.

## Crash / never-configured fallback

If `Stop`/`SubagentStop` never fires — the session crashes, is
`kill -9`'d, or the hooks above were never wired up on a given
machine — its claim is never released and would otherwise leak the
worktree forever. canopy's crash fallback is PID liveness, not these
hooks: every claim records the PID of the process that made it (see
`internal/pool/pool.go`'s `Claim`/`internal/pool/liveness.go`), and:

- `canopy status` reports such a claim's `liveness` as `"stale"` once
  that PID is no longer running, instead of `"live"`;
- `canopy prune` automatically returns a stale-and-clean worktree to the
  pool for reuse (leaving stale-but-dirty ones alone and reported, so
  uncommitted crashed work is never silently discarded);
- a human can always force it with
  `canopy release --force --holder <id>` once they've confirmed the
  process is really gone.

So a missing or failed hook firing degrades to "worktree becomes
reclaimable once its process dies," not "worktree stuck forever" or
"worktree silently reassigned out from under a still-running session."
