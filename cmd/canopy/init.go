package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/nexus-lab-org/canopy/internal/pool"
	"github.com/nexus-lab-org/canopy/internal/state"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Set up canopy's state file for this repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}
			if err := p.Init(); err != nil {
				if errors.Is(err, state.ErrAlreadyInitialized) {
					fmt.Fprintf(cmd.OutOrStdout(), "canopy already initialized (%s)\n", p.StatePath)
					return nil
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "initialized canopy state at %s\n", p.StatePath)
			return nil
		},
	}
}
