package cli

import (
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
)

// TestDoctorBudgetsNested machine-checks the doctor tier budgets against the
// budgets that run inside them: each tier's clock must contain its inner
// layer's worst case plus slack, so no inner budget is dead code and an
// expired tier can never be misreported as a subsystem failure. This pins the
// fix for two inversions: the 70s model tier wrapping probes whose own
// strategy-aware deadlines can legitimately run far longer, and the 20s vault
// tier wrapping mcp doctor's engine-tool budgets.
func TestDoctorBudgetsNested(t *testing.T) {
	// Tier 1 runs two sequential model probes (embedding, then generation),
	// each entitled to the full strategy-blind ceiling.
	if doctorModelTierTimeout < 2*ai.MaxProbeDeadline()+10*time.Second {
		t.Errorf("doctorModelTierTimeout = %v does not contain two sequential probes at ai.MaxProbeDeadline() = %v each plus slack; a degraded provider would starve the second probe and the tier would blame the model for the clock", doctorModelTierTimeout, ai.MaxProbeDeadline())
	}
	// Tier 2 folds in mcp doctor's engine checks, which need the exercised
	// tools' own per-tool budgets.
	if doctorVaultTierTimeout < mcppkg.DoctorExercisedBudget() {
		t.Errorf("doctorVaultTierTimeout = %v is below mcp.DoctorExercisedBudget() = %v; the engine checks would expire on the tier clock and report the index unusable when only the self-test ran out of time", doctorVaultTierTimeout, mcppkg.DoctorExercisedBudget())
	}
}
