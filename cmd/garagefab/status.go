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

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print active and attention-needing jobs (CLI-4)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(dataDirFlag)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		dbPath := filepath.Join(cfg.DataDir, "garagefab.db")
		db, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		defer db.Close()

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		items, err := db.Jobs().ListActiveStatusJobs(ctx)
		if err != nil {
			return fmt.Errorf("fetch active jobs: %w", err)
		}

		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No running jobs or attention items.")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "JOB ID\tPROJECT\tWORK TYPE\tSTAGE\tSTATUS\tTITLE")
		for _, item := range items {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
				item.ID, item.ProjectName, item.WorkType, item.Stage, item.Status, item.Title)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
