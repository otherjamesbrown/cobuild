package cmd

import (
	"strings"
	"testing"
)

func TestWritePhasePromptDecomposeMentionsCompletionModeDirect(t *testing.T) {
	var b strings.Builder
	writePhasePrompt(&b, "decompose", "cb-parent", "cb-parent", nil)
	got := b.String()

	for _, want := range []string{
		"set `completion_mode: direct` only for non-code tasks",
		"leave it unset and let `cobuild complete` auto-detect",
		"`cxp shard metadata set <task-id> completion_mode direct`",
		"tasks tagged `completion_mode: direct`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("decompose prompt missing %q\nprompt:\n%s", want, got)
		}
	}
}

func TestHasInvestigationContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty body",
			content: "",
			want:    false,
		},
		{
			name:    "plain bug report, no investigation",
			content: "## Description\n\nServer crashes on startup.\n\n## Steps to Reproduce\n\n1. Run server\n2. Observe crash",
			want:    false,
		},
		{
			name:    "has investigation report heading",
			content: "## Description\n\nServer crashes.\n\n## Investigation Report\n\nFound null pointer in auth middleware.",
			want:    true,
		},
		{
			name:    "has root cause heading",
			content: "## Description\n\nServer crashes.\n\n## Root Cause\n\nMissing nil check in auth.go:42.",
			want:    true,
		},
		{
			name:    "has fix applied heading",
			content: "## Description\n\nServer crashes.\n\n## Fix Applied\n\nAdded nil check.",
			want:    true,
		},
		{
			name:    "has fix heading",
			content: "## Description\n\nServer crashes.\n\n## Fix\n\n- [ ] Add nil check in auth.go",
			want:    true,
		},
		{
			name:    "case insensitive - uppercase",
			content: "## INVESTIGATION REPORT\n\nFound the issue.",
			want:    true,
		},
		{
			name:    "case insensitive - mixed",
			content: "## Root Cause\n\nThe bug is here.",
			want:    true,
		},
		{
			name:    "heading in prose, not heading level",
			content: "The investigation report showed nothing useful here.",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasInvestigationContent(tt.content)
			if got != tt.want {
				t.Errorf("hasInvestigationContent(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestInvestigationContentDowngrade(t *testing.T) {
	// These test the 4 combinations of label × investigation content.
	// The dispatch logic is: if label=needs-investigation → investigate, else → fix.
	// Then: if phase=investigate AND hasInvestigationContent → downgrade to fix.

	type input struct {
		hasNeedsInvestigationLabel bool
		hasInvestigationBody       bool
	}
	type want struct {
		phase string
	}

	tests := []struct {
		name  string
		input input
		want  want
	}{
		{
			name:  "label=false, investigation=false → fix (normal bug)",
			input: input{false, false},
			want:  want{"fix"},
		},
		{
			name:  "label=true, investigation=false → investigate (escalation path)",
			input: input{true, false},
			want:  want{"investigate"},
		},
		{
			name:  "label=false, investigation=true → fix (already investigated, default path)",
			input: input{false, true},
			want:  want{"fix"},
		},
		{
			name:  "label=true, investigation=true → fix (downgrade: body overrides label)",
			input: input{true, true},
			want:  want{"fix"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the dispatch.go phase inference + downgrade logic
			labels := []string{}
			if tt.input.hasNeedsInvestigationLabel {
				labels = []string{"needs-investigation"}
			}

			content := "## Description\n\nSome bug."
			if tt.input.hasInvestigationBody {
				content += "\n\n## Investigation Report\n\nFound the root cause."
			}

			// Phase inference (mirrors dispatch.go fallback logic)
			phase := "fix"
			if hasLabel(labels, "needs-investigation") {
				phase = "investigate"
			}

			// Downgrade (mirrors dispatch.go post-inference check)
			if phase == "investigate" && hasInvestigationContent(content) {
				phase = "fix"
			}

			if phase != tt.want.phase {
				t.Errorf("phase = %q, want %q", phase, tt.want.phase)
			}
		})
	}
}
