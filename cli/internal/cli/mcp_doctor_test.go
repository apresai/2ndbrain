package cli

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// buildMCPDoctorReport must pass on a healthy vault even with NO AI provider
// (the engine checks run on SQLite/FTS5; ai readiness + configured are warn-only),
// so the doctor is a usable offline gate.
func TestBuildMCPDoctorReport_HealthyOffline(t *testing.T) {
	v, _ := newContractVault(t)
	testutil.CreateAndIndex(t, v, "Alpha", "note", "alpha body about tokens")
	initAIProviders(v)

	report := buildMCPDoctorReport(context.Background(), v)

	if report.ToolCount != 22 {
		t.Errorf("tool_count = %d, want 22", report.ToolCount)
	}
	if !report.InstructionsPresent {
		t.Error("instructions should be present")
	}
	if len(report.ToolsExercised) != 3 {
		t.Errorf("expected 3 tools exercised, got %v", report.ToolsExercised)
	}
	// The four engine checks must be OK (hard); ai/configured/instructions/wal/
	// servers are warn-only, so the roll-up stays true offline + not-configured.
	engine := map[string]bool{
		"mcp tools registered":   false,
		"kb_info round-trip":     false,
		"kb_list round-trip":     false,
		"kb_search round-trip":   false,
	}
	for _, c := range report.Checks {
		if _, ok := engine[c.Name]; ok {
			engine[c.Name] = c.OK
		}
	}
	for name, ok := range engine {
		if !ok {
			t.Errorf("engine check %q should be OK", name)
		}
	}
	if !report.OK {
		t.Errorf("report.OK should be true offline (engine answers, ai/configured are warnings)")
	}
}
