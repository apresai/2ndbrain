package vault

import (
	"path/filepath"
	"testing"
)

// An UNSUPPORTED platform must get no path at all, never the nearest layout.
//
// This is the regression that shipped: the switch ended in
// `default: // linux and other unixes`, so removing the Windows case folded
// Windows into the unix branch. That returned a syntactically fine
// ~/.config/obsidian/obsidian.json which cannot exist there, obsidianProcessAlive
// read the resulting ENOENT as a CONFIRMED "not running", and register-types
// wrote under a live Obsidian with no --force. A guess is how a liveness check
// becomes a fail-open, and the guess was invisible to tests because runtime.GOOS
// cannot be varied at runtime.
func TestObsidianRegistryPathFor_UnsupportedPlatformGetsNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	for _, goos := range []string{"windows", "freebsd", "openbsd", "plan9", "js", ""} {
		if got := obsidianRegistryPathFor(goos); got != "" {
			t.Errorf("obsidianRegistryPathFor(%q) = %q, want \"\"; a platform 2nb does not build for must not be handed a guessed layout", goos, got)
		}
	}

	// And the supported ones still resolve, or the assertion above would pass
	// for the uninteresting reason that everything returns "".
	for _, goos := range []string{"darwin", "linux"} {
		if got := obsidianRegistryPathFor(goos); got == "" {
			t.Errorf("obsidianRegistryPathFor(%q) = \"\"; the supported platforms must still resolve", goos)
		}
	}
}

// THE safety property of this guard, stated once: a write is permitted only when
// liveness was affirmatively established. Anything less than that must refuse.
//
// Every bug this guard has had was a violation of exactly this: first the
// registry `open` flag alone (which Obsidian never clears, so it over-refused),
// then the unsupported-platform fall-through (which under-refused). A table over
// the probe's outcomes states the property directly, so a future change to the
// state machine has to break an assertion rather than a comment.
func TestObsidianVaultOpenState_PermitsOnlyOnAConfirmedAnswer(t *testing.T) {
	const root = "/Users/x/obsidian"
	flaggedOpen := `{"vaults":{"a":{"path":"` + root + `","ts":100,"open":true}}}`

	for _, tc := range []struct {
		name         string
		alive, known bool
		want         ObsidianVaultState
	}{
		{"confirmed running", true, true, ObsidianOpen},
		{"confirmed not running", false, true, ObsidianClosed},
		{"liveness unknown", false, false, ObsidianOpenUnconfirmed},
		// alive=true with known=false is the EPERM case: the process exists but
		// belongs to another user, so it cannot be claimed as "Obsidian".
		{"alive but not ours", true, false, ObsidianOpenUnconfirmed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			fakeObsidianLiveness(t, tc.alive, tc.known)
			writeObsidianRegistry(t, home, flaggedOpen)

			got := ObsidianVaultOpenState(root)
			if got != tc.want {
				t.Errorf("state = %v, want %v", got, tc.want)
			}
			// The property, asserted independently of the table so it survives
			// someone "fixing" an expectation: ObsidianClosed is the only state
			// that permits the write, and it requires a KNOWN answer.
			if got == ObsidianClosed && !tc.known {
				t.Error("permitted a write (ObsidianClosed) while liveness was NOT established; this guard must fail closed")
			}
		})
	}
}

// The same property one layer down: when the lock cannot be located at all, the
// probe must report "cannot tell", never "not running". That is what an
// unsupported platform relies on to fail closed.
//
// The path is an argument because it has to be: obsidianSingletonLockPath()
// derives it from HOME and so ALWAYS resolves on darwin, the only platform this
// ships for. A test that called the real thing could only skip, which is a test
// that reports success while asserting nothing.
func TestObsidianProcessAliveAt_NoLockPathIsUnknownNotAbsent(t *testing.T) {
	alive, known := obsidianProcessAliveAt("")
	if alive || known {
		t.Errorf("obsidianProcessAliveAt(\"\") = (%v, %v), want (false, false): an unlocatable lock is UNKNOWN, and reading it as absent is what permits a write", alive, known)
	}

	// The contrast that keeps the assertion honest: a path that resolves but has
	// no file is a real "not running" answer, so the two must not collapse.
	absent := filepath.Join(t.TempDir(), "SingletonLock")
	if alive, known := obsidianProcessAliveAt(absent); alive || !known {
		t.Errorf("obsidianProcessAliveAt(absent) = (%v, %v), want (false, true): a lock that is genuinely gone means Obsidian quit", alive, known)
	}
}
