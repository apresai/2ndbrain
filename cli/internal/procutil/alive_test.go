package procutil

import (
	"os"
	"runtime"
	"testing"
)

// The probe answers about a pid, and the two answers that matter are the ones
// callers act on: this process is alive, and a pid nothing owns is not.
func TestAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Alive is documented as always false there: os.Process.Signal refuses
		// every signal but Kill, so there is no signal-0 probe to make. Asserting
		// the running process reads as alive would fail for that reason alone and
		// say nothing about this code.
		t.Skip("no signal-0 probe on windows; see Alive's doc comment")
	}
	if !Alive(os.Getpid()) {
		t.Error("Alive(os.Getpid()) = false; the running test process must read as alive")
	}
	// Not a pid at all. A caller passing an unset or sentinel value must get
	// false rather than an accidental hit on pid 1 or the process group.
	for _, pid := range []int{0, -1, -99999} {
		if Alive(pid) {
			t.Errorf("Alive(%d) = true; a non-positive pid is never a live process", pid)
		}
	}
	// Above the system maximum, so nothing can own it. Chosen rather than a
	// recycled real pid, which another process could legitimately hold.
	if Alive(1 << 24) {
		t.Error("Alive(1<<24) = true; a pid beyond the system range cannot be live")
	}
}

// EPERM means the process EXISTS and we merely may not signal it, so it is an
// ALIVE answer. Reading it as dead fails OPEN on every liveness guard, which is
// the whole reason this package exists.
//
// pid 1 is the probe: on macOS and Linux it is launchd or init, owned by root,
// and always running. As a non-root user, signalling it gives EPERM rather than
// success, so a build that only checked for a nil error would call it dead.
func TestAlive_EPERMIsAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signal-0 probe on windows; see Alive's doc comment")
	}
	if os.Getuid() == 0 {
		// As root the signal succeeds outright, so the EPERM path is never taken
		// and the assertion would prove nothing.
		t.Skip("running as root: signalling pid 1 succeeds, so this cannot exercise EPERM")
	}
	if !Alive(1) {
		t.Error("Alive(1) = false; pid 1 is running, and EPERM must not be read as dead")
	}
}
