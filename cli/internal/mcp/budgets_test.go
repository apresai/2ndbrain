package mcp

import (
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestToolBudgetsNested machine-checks the per-tool timeout nesting: every
// outer budget strictly contains the inner budget it wraps plus slack, so the
// innermost bound fires first and a timeout error names the real subsystem.
// The two historical inversions this pins against: tSearch EQUAL to the embed
// budget it wrapped (both fired together, misattributing a stuck embed to the
// search tool), and tGenerate (120s) sitting far inside the mantle plane's
// retry budget (killing working cold-start reasoning models mid-answer).
func TestToolBudgetsNested(t *testing.T) {
	// tSearch wraps one query embed (mcpEmbedBudget) plus BM25 + KNN work.
	if tSearch < mcpEmbedBudget+10*time.Second {
		t.Errorf("tSearch = %v must exceed the embed budget it wraps (%v) by real slack; an outer bound equal to its inner bound fires with it and misattributes the hang", tSearch, mcpEmbedBudget)
	}
	// tGenerate wraps one retrieval embed plus a generation call whose worst
	// case is the mantle plane's full retry budget.
	if tGenerate < ai.MantleWorstCase+mcpEmbedBudget {
		t.Errorf("tGenerate = %v does not contain the mantle worst case %v + the retrieval embed budget %v; a working-but-slow reasoning model would be killed by the tool deadline", tGenerate, ai.MantleWorstCase, mcpEmbedBudget)
	}
	// The doctor budget contains every engine tool it exercises sequentially
	// (kb_info, kb_list, kb_search) at that tool's full per-tool timeout.
	if DoctorExercisedBudget() < tCheap+tCheap+tSearch {
		t.Errorf("DoctorExercisedBudget() = %v does not contain the exercised tools' own budgets (%v + %v + %v); an engine tool could time out on the doctor's clock and be misreported as an engine failure", DoctorExercisedBudget(), tCheap, tCheap, tSearch)
	}
}
