package ai

import (
	"sync"
	"testing"
	"time"
)

func TestAvailableCache_ZeroValueMisses(t *testing.T) {
	var c availableCache
	v, hit := c.get()
	if hit {
		t.Errorf("fresh cache should report miss, got hit=true value=%v", v)
	}
	if v {
		t.Errorf("fresh cache should return false, got %v", v)
	}
}

func TestAvailableCache_HitWithinTTL(t *testing.T) {
	var c availableCache
	c.set(true)
	v, hit := c.get()
	if !hit {
		t.Fatal("immediate read after set should hit")
	}
	if !v {
		t.Errorf("value = %v, want true", v)
	}

	// Also confirm a false value is cached (not just positive results).
	c.set(false)
	v, hit = c.get()
	if !hit {
		t.Fatal("immediate read after set(false) should hit")
	}
	if v {
		t.Errorf("value = %v, want false", v)
	}
}

func TestAvailableCache_ExpiresAfterTTL(t *testing.T) {
	// The package-level TTL is 30s — too long for a unit test to wait on.
	// Rewind the expiry manually to simulate TTL elapsing.
	var c availableCache
	c.set(true)
	c.mu.Lock()
	c.until = time.Now().Add(-time.Second)
	c.mu.Unlock()

	_, hit := c.get()
	if hit {
		t.Error("expired cache should report miss")
	}
}

func TestAvailableCache_ConcurrentGetSet(t *testing.T) {
	// Verifies the mutex under `-race`. 20 workers × 50 iters is enough to
	// surface any race on the first conflicting pair — more iterations add
	// no coverage but leave goroutines lingering past t.Fatal if the
	// watchdog fires.
	var c availableCache
	var wg sync.WaitGroup
	const workers = 20
	const iters = 50
	wg.Add(workers * 2)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				c.set(j%2 == 0)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_, _ = c.get()
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent get/set deadlocked or hung")
	}
}
