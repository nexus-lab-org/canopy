package main

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/nexus-lab-org/canopy/internal/pool"
)

func newEnterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enter <path>",
		Short: "Drop into a shell in the given worktree directory",
		Long: `Opens an interactive subshell in the given worktree directory.
This is purely for manual inspection and does not claim the worktree
or otherwise change pool state.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}

			// Validate that this path is a known worktree (read-only check).
			wt, err := p.ResolveWorktreePath(path)
			if err != nil {
				return err
			}

			// Open an interactive shell in the worktree.
			return spawnShell(wt.Path)
		},
	}

	return cmd
}

// spawnShell launches an interactive shell with its working directory
// set to the given path. The shell is read from $SHELL (falling back to
// /bin/sh if unset) and is wired to the parent's Stdin/Stdout/Stderr
// so it is fully interactive.
func spawnShell(path string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell)
	cmd.Dir = path
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
