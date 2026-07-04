package eval

import "testing"

func TestParseScore(t *testing.T) {
	cases := map[string]int{
		"4":            4,
		"5":            5,
		"1":            1,
		"Score: 3":     3,
		"4/5":          4,
		"I'd say 2.":   2,
		"10":           0, // out of range; must NOT read as 1
		"2024":         0, // a year is not a score
		"no number":    0,
		"":             0,
		"eight":        0,
	}
	for in, want := range cases {
		if got := parseScore(in); got != want {
			t.Errorf("parseScore(%q) = %d, want %d", in, got, want)
		}
	}
}
