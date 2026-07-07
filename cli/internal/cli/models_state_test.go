package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestStateLabel(t *testing.T) {
	recent := time.Now().UTC().Add(-3 * 24 * time.Hour).Format(time.RFC3339)
	tests := []struct {
		name string
		m    ai.ModelInfo
		want string
	}{
		{"untested", ai.ModelInfo{}, "-"},
		{"recommended untested", ai.ModelInfo{Recommended: true}, "★ -"},
		{"passed", ai.ModelInfo{TestedAt: recent}, "ok 3d"},
		{"recommended passed", ai.ModelInfo{Recommended: true, TestedAt: recent}, "★ ok 3d"},
		{"classified failure", ai.ModelInfo{TestedAt: recent, TestError: "403", TestErrorCode: "access_denied"}, "access_denied"},
		{"unclassified failure", ai.ModelInfo{TestedAt: recent, TestError: "boom"}, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stateLabel(tt.m); got != tt.want {
				t.Errorf("stateLabel(%+v) = %q, want %q", tt.m, got, tt.want)
			}
		})
	}
}

func TestTestAge(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		testedAt string
		want     string
	}{
		{"minutes ago", now.Add(-10 * time.Minute).Format(time.RFC3339), "now"},
		{"hours ago", now.Add(-5 * time.Hour).Format(time.RFC3339), "5h"},
		{"days ago", now.Add(-72 * time.Hour).Format(time.RFC3339), "3d"},
		{"months ago", now.Add(-65 * 24 * time.Hour).Format(time.RFC3339), "2mo"},
		{"garbage", "not-a-timestamp", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testAge(tt.testedAt); got != tt.want {
				t.Errorf("testAge(%q) = %q, want %q", tt.testedAt, got, tt.want)
			}
		})
	}
	// Guard the corrupt-timestamp rendering path end to end: a garbage
	// TestedAt must not leave a trailing-space "ok " label.
	label := stateLabel(ai.ModelInfo{TestedAt: "garbage"})
	if strings.HasSuffix(label, " ") {
		t.Errorf("stateLabel with corrupt TestedAt has trailing space: %q", label)
	}
}
