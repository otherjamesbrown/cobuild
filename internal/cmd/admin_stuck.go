package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/spf13/cobra"
)

var adminStuckCmd = &cobra.Command{
	Use:   "stuck",
	Short: "Show stuck pipelines, abandoned sessions, and orphan tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		staleHours, _ := cmd.Flags().GetInt("stale-hours")
		staleDuration := time.Duration(staleHours) * time.Hour

		fmt.Println("CoBuild — Stuck Work")
		fmt.Println("====================\n")

		// Stuck pipelines
		if cbStore != nil {
			runs, err := cbStore.ListRuns(ctx, projectName)
			if err == nil {
				stuckCount := 0
				for _, r := range runs {
					if r.Status != "active" {
						continue
					}
					age := time.Since(r.LastProgress)
					if age > staleDuration {
						if stuckCount == 0 {
							fmt.Printf("Stuck Pipelines (no progress in >%dh):\n", staleHours)
						}
						fmt.Printf("  %-12s %-14s last activity: %s\n",
							r.DesignID, r.Phase, client.TimeAgo(r.LastProgress))
						stuckCount++
					}
				}
				if stuckCount == 0 {
					fmt.Println("Stuck Pipelines: none")
				}
				fmt.Println()
			}
		}

		// Abandoned sessions
		if cbStore != nil {
			sessions, err := cbStore.ListSessions(ctx, "")
			if err == nil {
				abandonedCount := 0
				for _, s := range sessions {
					if s.Status != "running" {
						continue
					}
					age := time.Since(s.StartedAt)
					if age > staleDuration {
						if abandonedCount == 0 {
							fmt.Printf("Abandoned Sessions (running for >%dh):\n", staleHours)
						}
						wtPath := ""
						if s.WorktreePath != nil {
							wtPath = *s.WorktreePath
						}
						fmt.Printf("  %-12s %-12s started: %s  worktree: %s\n",
							s.ID[:12], s.TaskID, client.TimeAgo(s.StartedAt), wtPath)
						abandonedCount++
					}
				}
				if abandonedCount == 0 {
					fmt.Println("Abandoned Sessions: none")
				}
				fmt.Println()
			}
		}

		// Orphan tasks (open, no worktree, no recent dispatch)
		if conn != nil {
			edges, err := conn.List(ctx, connectorListFilters("task", "open"))
			if err == nil {
				orphanCount := 0
				for _, item := range edges.Items {
					wt, _ := conn.GetMetadata(ctx, item.ID, "worktree_path")
					dispatched, _ := conn.GetMetadata(ctx, item.ID, "dispatched_at")
					if wt == "" && dispatched == "" {
						age := time.Since(item.CreatedAt)
						if age > staleDuration {
							if orphanCount == 0 {
								fmt.Printf("Orphan Tasks (open, never dispatched, >%dh old):\n", staleHours)
							}
							fmt.Printf("  %-12s created: %s  %s\n",
								item.ID, client.TimeAgo(item.CreatedAt),
								client.Truncate(item.Title, 50))
							orphanCount++
						}
					}
				}
				if orphanCount == 0 {
					fmt.Println("Orphan Tasks: none")
				}
			}
		}

		return nil
	},
}

func connectorListFilters(itemType, status string) connector.ListFilters {
	return connector.ListFilters{
		Type:   itemType,
		Status: status,
		Limit:  100,
	}
}

func init() {
	adminStuckCmd.Flags().Int("stale-hours", 24, "Consider items stuck after this many hours")
	adminCmd.AddCommand(adminStuckCmd)
}
