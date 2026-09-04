package vault

import (
	"testing"
)

// ObsidianHasVaultOpen gates a WRITE into Obsidian's own config directory, so
// its second return ("was this knowable at all") carries as much weight as the
// first. An unreadable registry must never read as permission to write.
func TestObsidianHasVaultOpen(t *testing.T) {
	for _, tc := range []struct {
		name       string
		registry   string
		root       string
		wantOpen   bool
		wantKnown  bool
		wantReason string
	}{
		{
			name:      "the vault is the open one",
			registry:  `{"vaults":{"a":{"path":"/Users/x/obsidian","ts":100,"open":true}}}`,
			root:      "/Users/x/obsidian",
			wantOpen:  true,
			wantKnown: true,
		},
		{
			name:      "a DIFFERENT vault is open",
			registry:  `{"vaults":{"a":{"path":"/Users/x/other","ts":100,"open":true},"b":{"path":"/Users/x/obsidian","ts":90}}}`,
			root:      "/Users/x/obsidian",
			wantOpen:  false,
			wantKnown: true,
		},
		{
			// The distinction ObsidianActiveVault deliberately blurs and this
			// must not: a vault Obsidian opened last week is not one Obsidian
			// is holding now, and refusing a write on that basis would refuse
			// nearly every write.
			name:      "known but nothing flagged open",
			registry:  `{"vaults":{"a":{"path":"/Users/x/obsidian","ts":100}}}`,
			root:      "/Users/x/obsidian",
			wantOpen:  false,
			wantKnown: true,
		},
		{
			name:      "unparseable registry is UNKNOWN, not closed",
			registry:  `{not json`,
			root:      "/Users/x/obsidian",
			wantOpen:  false,
			wantKnown: false,
		},
		{
			name:      "empty registry is UNKNOWN, not closed",
			registry:  `{"vaults":{}}`,
			root:      "/Users/x/obsidian",
			wantOpen:  false,
			wantKnown: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			writeObsidianRegistry(t, home, tc.registry)
			open, known := ObsidianHasVaultOpen(tc.root)
			if open != tc.wantOpen || known != tc.wantKnown {
				t.Errorf("ObsidianHasVaultOpen(%q) = (%v, %v), want (%v, %v)", tc.root, open, known, tc.wantOpen, tc.wantKnown)
			}
		})
	}
}

// With no registry at all the answer is UNKNOWN. A caller that read this as
// "closed" would write under a running Obsidian on any machine whose registry
// it could not find.
func TestObsidianHasVaultOpen_NoRegistryIsUnknown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	if open, known := ObsidianHasVaultOpen("/Users/x/obsidian"); open || known {
		t.Errorf("ObsidianHasVaultOpen with no registry = (%v, %v), want (false, false)", open, known)
	}
}
