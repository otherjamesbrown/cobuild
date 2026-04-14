package cmd

import (
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Inspect and audit dispatch context",
	Long:  `Commands for inspecting the .cobuild/context/ layers that feed dispatched agents.`,
}

func init() {
	rootCmd.AddCommand(contextCmd)
}
