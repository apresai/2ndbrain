package procutil

import (
	"os"
	"testing"
)

// The probe answers about a pid, and the two answers that matter are the ones
// callers act on: this process is alive, and a pid nothing owns is not.
func TestAlive(t *testing.T) {
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
