package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type wasteFlag struct {
	Pattern      string `json:"pattern"`
	Description  string `json:"description"`
	TokensWasted int    `json:"tokens_wasted"`
	Suggestion   string `json:"suggestion"`
	Session      string `json:"session,omitempty"`
}

var adminWasteCmd = &cobra.Command{
	Use:   "waste [events-file]",
	Short: "Detect token waste patterns from session events",
	Long: `Analyses session event logs to find token waste patterns:
  - Repeated file reads (same file read multiple times)
  - Large reads where anatomy description existed
  - Context bloat (compaction triggered)
  - Error loops (same command failing repeatedly)

Run on a specific events.jsonl file or scans all session archives.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot := findRepoRoot()

		var eventFiles []string
		if len(args) > 0 {
			eventFiles = append(eventFiles, args[0])
		} else {
			// Scan session archives and current worktrees
			archiveDir := filepath.Join(repoRoot, ".cobuild", "sessions")
			filepath.Walk(archiveDir, func(path string, info os.FileInfo, err error) error {
				if err == nil && info.Name() == "events.jsonl" {
					eventFiles = append(eventFiles, path)
				}
				return nil
			})
			// Also check current worktrees
			home, _ := os.UserHomeDir()
			wtBase := filepath.Join(home, "worktrees")
			filepath.Walk(wtBase, func(path string, info os.FileInfo, err error) error {
				if err == nil && info.Name() == "events.jsonl" && strings.Contains(path, ".cobuild") {
					eventFiles = append(eventFiles, path)
				}
				return nil
			})
		}

		if len(eventFiles) == 0 {
			fmt.Println("No event files found. Run sessions with hooks enabled to generate events.")
			return nil
		}

		var allFlags []wasteFlag
		for _, f := range eventFiles {
			flags := detectWaste(f)
			allFlags = append(allFlags, flags...)
		}

		if len(allFlags) == 0 {
			fmt.Println("No waste patterns detected.")
			return nil
		}

		// Sort by tokens wasted
		sort.Slice(allFlags, func(i, j int) bool {
			return allFlags[i].TokensWasted > allFlags[j].TokensWasted
		})

		totalWasted := 0
		fmt.Printf("Token Waste Analysis (%d patterns found)\n", len(allFlags))
		fmt.Println("==========================================")
		fmt.Println()

		fmt.Printf("%-25s %-8s %s\n", "PATTERN", "TOKENS", "DESCRIPTION")
		fmt.Printf("%-25s %-8s %s\n", "-------", "------", "-----------")
		for _, f := range allFlags {
			fmt.Printf("%-25s %-8s %s\n", f.Pattern, formatTokensShort(f.TokensWasted), f.Description)
			totalWasted += f.TokensWasted
		}

		fmt.Printf("\nTotal estimated waste: ~%s tokens\n", formatTokensShort(totalWasted))

		if totalWasted > 10000 {
			fmt.Println()
			fmt.Println("Suggestions:")
			seen := make(map[string]bool)
			for _, f := range allFlags {
				if !seen[f.Pattern] {
					fmt.Printf("  • %s\n", f.Suggestion)
					seen[f.Pattern] = true
				}
			}
		}

		return nil
	},
}

type eventLine struct {
	Timestamp       string `json:"ts"`
	Event           string `json:"event"`
	File            string `json:"file"`
	TokensEstimated int    `json:"tokens_estimated"`
	TokensSaved     int    `json:"tokens_saved"`
	ReadCount       int    `json:"read_count"`
	Command         string `json:"command"`
	Error           string `json:"error"`
}

func detectWaste(eventsPath string) []wasteFlag {
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return nil
	}

	var events []eventLine
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e eventLine
		if json.Unmarshal([]byte(line), &e) == nil {
			events = append(events, e)
		}
	}

	sessionName := filepath.Base(filepath.Dir(eventsPath))
	var flags []wasteFlag

	// Pattern 1: Repeated file reads
	readCounts := make(map[string]int)
	readTokens := make(map[string]int)
	for _, e := range events {
		if e.Event == "file_read" || e.Event == "repeated_read" {
			readCounts[e.File]++
			if e.TokensEstimated > 0 {
				readTokens[e.File] = e.TokensEstimated
			}
			if e.TokensSaved > 0 {
				readTokens[e.File] = e.TokensSaved
			}
		}
	}
	for file, count := range readCounts {
		if count > 1 {
			tokens := readTokens[file] * (count - 1)
			flags = append(flags, wasteFlag{
				Pattern:      "repeated_read",
				Description:  fmt.Sprintf("%s read %dx (~%d tok wasted)", filepath.Base(file), count, tokens),
				TokensWasted: tokens,
				Suggestion:   "Hooks should warn about repeated reads. Check if cobuild-event.sh is installed.",
				Session:      sessionName,
			})
		}
	}

	// Pattern 2: Large file reads (>1000 tokens)
	for _, e := range events {
		if e.Event == "file_read" && e.TokensEstimated > 1000 {
			flags = append(flags, wasteFlag{
				Pattern:      "large_read",
				Description:  fmt.Sprintf("%s is ~%d tokens — could anatomy description suffice?", filepath.Base(e.File), e.TokensEstimated),
				TokensWasted: e.TokensEstimated / 2, // conservative — maybe half was unnecessary
				Suggestion:   "Run cobuild scan to generate anatomy. Large files may not need full reads.",
				Session:      sessionName,
			})
		}
	}

	// Pattern 3: Compaction events (context was full)
	compactions := 0
	for _, e := range events {
		if e.Event == "compact_start" {
			compactions++
		}
	}
	if compactions > 0 {
		flags = append(flags, wasteFlag{
			Pattern:      "context_overflow",
			Description:  fmt.Sprintf("%d compaction(s) — context window filled", compactions),
			TokensWasted: compactions * 50000, // rough estimate of lost context
			Suggestion:   "Reduce context layers or split into smaller tasks. Each compaction loses context.",
			Session:      sessionName,
		})
	}

	// Pattern 4: Error loops (same command failing repeatedly)
	errorCmds := make(map[string]int)
	for _, e := range events {
		if e.Event == "turn_error" {
			errorCmds[e.Error]++
		}
	}
	for errType, count := range errorCmds {
		if count > 2 {
			flags = append(flags, wasteFlag{
				Pattern:      "error_loop",
				Description:  fmt.Sprintf("Error '%s' occurred %d times", errType, count),
				TokensWasted: count * 500, // each failed turn wastes tokens
				Suggestion:   "Repeated errors waste tokens on retries. Check if the task spec is clear enough.",
				Session:      sessionName,
			})
		}
	}

	return flags
}

func init() {
	adminCmd.AddCommand(adminWasteCmd)
}
