package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/spf13/cobra"
)

type transcriptUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type transcriptMessage struct {
	Message *struct {
		Usage *transcriptUsage `json:"usage"`
	} `json:"message"`
}

type sessionTokenSummary struct {
	Turns              int     `json:"turns"`
	InputTokens        int     `json:"input_tokens"`
	OutputTokens       int     `json:"output_tokens"`
	CacheReadTokens    int     `json:"cache_read_tokens"`
	CacheCreateTokens  int     `json:"cache_create_tokens"`
	TotalTokens        int     `json:"total_tokens"`
	EstimatedCostUSD   float64 `json:"estimated_cost_usd"`
	MaxContextTokens   int     `json:"max_context_tokens"`
}

var adminTokensCmd = &cobra.Command{
	Use:   "tokens [transcript-path]",
	Short: "Parse a Claude transcript for token usage and cost data",
	Long: `Reads a Claude Code transcript JSONL file and extracts exact token
counts from API responses. Can parse a specific file or find the most
recent transcript for this project.

Outputs: turns, input/output/cache tokens, estimated cost, max context size.`,
	Args: cobra.MaximumNArgs(1),
	Example: `  cobuild admin tokens                                    # most recent transcript
  cobuild admin tokens ~/.claude/projects/.../session.jsonl  # specific file
  cobuild admin tokens --write-session ps-abc123            # write to DB`,
	RunE: func(cmd *cobra.Command, args []string) error {
		writeSession, _ := cmd.Flags().GetString("write-session")

		var transcriptPath string
		if len(args) > 0 {
			transcriptPath = args[0]
		} else {
			// Find most recent transcript for this project
			home, _ := os.UserHomeDir()
			projectsDir := filepath.Join(home, ".claude", "projects")
			var newest string
			var newestTime int64
			filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
					return nil
				}
				if info.ModTime().Unix() > newestTime {
					newestTime = info.ModTime().Unix()
					newest = path
				}
				return nil
			})
			if newest == "" {
				return fmt.Errorf("no transcripts found in %s", projectsDir)
			}
			transcriptPath = newest
		}

		summary, err := parseTranscript(transcriptPath)
		if err != nil {
			return fmt.Errorf("parse transcript: %w", err)
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(summary)
			fmt.Println(s)
		} else {
			fmt.Printf("Transcript: %s\n\n", transcriptPath)
			fmt.Printf("Turns:           %d\n", summary.Turns)
			fmt.Printf("Input tokens:    %s\n", formatTokens(summary.InputTokens))
			fmt.Printf("Output tokens:   %s\n", formatTokens(summary.OutputTokens))
			fmt.Printf("Cache read:      %s\n", formatTokens(summary.CacheReadTokens))
			fmt.Printf("Cache creation:  %s\n", formatTokens(summary.CacheCreateTokens))
			fmt.Printf("Total tokens:    %s\n", formatTokens(summary.TotalTokens))
			fmt.Printf("Max context:     %s\n", formatTokens(summary.MaxContextTokens))
			fmt.Printf("Estimated cost:  $%.2f\n", summary.EstimatedCostUSD)
		}

		// Write to pipeline_sessions if requested
		if writeSession != "" && cbStore != nil {
			// Update the session record with token data
			// For now just print — full integration needs store method
			fmt.Printf("\nWould write to session %s (not yet implemented)\n", writeSession)
		}

		return nil
	},
}

func parseTranscript(path string) (*sessionTokenSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	summary := &sessionTokenSummary{}
	maxContext := 0

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg transcriptMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if msg.Message != nil && msg.Message.Usage != nil {
			u := msg.Message.Usage
			summary.InputTokens += u.InputTokens
			summary.OutputTokens += u.OutputTokens
			summary.CacheReadTokens += u.CacheReadInputTokens
			summary.CacheCreateTokens += u.CacheCreationInputTokens
			summary.Turns++

			// Track max context window usage
			contextSize := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			if contextSize > maxContext {
				maxContext = contextSize
			}
		}
	}

	summary.TotalTokens = summary.InputTokens + summary.OutputTokens + summary.CacheReadTokens + summary.CacheCreateTokens
	summary.MaxContextTokens = maxContext

	// Estimate cost (rough Opus pricing)
	// Input: $15/M, Output: $75/M, Cache read: $1.50/M, Cache create: $3.75/M
	summary.EstimatedCostUSD = float64(summary.InputTokens)*15/1_000_000 +
		float64(summary.OutputTokens)*75/1_000_000 +
		float64(summary.CacheReadTokens)*1.5/1_000_000 +
		float64(summary.CacheCreateTokens)*3.75/1_000_000

	return summary, nil
}

func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func init() {
	adminTokensCmd.Flags().String("write-session", "", "Write token data to a pipeline session record")
	adminCmd.AddCommand(adminTokensCmd)
}
