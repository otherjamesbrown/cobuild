package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/otherjamesbrown/cobuild/internal/contextaudit"
	"github.com/spf13/cobra"
)

var contextAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Report dispatch context size and flag bloat",
	Long: `Walks .cobuild/context/ and prints a per-file size breakdown.

Large or polluted context files slow dispatched agents, spend tokens on
irrelevant content, and often correlate with loopy/confused agent output.
This command surfaces which files to trim. See docs/guides/context-optimization.md
for guidance.`,
	Example: `  cobuild context audit
  cobuild context audit --json
  cobuild context audit --path /path/to/repo`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pathFlag, _ := cmd.Flags().GetString("path")
		jsonOut, _ := cmd.Flags().GetBool("json")

		repoRoot := pathFlag
		if repoRoot == "" {
			repoRoot = findRepoRoot()
		}
		if repoRoot == "" {
			return fmt.Errorf("could not determine repo root; pass --path")
		}

		report, err := contextaudit.Inspect(repoRoot)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}

		printAuditText(cmd.OutOrStdout(), report)
		return nil
	},
}

func printAuditText(w io.Writer, r *contextaudit.Report) {
	fmt.Fprintf(w, "Context audit for %s\n\n", r.RepoRoot)
	if len(r.Entries) == 0 {
		fmt.Fprintln(w, "No files found under .cobuild/context/.")
		return
	}

	fmt.Fprintf(w, "%-50s %10s  %s\n", "File", "Size", "Flags")
	fmt.Fprintf(w, "%-50s %10s  %s\n", "----", "----", "-----")
	for _, e := range r.Entries {
		flags := ""
		if len(e.Flags) > 0 {
			flags = joinFlags(e.Flags)
		}
		fmt.Fprintf(w, "%-50s %10s  %s\n", e.RelPath, contextaudit.FormatKB(e.Bytes), flags)
		if e.Annotation != nil {
			printAuditAnnotation(w, e.Annotation)
		}
	}

	fmt.Fprintf(w, "\nTotal on-disk layer bytes: %s across %d files\n",
		contextaudit.FormatKB(r.TotalBytes), len(r.Entries))
	fmt.Fprintf(w, "Flagged: %d file(s)\n", r.FlaggedCount)

	// Assembled-total warning. On-disk total is a lower bound for the
	// assembled context, since config-driven layers (skills-dir, work-item
	// fetches, claude-md) may add more. But any individual on-disk total
	// past the warn threshold is already a strong signal.
	if r.TotalBytes >= contextaudit.AssembledHighBytes {
		fmt.Fprintf(w, "\n⚠ On-disk layers already exceed %s — dispatched agents will see substantially more. Trim aggressively.\n",
			contextaudit.FormatKB(contextaudit.AssembledHighBytes))
	} else if r.TotalBytes >= contextaudit.AssembledWarnBytes {
		fmt.Fprintf(w, "\nOn-disk layers exceed %s; the assembled total (what the agent sees) will be higher.\n",
			contextaudit.FormatKB(contextaudit.AssembledWarnBytes))
	}
}

func printAuditAnnotation(w io.Writer, a *contextaudit.Annotation) {
	printAuditField(w, "Owner:", a.Owner)
	printAuditField(w, "Why large:", a.WhyLarge)
	printAuditListField(w, "Try here:", a.TryHere)
	printAuditField(w, "File here:", a.FileHere)
}

func printAuditField(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "  %-10s %s\n", label, value)
}

func printAuditListField(w io.Writer, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(w, "  %-10s %s\n", label, values[0])
	for _, value := range values[1:] {
		fmt.Fprintf(w, "  %-10s %s\n", "", value)
	}
}

func joinFlags(flags []string) string {
	out := ""
	for i, f := range flags {
		if i > 0 {
			out += ","
		}
		out += f
	}
	return out
}

func init() {
	contextAuditCmd.Flags().String("path", "", "Repo root to audit (defaults to auto-detected repo)")
	contextAuditCmd.Flags().Bool("json", false, "Emit JSON instead of a human-readable table")
	contextCmd.AddCommand(contextAuditCmd)
}
