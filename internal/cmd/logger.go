package cmd

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// stderrProxy forwards writes to the current os.Stderr at the time of write,
// so tests that swap os.Stderr (captureStdoutAndStderr) see slog output.
// Without this, slog captures the os.Stderr fd at init time and tests never
// see the log lines.
type stderrProxy struct{}

func (stderrProxy) Write(p []byte) (int, error) {
	return os.Stderr.Write(p)
}

// Ensure stderrProxy satisfies io.Writer at compile time.
var _ io.Writer = stderrProxy{}

// cb-e7edc9: structured logging for non-user-facing internal paths.
//
// Default level is warn, so `go test` and interactive CLI runs stay quiet.
// Raise via COBUILD_LOG_LEVEL=debug|info|warn|error for deeper visibility
// (e.g. when chasing a poller bug). User-facing output (status tables,
// gate verdicts, "Next step:" lines, dispatch progress messages) still
// goes through fmt.* — slog is for the background noise a human reader
// never wants to see unless they explicitly asked for it.

var (
	loggerOnce sync.Once
	logger     *slog.Logger
	logLevel   = new(slog.LevelVar)
)

// internalLogger returns the shared slog handler used by poller and other
// non-user-facing paths. Safe to call from any goroutine; initialised once.
func internalLogger() *slog.Logger {
	loggerOnce.Do(func() {
		logLevel.Set(resolveLogLevel(os.Getenv("COBUILD_LOG_LEVEL")))
		handler := slog.NewTextHandler(stderrProxy{}, &slog.HandlerOptions{
			Level: logLevel,
		})
		logger = slog.New(handler)
	})
	return logger
}

// setLogLevelForTest overrides the log level in tests. Returns a restore
// function. Kept in the same file so it's obvious the test hook and the
// production setup share state.
func setLogLevelForTest(level slog.Level) func() {
	internalLogger() // ensure initialised
	prev := logLevel.Level()
	logLevel.Set(level)
	return func() { logLevel.Set(prev) }
}

func resolveLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning", "":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelWarn
}
