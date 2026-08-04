---
id: 7
title: Distribution/install story
type: wayfinder:grilling
status: closed
assignee: asif (claude session)
blocked_by: []
---

## Question

Now that [Language / runtime choice](003-language-runtime-choice.md) has
landed on Go targeting macOS + Linux, how do users install `canopy`?
Options to weigh, following treehouse's own precedent (curl script / Nix /
`go install`): a curl-to-install script pulling prebuilt binaries from
GitHub releases, `go install github.com/.../canopy@latest`, Homebrew tap,
or some combination. Decide the primary supported path and which
others (if any) are also maintained.

## Resolution

Two supported paths, no others for now:

- **`go install github.com/<org>/canopy@latest`** — zero-maintenance
  (no release pipeline needed, works the moment the repo is public),
  matches what anyone already carrying a Go toolchain expects.
- **Curl-to-install script pulling prebuilt binaries from GitHub
  Releases** (built via `goreleaser` or similar) — covers everyone
  without a Go toolchain, which matters since canopy is meant to be
  dropped into hooks on any dev machine, not just Go developers' machines.

Homebrew and Nix explicitly skipped for now: both are ongoing maintenance
overhead (formula/package upkeep, staying in sync with releases) that
isn't justified before the tool has real users. Can be added later
without breaking either of the two paths above.
