package ai

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

// captureHandler collects slog records so a test can assert on what was logged.
// It is a log SINK, not a provider mock: no request is made and no HTTP server
// is involved, which is what the no-mocks policy is about.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) find(msg string) (slog.Record, map[string]any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]any{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		return r, attrs, true
	}
	return slog.Record{}, nil, false
}

func captureLogs(t *testing.T) *captureHandler {
	t.Helper()
	h := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// TestBedrockRetryIsLoggedAndCounted is the whole point of the retry notice:
// before this, a throttled account rode out backoffs of up to 10s each, up to
// five times, with nothing written anywhere. The error is a real
// ThrottlingException value classified by the real isBedrockRetryable, so the
// test exercises the production classification rather than a stand-in for it.
func TestBedrockRetryIsLoggedAndCounted(t *testing.T) {
	err := &runtimetypes.ThrottlingException{}
	if !isBedrockRetryable(err) {
		t.Fatal("ThrottlingException must be retryable; this test watches the retry path")
	}

	h := captureLogs(t)
	counter := &RetryCounter{}
	ctx := WithRetryCounter(context.Background(), counter)

	noteBedrockRetry(ctx, "classic", 2, maxBedrockAttempts, 400*time.Millisecond, err)

	rec, attrs, ok := h.find("bedrock retry")
	if !ok {
		t.Fatal(`no "bedrock retry" record was logged; a silent retry is exactly the bug`)
	}
	if rec.Level != slog.LevelInfo {
		t.Errorf("level = %v, want Info: a retry is normal operation, not a warning", rec.Level)
	}
	if attrs["attempt"] != int64(2) {
		t.Errorf("attempt = %v, want 2", attrs["attempt"])
	}
	if attrs["of"] != int64(maxBedrockAttempts) {
		t.Errorf("of = %v, want %d", attrs["of"], maxBedrockAttempts)
	}
	if attrs["wait"] != 400*time.Millisecond {
		t.Errorf("wait = %v, want 400ms: the log has to say how long the silence will be", attrs["wait"])
	}
	if attrs["cause"] != "throttled" {
		t.Errorf("cause = %v, want throttled", attrs["cause"])
	}

	if counter.Count() != 1 {
		t.Errorf("counter = %d, want 1", counter.Count())
	}
	if counter.Cause() != "Bedrock throttled" {
		t.Errorf("counter cause = %q, want %q", counter.Cause(), "Bedrock throttled")
	}
	if got := RetriesFrom(ctx); got != 1 {
		t.Errorf("RetriesFrom = %d, want 1", got)
	}
}

// TestRetryCounterIsOptional: a call made without a counter must not panic, and
// the log still happens. Most call paths (the MCP server, a probe) attach none.
func TestRetryCounterIsOptional(t *testing.T) {
	h := captureLogs(t)
	noteBedrockRetry(context.Background(), "classic", 1, 5, time.Second, &runtimetypes.ThrottlingException{})
	if _, _, ok := h.find("bedrock retry"); !ok {
		t.Error("a retry with no counter attached must still be logged")
	}
	if got := RetriesFrom(context.Background()); got != 0 {
		t.Errorf("RetriesFrom with no counter = %d, want 0", got)
	}
}

// TestMantleRetryUsesTheSameCounter: the mantle plane answers with a status
// code rather than a typed error, and its retries must reach the same surfaces.
func TestMantleRetryUsesTheSameCounter(t *testing.T) {
	h := captureLogs(t)
	counter := &RetryCounter{}
	ctx := WithRetryCounter(context.Background(), counter)

	noteMantleRetry(ctx, 1, mantleMaxAttempts, time.Second, 429)

	_, attrs, ok := h.find("bedrock retry")
	if !ok {
		t.Fatal(`no "bedrock retry" record for the mantle plane`)
	}
	if attrs["plane"] != "mantle" {
		t.Errorf("plane = %v, want mantle", attrs["plane"])
	}
	if attrs["status"] != int64(429) {
		t.Errorf("status = %v, want 429", attrs["status"])
	}
	if counter.Count() != 1 || counter.Cause() != "Bedrock throttled" {
		t.Errorf("counter = %d / %q, want 1 / %q", counter.Count(), counter.Cause(), "Bedrock throttled")
	}
}

// TestBedrockRetryCauseNamesTheCode keeps the cause useful: "throttled" points
// the user at ai.embed_concurrency, a server error points at nothing they own.
func TestBedrockRetryCauseNamesTheCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"typed throttle", &runtimetypes.ThrottlingException{}, "throttled"},
		{"typed internal", &runtimetypes.InternalServerException{}, "server error"},
		{"generic 429", &smithy.GenericAPIError{Code: "TooManyRequestsException"}, "throttled"},
		{"generic unavailable", &smithy.GenericAPIError{Code: "ServiceUnavailableException"}, "service unavailable"},
		{"generic model timeout", &smithy.GenericAPIError{Code: "ModelTimeoutException"}, "model timeout"},
		{"unclassifiable", context.DeadlineExceeded, "retryable error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bedrockRetryCause(tc.err); got != tc.want {
				t.Errorf("bedrockRetryCause = %q, want %q", got, tc.want)
			}
		})
	}
}
