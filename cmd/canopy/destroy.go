package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nexus-lab-org/canopy/internal/pool"
)

func newDestroyCmd() *cobra.Command {
	var includeUnlanded bool
	var includeDirty bool
	var allIdle bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "destroy <path>",
		Short: "Permanently remove a worktree from disk and git, freeing pool capacity for good",
		Args: func(cmd *cobra.Command, args []string) error {
			if allIdle {
				if len(args) != 0 {
					return errors.New("canopy: destroy --all-idle takes no <path> argument")
				}
				return nil
			}
			if len(args) != 1 {
				return errors.New("canopy: destroy requires exactly one <path> argument, or --all-idle")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			p, err := pool.Open(wd)
			if err != nil {
				return err
			}

			opts := pool.DestroyOptions{
				IncludeUnlanded: includeUnlanded,
				IncludeDirty:    includeDirty,
			}

			if allIdle {
				report, err := p.DestroyAllIdle(opts)
				if err != nil {
					return err
				}
				if jsonOut {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(report)
				}
				return printDestroyReport(cmd.OutOrStdout(), report)
			}

			result, err := p.Destroy(args[0], opts)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "destroyed %s (branch %s)\n", result.Path, result.Branch)
			return nil
		},
	}

	cmd.Flags().BoolVar(&includeUnlanded, "include-unlanded", false, "destroy even if the branch has not been merged into the default branch")
	cmd.Flags().BoolVar(&includeDirty, "include-dirty", false, "destroy even if the working tree has uncommitted changes")
	cmd.Flags().BoolVar(&allIdle, "all-idle", false, "apply to every currently-unclaimed worktree in the pool, instead of a single <path>")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the result as JSON")

	return cmd
}

// printDestroyReport renders a DestroyReport as two simple aligned
// tables: what got destroyed, and what was left alone (with why).
func printDestroyReport(w io.Writer, report *pool.DestroyReport) error {
	if len(report.Destroyed) == 0 && len(report.Skipped) == 0 {
		fmt.Fprintln(w, "nothing to destroy")
		return nil
	}

	if len(report.Destroyed) > 0 {
		fmt.Fprintln(w, "DESTROYED")
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tBRANCH")
		for _, e := range report.Destroyed {
			fmt.Fprintf(tw, "%s\t%s\n", e.Path, e.Branch)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(report.Skipped) > 0 {
		fmt.Fprintln(w, "SKIPPED")
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tBRANCH\tREASON")
		for _, e := range report.Skipped {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Path, e.Branch, e.Reason)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	return nil
}
