// Package netstall bounds STALLED network transfers without capping total
// transfer time. A blanket http.Client.Timeout wrongly kills slow work: a
// legitimately slow multi-GB download that keeps making progress is
// indistinguishable, to a wall clock, from a half-open socket delivering
// nothing. The honest split is byte progress: a transfer that keeps producing
// bytes is never aborted, a transfer that goes silent for the stall window is
// cancelled promptly, and the connect phase (dial + TLS) stays tight so a
// dead network fails in seconds rather than waiting out the stall window.
//
// Extracted from the llama model downloader (internal/llama), which pulls
// multi-GB GGUF weights; also used by the plugin asset download in
// internal/cli. Callers that CAN judge progress (streaming reads) should use
// Guard; calls whose result arrives all at once after server-side work (a
// non-streaming model generation) carry no byte-progress signal until the
// work is done and need a generous wall-clock attempt bound instead — see
// internal/ai/timeouts.go.
package netstall

import (
	"io"
	"net"
	"net/http"
	"time"
)

const (
	// DefaultStallTimeout is the standard "no bytes arrived" window before a
	// transfer is declared dead. Long enough to ride out a congested link or a
	// server-side hiccup, short enough that a half-open socket cannot park a
	// download for minutes.
	DefaultStallTimeout = 60 * time.Second

	// DialTimeout bounds the TCP connect. A dead or blackholed network should
	// fail in seconds; nothing about a slow transfer justifies a slow dial.
	DialTimeout = 10 * time.Second

	// TLSHandshakeTimeout bounds the TLS handshake, for the same reason.
	TLSHandshakeTimeout = 10 * time.Second
)

// Transport returns an *http.Transport cloned from http.DefaultTransport
// (keeping proxy, HTTP/2, and pool settings) with the tight connect-phase
// bounds above. It deliberately sets NO ResponseHeaderTimeout: for a plain
// download headers arrive immediately and callers may add one, but for a
// non-streaming API call the headers arrive only after the server finishes
// its work, so a blanket header bound would kill working requests.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = TLSHandshakeTimeout
	return t
}

// Guard wraps r with a progress-resetting idle watchdog: cancel is invoked if
// no bytes are read for stallTimeout, and every non-empty read pushes the
// deadline out again, so a transfer that keeps progressing is never aborted.
// cancel is typically a context.CancelFunc whose context bounds the HTTP
// request, making the in-flight read return promptly. The returned stop
// function disarms the watchdog; call it (defer is fine) once the copy
// finishes so a completed transfer cannot fire a late cancel.
func Guard(r io.Reader, stallTimeout time.Duration, cancel func()) (io.Reader, func()) {
	watchdog := time.AfterFunc(stallTimeout, cancel)
	guarded := &reader{r: r, reset: func() { watchdog.Reset(stallTimeout) }}
	return guarded, func() { watchdog.Stop() }
}

// reader resets the idle watchdog on each non-empty read, so a transfer that
// stops delivering bytes is cancelled by the watchdog rather than blocking
// the enclosing io.Copy forever.
type reader struct {
	r     io.Reader
	reset func()
}

func (s *reader) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if n > 0 {
		s.reset()
	}
	return n, err
}
