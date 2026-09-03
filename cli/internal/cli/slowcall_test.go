package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestSlowCallStaysSilentBelowTheThreshold: a fast call must leave the terminal
// exactly as it found it. Every provider call goes through this, so a notice on
// a warm call would be noise on every search.
func TestSlowCallStaysSilentBelowTheThreshold(t *testing.T) {
	var buf bytes.Buffer
	s := newSlowCall("embedding query", &buf, 5*time.Second, time.Second, &ai.RetryCounter{})
	time.Sleep(20 * time.Millisecond)
	s.stop()
	if buf.Len() != 0 {
		t.Errorf("wrote %q before the threshold, want nothing", buf.String())
	}
}

// TestSlowCallSpeaksPastTheThresholdAndErasesItself: past the threshold the wait
// is named, and stopping erases the line so a completed call leaves no trace.
func TestSlowCallSpeaksPastTheThresholdAndErasesItself(t *testing.T) {
	var buf bytes.Buffer
	s := newSlowCall("embedding query", &buf, 10*time.Millisecond, 5*time.Millisecond, &ai.RetryCounter{})
	time.Sleep(60 * time.Millisecond)
	s.stop()

	out := buf.String()
	if !strings.Contains(out, "2nb: embedding query,") {
		t.Fatalf("output = %q, want it to name the call", out)
	}
	if !strings.HasSuffix(out, "\r\033[K") {
		t.Errorf("output = %q, want it to end by erasing the line", out)
	}
}

// TestSlowCallNamesTheRetryCause: "still waiting" and "being throttled" call for
// different responses from the user, so once a retry has actually happened the
// line says so. The counter is the same one the Bedrock retry loop records into.
func TestSlowCallNamesTheRetryCause(t *testing.T) {
	if got := slowCallLine("embedding query", 4*time.Second, 0, 5, ""); got != "embedding query, 4s" {
		t.Errorf("with no retry: %q, want %q", got, "embedding query, 4s")
	}
	got := slowCallLine("embedding query", 4*time.Second, 2, 5, "Bedrock throttled")
	want := "embedding query, 4s, Bedrock throttled, retry 2 of 5"
	if got != want {
		t.Errorf("with a retry: %q, want %q", got, want)
	}
}

// TestSlowCallNoticeSuppressedByPorcelain: --porcelain is a machine-readable
// stream, so it never carries a status line. Same rule noteWrite applies.
func TestSlowCallNoticeSuppressedByPorcelain(t *testing.T) {
	prev := flagPorcelain
	t.Cleanup(func() { flagPorcelain = prev })

	flagPorcelain = true
	if slowCallNoticeEnabled() {
		t.Error("the notice must be suppressed under --porcelain")
	}

	flagPorcelain = false
	// Under `go test` stderr is a pipe, never a character device, so this also
	// covers the non-TTY half of the rule: a redirected stderr gets no notice.
	if stderrIsTTY() {
		t.Skip("stderr is a terminal in this run; the non-TTY half cannot be asserted here")
	}
	if slowCallNoticeEnabled() {
		t.Error("the notice must be suppressed when stderr is not a terminal")
	}
}

// TestSlowCallNoticeAlwaysAttachesTheCounter: the notice is a display choice,
// the counter is not. A --porcelain or non-TTY run still has to record retries
// for the metrics observatory.
func TestSlowCallNoticeAlwaysAttachesTheCounter(t *testing.T) {
	prev := flagPorcelain
	t.Cleanup(func() { flagPorcelain = prev })
	flagPorcelain = true

	ctx, stop := slowCallNotice(t.Context(), "embedding query")
	defer stop()
	if ai.RetryCounterFrom(ctx) == nil {
		t.Error("no retry counter on the context; a suppressed notice must still count retries")
	}
}
