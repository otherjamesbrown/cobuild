package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/spf13/cobra"
)

var workItemCmd = &cobra.Command{
	Use:     "work-item",
	Aliases: []string{"wi"},
	Short:   "Work item operations via the connector",
	Long:    `Read and write work items through the configured connector (Context Palace, Beads, etc.).`,
}

var wiShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a work item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		item, err := conn.Get(ctx, args[0])
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(item)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("ID:      %s\n", item.ID)
		fmt.Printf("Title:   %s\n", item.Title)
		fmt.Printf("Type:    %s\n", item.Type)
		fmt.Printf("Status:  %s\n", item.Status)
		if item.Project != "" {
			fmt.Printf("Project: %s\n", item.Project)
		}
		if len(item.Labels) > 0 {
			fmt.Printf("Labels:  %s\n", strings.Join(item.Labels, ", "))
		}
		fmt.Println()
		if item.Content != "" {
			fmt.Println(item.Content)
		}
		return nil
	},
}

var wiListCmd = &cobra.Command{
	Use:   "list",
	Short: "List work items",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		wiType, _ := cmd.Flags().GetString("type")
		wiStatus, _ := cmd.Flags().GetString("status")
		wiProject, _ := cmd.Flags().GetString("project")
		wiLimit, _ := cmd.Flags().GetInt("limit")

		if wiProject == "" {
			wiProject = projectName
		}

		result, err := conn.List(ctx, connector.ListFilters{
			Type:    wiType,
			Status:  wiStatus,
			Project: wiProject,
			Limit:   wiLimit,
		})
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(result)
			fmt.Println(s)
			return nil
		}

		if len(result.Items) == 0 {
			fmt.Println("No work items found.")
			return nil
		}

		// Table output
		fmt.Printf("%-12s %-10s %-12s %s\n", "ID", "TYPE", "STATUS", "TITLE")
		fmt.Printf("%-12s %-10s %-12s %s\n", "----", "----", "------", "-----")
		for _, item := range result.Items {
			fmt.Printf("%-12s %-10s %-12s %s\n",
				item.ID,
				item.Type,
				item.Status,
				client.Truncate(item.Title, 60))
		}
		if result.Total > len(result.Items) {
			fmt.Printf("\nShowing %d of %d\n", len(result.Items), result.Total)
		}
		return nil
	},
}

var wiLinksCmd = &cobra.Command{
	Use:   "links <id>",
	Short: "Show relationships for a work item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		direction, _ := cmd.Flags().GetString("direction")
		linkTypes, _ := cmd.Flags().GetStringSlice("type")

		var types []string
		if len(linkTypes) > 0 {
			types = linkTypes
		}

		edges, err := conn.GetEdges(ctx, args[0], direction, types)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(edges)
			fmt.Println(s)
			return nil
		}

		if len(edges) == 0 {
			fmt.Println("No links found.")
			return nil
		}

		fmt.Printf("%-10s %-14s %-12s %-10s %s\n", "DIRECTION", "TYPE", "ID", "STATUS", "TITLE")
		fmt.Printf("%-10s %-14s %-12s %-10s %s\n", "---------", "----", "----", "------", "-----")
		for _, e := range edges {
			fmt.Printf("%-10s %-14s %-12s %-10s %s\n",
				e.Direction,
				e.EdgeType,
				e.ItemID,
				e.Status,
				client.Truncate(e.Title, 50))
		}
		return nil
	},
}

var wiStatusCmd = &cobra.Command{
	Use:   "status <id> <status>",
	Short: "Update work item status",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}
		if err := conn.UpdateStatus(ctx, args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Updated %s → %s\n", args[0], args[1])
		return nil
	},
}

var wiAppendCmd = &cobra.Command{
	Use:   "append <id>",
	Short: "Append content to a work item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}
		body, _ := cmd.Flags().GetString("body")
		if body == "" {
			return fmt.Errorf("--body is required")
		}
		if err := conn.AppendContent(ctx, args[0], body); err != nil {
			return err
		}
		fmt.Printf("Appended to %s\n", args[0])
		return nil
	},
}

var wiLabelCmd = &cobra.Command{
	Use:   "label",
	Short: "Manage work item labels",
}

var wiLabelAddCmd = &cobra.Command{
	Use:   "add <id> <label>",
	Short: "Add a label to a work item",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}
		if err := conn.AddLabel(ctx, args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Added label %q to %s\n", args[1], args[0])
		return nil
	},
}

var wiCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new work item",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		title, _ := cmd.Flags().GetString("title")
		wiType, _ := cmd.Flags().GetString("type")
		body, _ := cmd.Flags().GetString("body")
		parent, _ := cmd.Flags().GetString("parent")
		labels, _ := cmd.Flags().GetStringSlice("label")

		if title == "" {
			return fmt.Errorf("--title is required")
		}
		if wiType == "" {
			return fmt.Errorf("--type is required")
		}

		id, err := conn.Create(ctx, connector.CreateRequest{
			Title:    title,
			Content:  body,
			Type:     wiType,
			Labels:   labels,
			ParentID: parent,
		})
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(map[string]string{"id": id})
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Created %s\n", id)
		return nil
	},
}

var wiLinkAddCmd = &cobra.Command{
	Use:   "add <from-id> <to-id> <link-type>",
	Short: "Create a relationship between work items",
	Long:  `Link types: child-of, blocked-by, relates-to, etc.`,
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}
		if err := conn.CreateEdge(ctx, args[0], args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("Linked %s → %s (%s)\n", args[0], args[1], args[2])
		return nil
	},
}

func init() {
	// List flags
	wiListCmd.Flags().String("type", "", "Filter by type (design, bug, task)")
	wiListCmd.Flags().String("status", "", "Filter by status (open, ready, in_progress, etc.)")
	wiListCmd.Flags().String("project", "", "Filter by project (default: from config)")
	wiListCmd.Flags().Int("limit", 20, "Max results")

	// Links flags
	wiLinksCmd.Flags().String("direction", "", "Filter by direction (incoming, outgoing)")
	wiLinksCmd.Flags().StringSlice("type", nil, "Filter by link type (child-of, blocked-by)")

	// Append flags
	wiAppendCmd.Flags().String("body", "", "Content to append")

	// Create flags
	wiCreateCmd.Flags().String("title", "", "Work item title")
	wiCreateCmd.Flags().String("type", "", "Work item type (design, bug, task)")
	wiCreateCmd.Flags().String("body", "", "Work item content")
	wiCreateCmd.Flags().String("parent", "", "Parent work item ID (creates child-of edge)")
	wiCreateCmd.Flags().StringSlice("label", nil, "Labels to add")

	// Label subcommands
	wiLabelCmd.AddCommand(wiLabelAddCmd)

	// Link subcommands (links = read, link add = write)
	wiLinksCmd.AddCommand(wiLinkAddCmd)

	// Register all subcommands
	workItemCmd.AddCommand(wiShowCmd)
	workItemCmd.AddCommand(wiListCmd)
	workItemCmd.AddCommand(wiLinksCmd)
	workItemCmd.AddCommand(wiStatusCmd)
	workItemCmd.AddCommand(wiAppendCmd)
	workItemCmd.AddCommand(wiLabelCmd)
	workItemCmd.AddCommand(wiCreateCmd)

	rootCmd.AddCommand(workItemCmd)
}
