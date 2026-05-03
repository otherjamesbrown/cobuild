package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

// sessionEndCmd is a minimal command for ending a session record.
// Primary use: runner script EXIT trap as a backstop — if the Go code
// path misses an edge case (e.g. cb-0e0482 CI-pending session leak),
// the bash cleanup catches it. Best-effort: errors are warnings, not
// fatal, because the session may already be ended by the time the trap
// fires.
var sessionEndCmd = &cobra.Command{
	Use:     "session-end <session-id>",
	Short:   "End a running session record (runner script backstop)",
	Args:    cobra.ExactArgs(1),
	Example: "  cobuild session-end ps-abc123",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		sessionID := args[0]

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}

		// Best-effort: if the session is already ended, EndSession returns
		// an error (row not found or status not running). That's fine —
		// the backstop fired after the normal path already handled it.
		note, _ := cmd.Flags().GetString("note")
		if note == "" {
			note = "ended by runner script EXIT trap (backstop)"
		}
		err := cbStore.EndSession(ctx, sessionID, store.SessionResult{
			ExitCode:       0,
			Status:         domain.StatusCompleted,
			CompletionNote: note,
		})
		if err != nil {
			fmt.Printf("session-end: %v (may already be ended)\n", err)
		} else {
			fmt.Printf("Session %s ended.\n", sessionID)
		}
		return nil
	},
}

func init() {
	sessionEndCmd.Flags().String("note", "", "Completion note")
	rootCmd.AddCommand(sessionEndCmd)
}
