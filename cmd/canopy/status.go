package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/asif/canopy/internal/pool"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "List every worktree in the pool with claim, liveness, and cleanliness info",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}

			statuses, err := p.Status()
			if err != nil {
				return err
			}

			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(statuses)
			}
			return printStatusTable(cmd.OutOrStdout(), statuses)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit status as a JSON array")

	return cmd
}

// printStatusTable renders statuses as a simple aligned table: path,
// branch, holder ("-" when unclaimed), liveness ("live"/"stale"/"idle"),
// and clean/dirty.
func printStatusTable(w io.Writer, statuses []*pool.WorktreeStatus) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tBRANCH\tHOLDER\tLIVENESS\tWORKTREE")
	for _, st := range statuses {
		holder := st.Holder
		if holder == "" {
			holder = "-"
		}
		clean := "clean"
		if !st.Clean {
			clean = "dirty"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", st.Path, st.Branch, holder, st.Liveness, clean)
	}
	return tw.Flush()
}
