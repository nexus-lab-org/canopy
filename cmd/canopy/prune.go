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

func newPruneCmd() *cobra.Command {
	var jsonOut bool
	var includeUnlanded bool

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Reclaim worktrees with a stale claim and a clean working tree back into the pool",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}

			report, err := p.Prune(includeUnlanded)
			if err != nil {
				return err
			}

			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			return printPruneReport(cmd.OutOrStdout(), report)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the prune report as JSON")
	cmd.Flags().BoolVar(&includeUnlanded, "include-unlanded", false, "accepted for CLI parity with destroy; currently a no-op for prune (see Pool.Prune doc comment)")

	return cmd
}

// printPruneReport renders a PruneReport as two simple aligned tables:
// what got reclaimed, and what was left alone (with why).
func printPruneReport(w io.Writer, report *pool.PruneReport) error {
	if len(report.Reclaimed) == 0 && len(report.Skipped) == 0 {
		fmt.Fprintln(w, "nothing to prune")
		return nil
	}

	if len(report.Reclaimed) > 0 {
		fmt.Fprintln(w, "RECLAIMED")
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tBRANCH\tHOLDER")
		for _, e := range report.Reclaimed {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Path, e.Branch, e.Holder)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(report.Skipped) > 0 {
		fmt.Fprintln(w, "SKIPPED")
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tBRANCH\tHOLDER\tREASON")
		for _, e := range report.Skipped {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Path, e.Branch, e.Holder, e.Reason)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	return nil
}
