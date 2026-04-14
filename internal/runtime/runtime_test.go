package runtime

import (
	"context"
	"strings"
	"testing"
)

// fakeRuntime is a minimal Runtime implementation used by Register tests.
// It intentionally satisfies just enough of the interface to exercise the
// registry — the real runtime implementations live under runtime/{claudecode,
// codex, stub}. Unused methods return zero values rather than panic per
// cb-6d598a (stubs in test code should never panic — they might leak).
type fakeRuntime struct {
	name string
}

func (f *fakeRuntime) Name() string                                       { return f.name }
func (f *fakeRuntime) ContextFile() string                                { return "" }
func (f *fakeRuntime) PreDispatch(context.Context, string) error          { return nil }
func (f *fakeRuntime) WriteSettings(string) error                         { return nil }
func (f *fakeRuntime) BuildRunnerScript(RunnerInput) (string, error)      { return "", nil }
func (f *fakeRuntime) ParseSessionStats(string) (SessionStats, error)    { return SessionStats{}, nil }

// resetRegistryForTest clears the global registry — only safe in tests,
// and only when tests serialise access. t.Helper ensures the caller is a
// test; do not expose outside the test binary.
func resetRegistryForTest(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Runtime{}
}

func TestRegisterRejectsNilRuntime(t *testing.T) {
	resetRegistryForTest(t)
	err := Register(nil)
	if err == nil {
		t.Fatal("Register(nil) = nil, want error")
	}
	if !strings.Contains(err.Error(), "nil Runtime") {
		t.Errorf("error = %q, want mention of nil Runtime", err.Error())
	}
}

func TestRegisterRejectsEmptyName(t *testing.T) {
	resetRegistryForTest(t)
	err := Register(&fakeRuntime{name: ""})
	if err == nil {
		t.Fatal("Register(empty-name) = nil, want error")
	}
	if !strings.Contains(err.Error(), "empty Name") {
		t.Errorf("error = %q, want mention of empty Name", err.Error())
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	resetRegistryForTest(t)
	if err := Register(&fakeRuntime{name: "dup"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := Register(&fakeRuntime{name: "dup"})
	if err == nil {
		t.Fatal("second Register(dup) = nil, want error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want mention of duplicate", err.Error())
	}
}

func TestRegisterHappyPath(t *testing.T) {
	resetRegistryForTest(t)
	if err := Register(&fakeRuntime{name: "happy"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := Get("happy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "happy" {
		t.Errorf("Get().Name() = %q, want happy", got.Name())
	}
}

func TestMustRegisterPanicsOnError(t *testing.T) {
	resetRegistryForTest(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustRegister(nil) did not panic")
		}
	}()
	MustRegister(nil)
}

func TestMustRegisterHappyPath(t *testing.T) {
	resetRegistryForTest(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MustRegister panicked on valid runtime: %v", r)
		}
	}()
	MustRegister(&fakeRuntime{name: "clean"})
}
