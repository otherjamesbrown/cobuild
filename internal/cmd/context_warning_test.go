package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintContextSizeWarning_WarnTier(t *testing.T) {
	var buf bytes.Buffer
	printContextSizeWarning(&buf, 50*1024, t.TempDir())
	out := buf.String()

	if !strings.Contains(out, "Context size 50 KB") {
		t.Fatalf("should show size in KB, got:\n%s", out)
	}
	if !strings.Contains(out, "warn threshold (30 KB)") {
		t.Fatalf("should mention warn threshold, got:\n%s", out)
	}
	if strings.Contains(out, "HIGH") {
		t.Fatalf("50 KB should not trigger HIGH tier, got:\n%s", out)
	}
	if !strings.Contains(out, "cobuild context audit") {
		t.Fatalf("should point to cobuild context audit, got:\n%s", out)
	}
}

func TestPrintContextSizeWarning_HighTier(t *testing.T) {
	var buf bytes.Buffer
	printContextSizeWarning(&buf, 180*1024, t.TempDir())
	out := buf.String()

	if !strings.Contains(out, "HIGH context size: 180 KB") {
		t.Fatalf("should show HIGH framing, got:\n%s", out)
	}
	if !strings.Contains(out, "high 100 KB") {
		t.Fatalf("should mention high threshold, got:\n%s", out)
	}
	// Should name concrete failure modes
	for _, mode := range []string{"pushing to main", "skipping tests", "stub PR bodies"} {
		if !strings.Contains(out, mode) {
			t.Fatalf("HIGH tier should name failure mode %q, got:\n%s", mode, out)
		}
	}
}
