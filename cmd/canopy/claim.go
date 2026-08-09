package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/nexus-lab-org/canopy/internal/pool"
)

func newClaimCmd() *cobra.Command {
	var holder string
	var pid int
	var max int

	cmd := &cobra.Command{
		Use:   "claim",
		Short: "Claim a worktree from the pool, creating one if none is free",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}

			effectivePID := pid
			if effectivePID == 0 {
				// Default to the parent process's PID: the process that
				// invoked `canopy claim` (e.g. an agent session's shell),
				// which is a better liveness proxy than canopy's own
				// short-lived CLI process. Callers (hook wiring) can pass
				// --pid explicitly if they have a more precise session PID.
				effectivePID = os.Getppid()
			}

			effectiveMax := max
			if !cmd.Flags().Changed("max") {
				// No --max on the command line: fall back to the pool max
				// configured in repo-level canopy.toml (0 if unconfigured,
				// same "unlimited" meaning as an explicit --max 0).
				effectiveMax = p.ConfigMax
			}

			wt, err := p.Claim(holder, effectivePID, effectiveMax)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), wt.Path)
			return nil
		},
	}

	cmd.Flags().StringVar(&holder, "holder", "", "identifier for the claiming session/subagent (required)")
	cmd.Flags().IntVar(&pid, "pid", 0, "PID to associate with this claim (defaults to the parent process's PID)")
	cmd.Flags().IntVar(&max, "max", 0, "maximum pool size (claimed + idle worktrees) the pool may auto-grow to; 0 means unlimited")
	cmd.MarkFlagRequired("holder")

	return cmd
}
