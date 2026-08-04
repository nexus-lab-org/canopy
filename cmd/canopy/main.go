// Command canopy manages a pool of git worktrees for agentic coding
// sessions. See .context/spec-canopy.md at the repo root for full design.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "canopy:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "canopy",
		Short:         "Manage a pool of git worktrees for agentic coding sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInitCmd())
	root.AddCommand(newClaimCmd())
	root.AddCommand(newReleaseCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newPruneCmd())
	return root
}
