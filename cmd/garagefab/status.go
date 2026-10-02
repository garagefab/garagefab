// Package main is the entry point for the garagefab binary.
//
// status.go implements the "garagefab status" command (spec CLI-4).
// It queries the SQLite database directly and prints an aligned table of all
// currently active (running, queued, or blocked) jobs requiring attention.
// It can be run independently while the factory service is running in the background.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/garagefab/garagefab/internal/config"
	"github.com/garagefab/garagefab/internal/store"
)

// statusCmd defines the Cobra CLI specification for "garagefab status".
// RunE is used instead of Run so that errors can be returned directly up the Cobra call chain.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print active and attention-needing jobs (CLI-4)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. Load configuration to locate the data directory.
		cfg, err := config.Load(dataDirFlag)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		// 2. Open SQLite database connection in read-only/shared WAL mode.
		dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
		db, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		defer db.Close() // Ensure connection pool is closed on exit.

		// 3. Obtain execution context (supports cancellation via Ctrl+C).
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		// 4. Query active jobs: status in ('running', 'queued', 'blocked').
		items, err := db.Jobs().ListActiveStatusJobs(ctx)
		if err != nil {
			return fmt.Errorf("fetch active jobs: %w", err)
		}

		// Empty check.
		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No running jobs or attention items.")
			return nil
		}

		// 5. Format output using text/tabwriter for clean terminal column alignment.
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "JOB ID\tPROJECT\tWORK TYPE\tSTAGE\tSTATUS\tTITLE")
		for _, item := range items {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
				item.ID, item.ProjectName, item.WorkType, item.Stage, item.Status, item.Title)
		}
		return w.Flush()
	},
}

// init registers the status subcommand into the root command tree automatically on startup.
func init() {
	rootCmd.AddCommand(statusCmd)
}
