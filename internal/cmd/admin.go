package cmd

import (
	"github.com/spf13/cobra"
)

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "System health, cleanup, and maintenance",
	Long:  `Administrative commands for maintaining a CoBuild installation.`,
}

func init() {
	rootCmd.AddCommand(adminCmd)
}
