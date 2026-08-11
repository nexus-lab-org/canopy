# canopy ships as an agent skill now

canopy already automates the annoying part of running agentic coding
sessions in worktrees — claim on start, release on stop, no manual
bookkeeping. The one piece that stayed manual was *getting canopy
itself* set up: installing the binary, wiring the hooks, running `init`
in a new repo. That's now a [SKILL.md](../../.claude/skills/canopy/SKILL.md)
in this repo, so an agent can do that setup for you too.

## What the skill covers

The `canopy` skill (`.claude/skills/canopy/SKILL.md`) walks an agent
through the same steps a human would follow from the README:

1. Check whether `canopy` is already installed and whether the current
   repo already has a pool.
2. Install it — `go install` if a Go toolchain is available, otherwise
   the curl-to-install script — with explicit confirmation before
   running anything that pipes a remote script into `sh`.
3. Wire the user-level Claude Code / Codex hooks via `./hooks/install.sh`,
   again with confirmation since it edits global config.
4. Run `canopy init` in the target repo.
5. Drive day-to-day commands (`status`, `prune`, `destroy`, `enter`,
   manual `claim`/`release`) when asked, with the safety rails
   (`--include-dirty`, `--force`, etc.) treated as guardrails rather than
   obstacles to bypass by default.

## Installing the skill

It's a standard [Agent Skill](https://skills.sh), so the
[`skills` CLI](https://github.com/vercel-labs/skills) picks it up
directly from this repo:

```sh
npx skills add nexus-lab-org/canopy --skill canopy
```

That copies it into whichever agents you have installed locally
(Claude Code, Codex, Cursor, and dozens more — `npx skills add --help`
lists them) at project or global scope.

## Or just hand an agent this prompt

If you'd rather skip the CLI, paste this into any repo-aware coding
agent (with tool/shell access) and it'll do the same setup itself:

```
Set up canopy (https://github.com/nexus-lab-org/canopy) for this repo.

Check whether the `canopy` CLI is already installed and whether this
repo already has a worktree pool (`canopy status`). If canopy isn't
installed, install it — prefer `go install
github.com/nexus-lab-org/canopy/cmd/canopy@latest` if I have a Go
toolchain, otherwise use the curl-to-install script documented at
https://github.com/nexus-lab-org/canopy#installation. Confirm with me
before running the curl script or before editing my global hook config.

Once canopy is installed, ask me whether I also want its Claude
Code/Codex hooks wired (auto claim/release on session start/stop via
`./hooks/install.sh` from a clone of the repo) and whether I want
`canopy init` run in this repo now.
```

An agent with the skill installed (previous section) will follow the
same steps without needing this pasted in — the prompt above is for
one-off use when you don't want to install anything first.
