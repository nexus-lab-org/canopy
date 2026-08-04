#!/usr/bin/env bash
#
# canopy hook: Stop / SubagentStop
#
# Reads a Claude Code or Codex hook payload (JSON) from stdin and releases
# the canopy worktree claimed by the session or subagent that fired the
# hook, returning it to the pool. Wire this as the command for Stop and
# SubagentStop — see hooks/README.md for the exact settings.json /
# hooks.json snippets for each tool.
#
# Holder id resolution mirrors claim.sh exactly (prefer "agent_id", fall
# back to "session_id") so a Stop/SubagentStop firing always resolves to
# the same holder id its matching SessionStart/SubagentStart claimed
# under.
#
# Never invoked with --force: a normal Stop/SubagentStop firing is this
# session's own clean shutdown, so its recorded PID is still alive (it's
# the process running this hook, or a live parent of it) and a plain
# release succeeds. If release fails anyway (no matching claim, or the
# rare case where it's already stale), that's reported on stderr but
# never fails the hook — Stop/SubagentStop must not block a session from
# finishing. A claim that's genuinely orphaned this way (hook never fired
# at all, e.g. a crash) is caught later by PID-liveness in `canopy
# status`/`canopy prune`, not by this script.
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
    echo "canopy hook: hook payload had neither agent_id nor session_id; nothing to release" >&2
    exit 0
fi
if [ -n "$cwd" ]; then
    cd "$cwd"
fi

if ! canopy release --holder "$holder" >/dev/null; then
    echo "canopy hook: 'canopy release --holder $holder' failed in $(pwd) (no matching claim, or already stale); leaving it for 'canopy prune'/'canopy status' to surface" >&2
fi
