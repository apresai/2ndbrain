package vault

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeObsidianLiveness substitutes the process probe for one test. Every case
// below MUST set it: without substitution these read the developer's real
// machine, and the same test would pass or fail depending on whether Obsidian
// happened to be open. That is the whole reason the probe is a var.
func fakeObsidianLiveness(t *testing.T, alive, known bool) {
	t.Helper()
	prev := obsidianProcessAlive
	obsidianProcessAlive = func() (bool, bool) { return alive, known }
	t.Cleanup(func() { obsidianProcessAlive = prev })
}

// ObsidianHasVaultOpen gates a WRITE into Obsidian's own config directory, so
// its second return ("was this knowable at all") carries as much weight as the
// first. An unreadable registry must never read as permission to write.
//
// It answers from TWO facts, and needs both: the registry says which vault
// Obsidian opens, and only a live process says it is still there. The registry
// alone cannot, because Obsidian never clears `open` on quit.
func TestObsidianHasVaultOpen(t *testing.T) {
	for _, tc := range []struct {
		name        string
		registry    string
		root        string
		alive, live bool // what the process probe reports (alive, known)
		wantOpen    bool
		wantKnown   bool
	}{
		{
			name:      "flagged open AND Obsidian is running",
			registry:  `{"vaults":{"a":{"path":"/Users/x/obsidian","ts":100,"open":true}}}`,
			root:      "/Users/x/obsidian",
			alive:     true,
			live:      true,
			wantOpen:  true,
			wantKnown: true,
		},
		{
			// The bug this guard was rebuilt for. Obsidian sets `open` on open
			// and never clears it on quit, so the flag alone refused a user who
			// had just quit because the command told them to.
			name:      "flagged open but Obsidian has QUIT",
			registry:  `{"vaults":{"a":{"path":"/Users/x/obsidian","ts":100,"open":true}}}`,
			root:      "/Users/x/obsidian",
			alive:     false,
			live:      true,
			wantOpen:  false,
			wantKnown: true,
		},
		{
			// No liveness signal (Windows uses a named mutex, not the lock).
			// Absence of a signal is not permission: keep the old refusal.
			name:      "flagged open and liveness cannot be determined",
			registry:  `{"vaults":{"a":{"path":"/Users/x/obsidian","ts":100,"open":true}}}`,
			root:      "/Users/x/obsidian",
			alive:     false,
			live:      false,
			wantOpen:  true,
			wantKnown: true,
		},
		{
			// A running Obsidian holding a DIFFERENT vault must not block this
			// one: the registry decides which, the process only decides whether.
			name:      "Obsidian is running but this vault is not the open one",
			registry:  `{"vaults":{"a":{"path":"/Users/x/other","ts":100,"open":true},"b":{"path":"/Users/x/obsidian","ts":90}}}`,
			root:      "/Users/x/obsidian",
			alive:     true,
			live:      true,
			wantOpen:  false,
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
			fakeObsidianLiveness(t, tc.alive, tc.live)
			writeObsidianRegistry(t, home, tc.registry)
			open, known := ObsidianHasVaultOpen(tc.root)
			if open != tc.wantOpen || known != tc.wantKnown {
				t.Errorf("ObsidianHasVaultOpen(%q) = (%v, %v), want (%v, %v)", tc.root, open, known, tc.wantOpen, tc.wantKnown)
			}
		})
	}
}

// The probe itself, against a real lock file rather than a substitute. It reads
// what Chromium actually writes: a SYMLINK whose target is `<hostname>-<pid>`,
// where the hostname carries dashes of its own, so the pid is what follows the
// LAST one.
func TestObsidianProcessAlive_ReadsTheSingletonLock(t *testing.T) {
	if obsidianSingletonLockPath() == "" {
		t.Skip("no singleton-lock mechanism on this platform")
	}
	for _, tc := range []struct {
		name        string
		target      string // "" means write no lock at all
		wantAlive   bool
		wantKnown   bool
		useOwnPID   bool
		useStalePID bool
		foreignHost bool
	}{
		{
			name:      "no lock means Obsidian is not running",
			target:    "",
			wantAlive: false,
			wantKnown: true,
		},
		{
			// The real shape, hostname dashes and all.
			name:      "a lock naming a LIVE pid means running",
			useOwnPID: true,
			wantAlive: true,
			wantKnown: true,
		},
		{
			// A crash leaves the lock behind. A pid that is gone is a definite
			// "not running", not an unknown, or a crashed Obsidian would block
			// the write forever.
			name:        "a STALE lock naming a dead pid means not running",
			useStalePID: true,
			wantAlive:   false,
			wantKnown:   true,
		},
		{
			name:      "a target with no pid at all is unknown",
			target:    "nodashes",
			wantAlive: false,
			wantKnown: false,
		},
		{
			name:      "a non-numeric pid is unknown",
			target:    "host.local-notapid",
			wantAlive: false,
			wantKnown: false,
		},
		{
			// Chromium puts the hostname in the lock so a lock written by
			// ANOTHER machine sharing this home directory can be spotted. Its
			// pid means nothing here and could well belong to something live
			// and unrelated, so the honest answer is that we cannot tell.
			name:        "a lock from a DIFFERENT machine is unknown",
			foreignHost: true,
			wantAlive:   false,
			wantKnown:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			lock := obsidianSingletonLockPath()
			if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
				t.Fatal(err)
			}
			host, herr := os.Hostname()
			if herr != nil {
				t.Fatal(herr)
			}
			target := tc.target
			switch {
			case tc.useOwnPID:
				// This test process is the one pid guaranteed to be alive.
				target = host + "-" + strconv.Itoa(os.Getpid())
			case tc.useStalePID:
				// Beyond the system pid range, so nothing can own it, and
				// carrying THIS host so the case tests staleness rather than
				// accidentally tripping the foreign-host check.
				target = host + "-2147483000"
			case tc.foreignHost:
				target = "some-other-machine.local-" + strconv.Itoa(os.Getpid())
			}
			if target != "" {
				if err := os.Symlink(target, lock); err != nil {
					t.Fatal(err)
				}
			}
			alive, known := obsidianProcessAlive()
			if alive != tc.wantAlive || known != tc.wantKnown {
				t.Errorf("obsidianProcessAlive() = (%v, %v), want (%v, %v)", alive, known, tc.wantAlive, tc.wantKnown)
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
