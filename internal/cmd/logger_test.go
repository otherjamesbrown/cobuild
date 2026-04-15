package cmd

import (
	"log/slog"
	"testing"
)

func TestResolveLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelWarn},
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"notalevel", slog.LevelWarn},
		{"  info  ", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := resolveLogLevel(tc.in); got != tc.want {
			t.Errorf("resolveLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestInternalLoggerRespectsCOBUILDLogLevel(t *testing.T) {
	// Sanity check: resolveLogLevel is the single entry point for
	// COBUILD_LOG_LEVEL, so a regression here would silently leave the
	// env var non-functional. Doesn't exercise internalLogger() directly
	// because that's a one-shot init; resolveLogLevel carries the contract.
	for env, want := range map[string]string{
		"":      "WARN",
		"warn":  "WARN",
		"info":  "INFO",
		"debug": "DEBUG",
		"error": "ERROR",
	} {
		if got := resolveLogLevel(env).String(); got != want {
			t.Errorf("resolveLogLevel(%q) = %s, want %s", env, got, want)
		}
	}
}

func TestSetLogLevelForTestRestores(t *testing.T) {
	// Baseline the level explicitly so the test doesn't depend on what other
	// tests happened to leave in the shared logLevel var.
	baseline := setLogLevelForTest(slog.LevelWarn)
	t.Cleanup(baseline)

	restore := setLogLevelForTest(slog.LevelDebug)
	if logLevel.Level() != slog.LevelDebug {
		t.Fatalf("level after override = %v, want debug", logLevel.Level())
	}
	restore()
	if logLevel.Level() != slog.LevelWarn {
		t.Fatalf("level after restore = %v, want warn (baseline)", logLevel.Level())
	}
}
