package eval

import "testing"

func TestParseScore(t *testing.T) {
	cases := map[string]int{
		"4":          4,
		"5":          5,
		"1":          1,
		"Score: 3":   3,
		"4/5":        4,
		"I'd say 2.": 2,
		"10":         0, // out of range; must NOT read as 1
		"2024":       0, // a year is not a score
		"no number":  0,
		"":           0,
		"eight":      0,
	}
	for in, want := range cases {
		if got := parseScore(in); got != want {
			t.Errorf("parseScore(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseAxes(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wc, wp, wg int
	}{
		{"clean three lines", "CORRECTNESS: 5\nCOMPLETENESS: 4\nGROUNDING: 5", 5, 4, 5},
		{"lowercase + equals", "correctness=3\ncompleteness = 2\ngrounding: 4", 3, 2, 4},
		{"reordered with prose", "GROUNDING: 5. COMPLETENESS: 3, CORRECTNESS: 4", 4, 3, 5},
		{"missing one axis -> that axis 0", "CORRECTNESS: 4\nGROUNDING: 5", 4, 0, 5},
		{"garbage -> all 0", "I think it's pretty good overall", 0, 0, 0},
		{"out-of-range digit ignored", "CORRECTNESS: 9\nCOMPLETENESS: 4\nGROUNDING: 5", 0, 4, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gc, gp, gg := parseAxes(c.in)
			if gc != c.wc || gp != c.wp || gg != c.wg {
				t.Errorf("parseAxes(%q) = (%d,%d,%d), want (%d,%d,%d)", c.in, gc, gp, gg, c.wc, c.wp, c.wg)
			}
		})
	}
}

func TestJudgmentOK(t *testing.T) {
	if !(Judgment{Correctness: 5, Completeness: 3, Grounding: 4}).ok() {
		t.Error("full verdict should be ok")
	}
	if (Judgment{Correctness: 5, Grounding: 4}).ok() { // completeness 0
		t.Error("a partial verdict (missing axis) must NOT be ok")
	}
}
