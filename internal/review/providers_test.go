package review

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseReviewResult(t *testing.T) {
	t.Run("plain json", func(t *testing.T) {
		raw := `{"verdict":"approve","findings":[{"path":"internal/review/openai.go","line":42,"priority":"medium","message":"Handle empty choices explicitly."}],"summary":"looks good"}`

		got, err := parseReviewResult(raw)
		if err != nil {
			t.Fatalf("parseReviewResult: %v", err)
		}

		want := &ReviewResult{
			Verdict: VerdictApprove,
			Findings: []Finding{{
				File:     "internal/review/openai.go",
				Line:     42,
				Severity: SeveritySuggestion,
				Body:     "Handle empty choices explicitly.",
			}},
			Summary: "looks good",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("result mismatch\n got: %#v\nwant: %#v", got, want)
		}
	})

	t.Run("json fenced block", func(t *testing.T) {
		raw := "Here is the review:\n```json\n{\"verdict\":\"request-changes\",\"findings\":[{\"file\":\"internal/review/claude.go\",\"line\":17,\"severity\":\"critical\",\"body\":\"Return explicit errors.\"}]}\n```"

		got, err := parseReviewResult(raw)
		if err != nil {
			t.Fatalf("parseReviewResult: %v", err)
		}
		if got.Verdict != VerdictRequestChanges {
			t.Fatalf("verdict = %q, want %q", got.Verdict, VerdictRequestChanges)
		}
		if len(got.Findings) != 1 || got.Findings[0].Severity != SeverityCritical {
			t.Fatalf("findings = %#v, want one critical finding", got.Findings)
		}
		if !strings.Contains(got.Summary, "request-changes") {
			t.Fatalf("summary = %q, want default request-changes summary", got.Summary)
		}
	})
}

func TestParseReviewResultRejectsInvalidVerdict(t *testing.T) {
	_, err := parseReviewResult(`{"verdict":"maybe","findings":[]}`)
	if err == nil {
		t.Fatal("expected invalid verdict error")
	}
}

func TestNewReviewer(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantType string
	}{
		{name: "claude", provider: ProviderClaude, wantType: "*review.ClaudeReviewer"},
		{name: "openai", provider: ProviderOpenAI, wantType: "*review.OpenAIReviewer"},
		{name: "external", provider: ProviderExternal, wantType: "*review.ExternalReviewer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reviewer, err := NewReviewer(tc.provider, ProviderConfig{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatalf("NewReviewer: %v", err)
			}
			if got := reflect.TypeOf(reviewer).String(); got != tc.wantType {
				t.Fatalf("reviewer type = %s, want %s", got, tc.wantType)
			}
		})
	}
}

func TestNewReviewerRejectsUnknownProvider(t *testing.T) {
	_, err := NewReviewer("bogus", ProviderConfig{})
	if err == nil {
		t.Fatal("expected unknown provider error")
	}
}
