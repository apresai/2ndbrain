package ai

import (
	"sync"
	"testing"
)

// TestRetryCounterSnapshotIsSelfConsistentUnderConcurrency: the counter's three
// fields used to be three independent atomics, so a reader could observe a cause
// from one retry paired with the attempt budget of another. That matters because
// the two planes disagree about the budget (the classic loops allow five
// attempts, the mantle ones three), and the slow-call notice renders both in one
// sentence: an impossible pairing tells the user they have retries left that
// they do not.
//
// The pairing is the invariant, so the test asserts on it rather than on a race
// detector firing: with two goroutines recording distinct (cause, max) pairs, a
// snapshot must always carry a pair one of them actually recorded.
func TestRetryCounterSnapshotIsSelfConsistentUnderConcurrency(t *testing.T) {
	const iterations = 20000
	c := &RetryCounter{}
	pairs := map[string]int{
		"classic": 5,
		"mantle":  3,
	}

	var wg sync.WaitGroup
	for cause, max := range pairs {
		wg.Add(1)
		go func(cause string, max int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				c.Record(cause, max)
			}
		}(cause, max)
	}

	inconsistent := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < iterations*2; i++ {
			s := c.Snapshot()
			if s.Cause == "" {
				continue // nothing recorded yet
			}
			if want, ok := pairs[s.Cause]; !ok || want != s.MaxAttempts {
				inconsistent++
			}
		}
	}()
	wg.Wait()
	<-done

	if inconsistent != 0 {
		t.Errorf("%d snapshots paired a cause with another retry's attempt budget", inconsistent)
	}
	// No record may be lost either: the count is a read-modify-write.
	if got := c.Count(); got != iterations*len(pairs) {
		t.Errorf("Count() = %d, want %d: a concurrent record was dropped", got, iterations*len(pairs))
	}
}
