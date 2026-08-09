package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/nexus-lab-org/canopy/internal/pool"
)

func newReleaseCmd() *cobra.Command {
	var holder string
	var force bool

	cmd := &cobra.Command{
		Use:   "release",
		Short: "Return a claimed worktree to the pool",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}

			wt, err := p.Release(holder, force)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), wt.Path)
			return nil
		},
	}

	cmd.Flags().StringVar(&holder, "holder", "", "identifier for the claim to release (required)")
	cmd.Flags().BoolVar(&force, "force", false, "release even if the claim's recorded PID is still alive (human override; never invoked by a hook)")
	cmd.MarkFlagRequired("holder")

	return cmd
}
