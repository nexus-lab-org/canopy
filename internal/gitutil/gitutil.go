// Package gitutil shells out to the git binary for the handful of
// operations canopy needs: locating repo directories and creating
// worktrees. Shelling out (rather than a Go git library) keeps behavior
// identical to whatever git the user already has installed.
package gitutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// run executes git with the given args in dir and returns trimmed stdout.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Toplevel returns the working tree root of the repo containing dir.
func Toplevel(dir string) (string, error) {
	return run(dir, "rev-parse", "--show-toplevel")
}

// CommonDir returns the absolute path to the repo's shared .git directory
// (the main repo's .git dir, even when dir is inside a linked worktree).
func CommonDir(dir string) (string, error) {
	return run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// WorktreeAdd creates a new worktree at path on a new branch checked out
// from the repo's current HEAD, running `git worktree add <path> -b
// <branch>` inside repoDir.
func WorktreeAdd(repoDir, path, branch string) error {
	_, err := run(repoDir, "worktree", "add", path, "-b", branch)
	return err
}

// IsClean reports whether the working tree at dir has no uncommitted
// changes: no staged changes, no unstaged modifications, and no
// untracked files. It runs `git status --porcelain`, which prints one
// line per changed/untracked path and nothing at all when the tree is
// clean.
func IsClean(dir string) (bool, error) {
	out, err := run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// DefaultBranch returns the branch that canopy treats as the "landed
// upstream" branch for merge-detection purposes (used by `destroy`'s
// unmerged-branch safety check). It uses the branch currently checked
// out in the primary repo at repoDir, since that's the branch every
// canopy-managed worktree is forked from (see WorktreeAdd) — so it's
// the natural "has this landed yet?" comparison point without requiring
// any config. If the primary repo's HEAD is detached (unusual, but
// possible if an operator checked out a specific commit there), it
// falls back to whichever of the common default-branch names ("main",
// "master") exists locally.
func DefaultBranch(repoDir string) (string, error) {
	if branch, err := run(repoDir, "symbolic-ref", "--short", "HEAD"); err == nil && branch != "" {
		return branch, nil
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := run(repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not determine a default branch: primary repo's HEAD is detached and neither main nor master exists")
}

// IsMerged reports whether every commit reachable from branch is also
// reachable from base — i.e. branch has been fully merged into base —
// via `git merge-base --is-ancestor branch base`, which exits 0 when
// true and a non-error nonzero status (1) when false.
func IsMerged(dir, branch, base string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", branch, base)
	if dir != "" {
		cmd.Dir = dir
	}
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", branch, base, err)
}

// WorktreeRemove unregisters and deletes the worktree at path, running
// `git worktree remove [--force] path` inside repoDir. force is only
// meant to be passed once canopy's own dirty-working-tree safety check
// (--include-dirty) has already been satisfied — it exists because git
// itself refuses to remove a worktree with uncommitted changes even
// when the caller has separately confirmed that's acceptable.
func WorktreeRemove(repoDir, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := run(repoDir, args...)
	return err
}
