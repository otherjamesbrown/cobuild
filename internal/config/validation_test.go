package config

import (
	"strings"
	"testing"
)

// TestValidate_CatchesTypoInWorkflowPhase exercises cb-663873 slice 3: a
// workflow referencing an undeclared phase surfaces as a clear error at
// LoadConfig time instead of dispatch time. The unit test here calls the
// validator directly; LoadConfig wiring is covered implicitly by every
// other LoadConfig test.
func TestValidate_CatchesTypoInWorkflowPhase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Workflows = map[string]WorkflowConfig{
		"bug": {Phases: []string{"fix", "rview", "done"}}, // typo: "rview"
	}
	err := cfg.validateReferentialIntegrity()
	if err == nil {
		t.Fatalf("validateReferentialIntegrity() = nil, want error for typo 'rview'")
	}
	if !strings.Contains(err.Error(), "rview") {
		t.Errorf("error = %q, want mention of the typo", err.Error())
	}
	if !strings.Contains(err.Error(), "bug") {
		t.Errorf("error = %q, want mention of workflow name", err.Error())
	}
}

func TestValidate_AcceptsBuiltinPhases(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Workflows = map[string]WorkflowConfig{
		"task": {Phases: []string{"implement", "review", "done"}},
		"bug":  {Phases: []string{"fix", "review", "done"}},
		"design": {Phases: []string{"design", "decompose", "implement", "review", "done"}},
	}
	if err := cfg.validateReferentialIntegrity(); err != nil {
		t.Fatalf("validateReferentialIntegrity() = %v, want nil for all-builtin phases", err)
	}
}

func TestValidate_AcceptsOperatorDeclaredPhase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Phases = map[string]PhaseConfig{
		"custom": {},
	}
	cfg.Workflows = map[string]WorkflowConfig{
		"bespoke": {Phases: []string{"custom", "done"}},
	}
	if err := cfg.validateReferentialIntegrity(); err != nil {
		t.Fatalf("validateReferentialIntegrity() = %v, want nil when phase is declared", err)
	}
}
