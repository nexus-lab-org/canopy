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
