package cli

import (
	"strings"
	"testing"
)

// TestRefreshRequiresDiscover: --refresh only means anything to the discovery
// walk, so asking for it without --discover is a flag mistake, not a silent
// no-op. Both commands that serve the cached pool say so the same way.
func TestRefreshRequiresDiscover(t *testing.T) {
	_, root := newContractVault(t)

	for _, argv := range [][]string{
		{"models", "list", "--refresh"},
		{"models", "cost-preview", "--refresh", "--all"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			_, err := runCLIArgs(t, root, argv...)
			if err == nil {
				t.Fatal("--refresh without --discover must be refused, not ignored")
			}
			if !strings.Contains(err.Error(), "--refresh requires --discover") {
				t.Errorf("error = %v, want it to name the missing flag", err)
			}
		})
	}
}
