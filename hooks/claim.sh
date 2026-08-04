#!/usr/bin/env bash
#
# canopy hook: SessionStart / SubagentStart
#
# Reads a Claude Code or Codex hook payload (JSON) from stdin and claims a
# canopy worktree on behalf of the session or subagent that fired the
# hook. Wire this as the command for SessionStart and SubagentStart — see
# hooks/README.md for the exact settings.json / hooks.json snippets for
# each tool.
#
# Holder id: prefers the payload's "agent_id" field, present only on a
# SubagentStart firing, where it identifies that specific subagent,
# distinct from its parent session's id. Falls back to "session_id"
# (present on every hook event) for a top-level SessionStart. This is
# what gives every subagent its own independent claim instead of sharing
# its parent's: see the "Claim granularity" decision in
# .context/spec-canopy.md. Claude Code's session_id field is confirmed
# identical to its CLAUDE_CODE_SESSION_ID env var (per
# https://code.claude.com/docs/en/env-vars); we read it from the JSON
# payload rather than the env var so the same code path also handles
# agent_id, which has no env var equivalent.
#
# A missing canopy pool (canopy init was never run against this repo), an
# exhausted pool, or a missing dependency is reported on stderr but never
# fails the hook — SessionStart/SubagentStart must not block an agent
# session from starting just because worktree pooling isn't set up, is
# full, or canopy/jq aren't on PATH.
#
# --pid: canopy claim defaults its recorded PID to its own parent
# process, which by default would be *this script* — but this script
# exits the moment it finishes, so that default would make the claim
# look dead (liveness-stale) within moments of being made, long before
# Stop/SubagentStop ever fires. We instead pass $PPID explicitly: the
# process that invoked this hook script in the first place (Claude
# Code's or Codex's own long-running process), which is what actually
# stays alive for the duration of the session/subagent turn this claim
# belongs to.
set -euo pipefail

for bin in jq canopy; do
    if ! command -v "$bin" >/dev/null 2>&1; then
        echo "canopy hook: required command '$bin' not found in PATH" >&2
        exit 0
    fi
done

payload="$(cat)"
holder="$(jq -r '.agent_id // .session_id // empty' <<<"$payload")"
cwd="$(jq -r '.cwd // empty' <<<"$payload")"

if [ -z "$holder" ]; then
    echo "canopy hook: hook payload had neither agent_id nor session_id; nothing to claim" >&2
    exit 0
fi
if [ -n "$cwd" ]; then
    cd "$cwd"
fi

if ! canopy claim --holder "$holder" --pid "$PPID" >/dev/null; then
    echo "canopy hook: 'canopy claim --holder $holder' failed in $(pwd) (pool not initialized, or exhausted); continuing without an isolated worktree" >&2
fi
