package document

import "testing"

func TestFenceTracker_LongerOpenerNotClosedByTriple(t *testing.T) {
	var f fenceTracker
	if !f.Feed("````") {
		t.Fatal("quadruple backtick should open")
	}
	if !f.Inside() {
		t.Fatal("should be inside after opening ````")
	}
	if f.Feed("```") {
		t.Fatal("``` must not close a ```` opener")
	}
	if !f.Inside() {
		t.Fatal("should still be inside after a too-short closer")
	}
	if !f.Feed("````") {
		t.Fatal("matching ```` should close")
	}
	if f.Inside() {
		t.Fatal("should be outside after a matching closer")
	}
}

func TestFenceTracker_TildeNotClosedByBacktick(t *testing.T) {
	var f fenceTracker
	if !f.Feed("~~~") {
		t.Fatal("~~~ should open")
	}
	if f.Feed("```") {
		t.Fatal("``` must not close a ~~~ fence")
	}
	if !f.Inside() {
		t.Fatal("should still be inside a tilde fence")
	}
	if !f.Feed("~~~") {
		t.Fatal("~~~ should close")
	}
	if f.Inside() {
		t.Fatal("should be outside after ~~~ close")
	}
}

func TestFenceTracker_CloserWithInfoDoesNotClose(t *testing.T) {
	var f fenceTracker
	if !f.Feed("```") {
		t.Fatal("``` should open")
	}
	if f.Feed("``` not-a-close") {
		t.Fatal("a closer with an info string must not close")
	}
	if !f.Inside() {
		t.Fatal("should still be inside after a rejected closer")
	}
	if !f.Feed("```") {
		t.Fatal("bare ``` should close")
	}
	if f.Inside() {
		t.Fatal("should be outside after a bare closer")
	}
}

func TestFenceTracker_InfoStringOpens(t *testing.T) {
	var f fenceTracker
	if !f.Feed("```bash") {
		t.Fatal("```bash should open")
	}
	if !f.Inside() {
		t.Fatal("info string on an opener is ignored, not rejected")
	}
	if !f.Feed("```") {
		t.Fatal("bare ``` should close an info-string opener")
	}
}

func TestFenceTracker_UnclosedSkipsLaterHeading(t *testing.T) {
	var f fenceTracker
	if headingLevelOutsideFence("```", &f) != 0 {
		t.Fatal("opening fence is not a heading")
	}
	if headingLevelOutsideFence("# Heading", &f) != 0 {
		t.Fatal("unclosed fence: later # Heading must not be a heading")
	}
	if !f.Inside() {
		t.Fatal("unclosed fence stays open through EOF")
	}
}

func TestHeadingLevelOutsideFence_AfterClose(t *testing.T) {
	var f fenceTracker
	if headingLevelOutsideFence("```", &f) != 0 {
		t.Fatal("opener")
	}
	if headingLevelOutsideFence("# comment", &f) != 0 {
		t.Fatal("in-fence ATX lookalike")
	}
	if headingLevelOutsideFence("```", &f) != 0 {
		t.Fatal("closer")
	}
	if got := headingLevelOutsideFence("# Real", &f); got != 1 {
		t.Fatalf("heading after close = %d, want 1", got)
	}
}
