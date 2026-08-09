#!/usr/bin/env sh
# install.sh — wires claim.sh/release.sh into Claude Code's and/or Codex's
# user-level lifecycle hooks (~/.claude/settings.json, ~/.codex/hooks.json).
#
#   ./hooks/install.sh              # wire both Claude Code and Codex, if present
#   ./hooks/install.sh --claude     # wire Claude Code only
#   ./hooks/install.sh --codex      # wire Codex only
#   ./hooks/install.sh --uninstall  # remove canopy's entries again
#
# Requires jq. Idempotent: re-running (or running after --uninstall) never
# duplicates or corrupts entries — every hooks.json/settings.json edit is a
# full read-modify-write of the JSON, keyed off the absolute paths to this
# repo's claim.sh/release.sh, and any existing unrelated hooks in the file
# are left untouched.
#
# See hooks/README.md for what these hooks do and why they're wired at the
# user level rather than a project level.

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
claim_path="${script_dir}/claim.sh"
release_path="${script_dir}/release.sh"

WIRE_CLAUDE=0
WIRE_CODEX=0
UNINSTALL=0

if [ "$#" -eq 0 ]; then
	WIRE_CLAUDE=1
	WIRE_CODEX=1
else
	for arg in "$@"; do
		case "$arg" in
		--claude) WIRE_CLAUDE=1 ;;
		--codex) WIRE_CODEX=1 ;;
		--uninstall) UNINSTALL=1 ;;
		*)
			echo "canopy: unknown argument '$arg' (expected --claude, --codex, --uninstall)" >&2
			exit 1
			;;
		esac
	done
	if [ "$WIRE_CLAUDE" -eq 0 ] && [ "$WIRE_CODEX" -eq 0 ]; then
		WIRE_CLAUDE=1
		WIRE_CODEX=1
	fi
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "canopy: jq is required (https://jqlang.org/download/)" >&2
	exit 1
fi
if [ ! -x "$claim_path" ] || [ ! -x "$release_path" ]; then
	echo "canopy: expected executable claim.sh/release.sh next to this script in ${script_dir}" >&2
	exit 1
fi

# canopy_wire_config edits (or creates) the hooks config at $1, adding
# canopy's claim.sh/release.sh entries to the SessionStart/SubagentStart
# (claim) and Stop/SubagentStop (release) hook arrays, or removing them
# again if $2 is "uninstall". Any existing entries for other commands are
# preserved.
canopy_wire_config() {
	config_path="$1"
	mode="$2"

	mkdir -p "$(dirname -- "$config_path")"
	if [ -f "$config_path" ]; then
		base=$(cat "$config_path")
	else
		base='{}'
	fi

	echo "$base" | jq \
		--arg claim "$claim_path" \
		--arg release "$release_path" \
		--arg mode "$mode" '
		def dropCanopy($cmd):
			map(select((.hooks // []) | map(.command) | index($cmd) | not));
		def addEntry($event; $cmd):
			(.hooks[$event] //= []) |
			(.hooks[$event] |= dropCanopy($cmd)) |
			(if $mode == "install" then
				.hooks[$event] += [{"hooks": [{"type": "command", "command": $cmd}]}]
			else . end);

		.hooks //= {} |
		addEntry("SessionStart"; $claim) |
		addEntry("SubagentStart"; $claim) |
		addEntry("Stop"; $release) |
		addEntry("SubagentStop"; $release)
	' >"${config_path}.tmp"
	mv "${config_path}.tmp" "$config_path"
}

mode="install"
verb="Installed"
if [ "$UNINSTALL" -eq 1 ]; then
	mode="uninstall"
	verb="Removed"
fi

if [ "$WIRE_CLAUDE" -eq 1 ]; then
	claude_settings="${HOME}/.claude/settings.json"
	canopy_wire_config "$claude_settings" "$mode"
	echo "canopy: ${verb} hooks in ${claude_settings}"
fi

if [ "$WIRE_CODEX" -eq 1 ]; then
	codex_home="${CODEX_HOME:-${HOME}/.codex}"
	codex_hooks="${codex_home}/hooks.json"
	canopy_wire_config "$codex_hooks" "$mode"
	echo "canopy: ${verb} hooks in ${codex_hooks}"
fi

if [ "$UNINSTALL" -eq 0 ]; then
	echo "canopy: done. Start a new session for the hooks to take effect."
fi
