package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/skills"
)

func TestSkillFreshnessDetail(t *testing.T) {
	mk := func(f skills.Freshness) skills.Verification { return skills.Verification{Freshness: f} }
	cases := []struct {
		name string
		v    skills.Verification
		want string
	}{
		{"up to date", mk(skills.Freshness{Stamped: true, UpToDate: true, InstalledVersion: "1.2.3"}), "up to date"},
		{"hand-edited", mk(skills.Freshness{Stamped: true, Modified: true}), "hand-edited"},
		{"stale", mk(skills.Freshness{Stamped: true}), "out of date"},
		{"unstamped", mk(skills.Freshness{}), "no version stamp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := skillFreshnessDetail(c.v); !strings.Contains(got, c.want) {
				t.Errorf("skillFreshnessDetail = %q, want substring %q", got, c.want)
			}
		})
	}
}
