package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/spf13/cobra"
)

var adminStatsCmd = &cobra.Command{
	Use:   "db-stats",
	Short: "Show database table sizes and row counts",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		if storeDSN == "" {
			return fmt.Errorf("no database connection — set up ~/.cobuild/config.yaml or COBUILD_* env vars")
		}

		conn, err := cliutil.ConnectPostgres(ctx, storeDSN)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer conn.Close(ctx)

		fmt.Println("CoBuild Database Stats")
		fmt.Println("======================")
		fmt.Println()

		fmt.Printf("%-30s %8s %10s\n", "Table", "Rows", "Size")
		fmt.Printf("%-30s %8s %10s\n", "-----", "----", "----")

		tables := []string{
			"pipeline_runs",
			"pipeline_gates",
			"pipeline_tasks",
			"pipeline_sessions",
			"pipeline_session_events",
		}

		for _, table := range tables {
			var count int
			err := conn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
			if err != nil {
				fmt.Printf("%-30s %8s %10s\n", table, "error", "-")
				continue
			}

			var size string
			err = conn.QueryRow(ctx, fmt.Sprintf("SELECT pg_size_pretty(pg_total_relation_size('%s'))", table)).Scan(&size)
			if err != nil {
				size = "-"
			}

			fmt.Printf("%-30s %8d %10s\n", table, count, size)
		}

		// Total
		var totalSize string
		conn.QueryRow(ctx, `
			SELECT pg_size_pretty(
				pg_total_relation_size('pipeline_runs') +
				pg_total_relation_size('pipeline_gates') +
				pg_total_relation_size('pipeline_tasks') +
				pg_total_relation_size('pipeline_sessions') +
				pg_total_relation_size('pipeline_session_events')
			)
		`).Scan(&totalSize)

		fmt.Printf("\n%-30s %8s %10s\n", "TOTAL", "", totalSize)

		return nil
	},
}

func init() {
	adminCmd.AddCommand(adminStatsCmd)
}
