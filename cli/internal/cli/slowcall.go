package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

// A provider call that takes tens of seconds is indistinguishable from a hung
// one when nothing says otherwise. Measured on a real vault: a search whose
// query embedding took 10.8s, single-note reindexes at 10 to 14s, a
// two-document embed pass at 53.6s, against 0.7s for the same embedding call on
// an unthrottled account. All of it silent.
//
// slowCallNotice breaks that silence without adding noise to fast calls: past
// slowCallThreshold it writes ONE self-erasing line to stderr, counting up, and
// naming the retry cause once a retry has actually happened, since "still
// waiting" and "being throttled" call for different responses from the user.

const (
	// slowCallThreshold sits above every warm call measured and well under the
	// throttled waits this exists to explain.
	slowCallThreshold = 2 * time.Second
	// slowCallTick refreshes the elapsed seconds so the line counts up rather
	// than freezing at the threshold and looking stuck itself.
	slowCallTick = time.Second
)

// slowCallNotice attaches a retry counter to ctx and, when stderr deserves it,
// starts the notice. The returned function stops the notice and erases the
// line; call it as soon as the provider call returns.
//
// The counter is attached even when the notice is suppressed: --porcelain and
// non-TTY callers still record retries into the metrics observatory.
func slowCallNotice(ctx context.Context, what string) (context.Context, func()) {
	counter := &ai.RetryCounter{}
	ctx = ai.WithRetryCounter(ctx, counter)
	if !slowCallNoticeEnabled() {
		return ctx, func() {}
	}
	s := newSlowCall(what, os.Stderr, slowCallThreshold, slowCallTick, counter)
	return ctx, s.stop
}

// slowCallNoticeEnabled is noteWrite's rule: a status line belongs on an
// interactive terminal and never in a machine-readable stream.
func slowCallNoticeEnabled() bool { return !flagPorcelain && stderrIsTTY() }

// slowCall is the notice's testable core: everything except deciding whether
// stderr deserves one.
type slowCall struct {
	what      string
	out       io.Writer
	threshold time.Duration
	tick      time.Duration
	counter   *ai.RetryCounter
	start     time.Time
	quit      chan struct{}
	done      chan struct{}
}

func newSlowCall(what string, out io.Writer, threshold, tick time.Duration, counter *ai.RetryCounter) *slowCall {
	s := &slowCall{
		what:      what,
		out:       out,
		threshold: threshold,
		tick:      tick,
		counter:   counter,
		start:     time.Now(),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *slowCall) run() {
	defer close(s.done)
	printed := false
	timer := time.NewTimer(s.threshold)
	defer timer.Stop()
	for {
		select {
		case <-s.quit:
			if printed {
				// Erase the line so a completed call leaves no trace: the
				// notice is about the wait, not about the result.
				fmt.Fprint(s.out, "\r\033[K")
			}
			return
		case <-timer.C:
			fmt.Fprintf(s.out, "\r2nb: %s\033[K",
				slowCallLine(s.what, time.Since(s.start), s.counter.Count(), maxProviderRetries, s.counter.Cause()))
			printed = true
			timer.Reset(s.tick)
		}
	}
}

func (s *slowCall) stop() {
	close(s.quit)
	<-s.done
}

// maxProviderRetries is the attempt budget the line quotes. It matches the
// classic Bedrock loop, which is the path that actually produces multi-second
// waits; quoting a number is what makes "retry 2 of 5" readable as progress
// rather than as an error.
const maxProviderRetries = 5

// slowCallLine renders the one-line status. Without a retry there is nothing to
// explain beyond the wait itself, so the line stays short.
func slowCallLine(what string, elapsed time.Duration, retries, max int, cause string) string {
	secs := int(elapsed.Round(time.Second) / time.Second)
	if retries > 0 && cause != "" {
		return fmt.Sprintf("%s, %ds, %s, retry %d of %d", what, secs, cause, retries, max)
	}
	return fmt.Sprintf("%s, %ds", what, secs)
}
