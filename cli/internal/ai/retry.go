package ai

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

// Provider retries used to be completely silent. A throttled Bedrock account
// rides out up to maxBedrockAttempts backoffs capped at 10s each, on top of the
// SDK's own attempts, so a single embed call can legitimately take tens of
// seconds; nothing logged it and no CLI or app surface mentioned it. Measured on
// a real vault: single-note reindexes at 10 to 14s, a two-document embed pass at
// 53.6s, a search whose query embedding alone took 10.8s, against 0.7s for the
// same call when the account was not throttled. Every one of those looked to the
// user like the tool had hung.
//
// Two things close that. Every retry is logged here, and a RetryCounter the
// caller attaches to the context accumulates them so a live surface (the CLI's
// slow-call notice) and the metrics observatory can report what actually
// happened rather than guessing from wall time.

// RetryState is one self-consistent view of a counter: a count, the cause of the
// retry that produced it, and the attempt budget of the loop that recorded it.
// The three always belong to the same retry, which is why it is one value rather
// than three reads.
type RetryState struct {
	Count       int
	Cause       string
	MaxAttempts int
}

// RetryCounter accumulates provider retries for one logical operation. Safe for
// concurrent use: the bulk embed pass runs many calls at once and they all
// report into the counter their shared context carries.
//
// The state is kept as ONE immutable value behind a single atomic pointer, so a
// reader can never see a count from one retry paired with the cause or the
// attempt budget of another. Three independent atomics could: the mantle loop
// allows three attempts and the classic loops five, so a reader interleaving
// with a record could render "retry 3 of 5" for a call whose real budget was
// three, which is the notice telling the user they have patience left that they
// do not. Writes are serialized by mu (a read-modify-write of the count);
// reads take no lock.
type RetryCounter struct {
	mu    sync.Mutex
	state atomic.Pointer[RetryState]
}

// Snapshot returns the whole state at once. Prefer it wherever more than one
// field is rendered together.
func (c *RetryCounter) Snapshot() RetryState {
	if c == nil {
		return RetryState{}
	}
	if s := c.state.Load(); s != nil {
		return *s
	}
	return RetryState{}
}

// Count returns how many retries have been recorded so far.
func (c *RetryCounter) Count() int { return c.Snapshot().Count }

// Cause returns the most recent retry's human cause ("Bedrock throttled"), or
// "" when nothing has retried.
func (c *RetryCounter) Cause() string { return c.Snapshot().Cause }

// MaxAttempts returns the attempt budget of the loop that recorded the most
// recent retry, or 0 when nothing has retried. It is recorded rather than
// assumed because the planes do not agree: the classic loops allow
// maxBedrockAttempts, the mantle ones mantleMaxAttempts.
func (c *RetryCounter) MaxAttempts() int { return c.Snapshot().MaxAttempts }

// Record adds one retry with the cause and attempt budget of the loop that made
// it. Callers are the provider retry loops; it is exported so a segregated test
// helper package can drive a retry-counting call site without reaching a
// provider, rather than the ai package carrying a test-only symbol.
func (c *RetryCounter) Record(cause string, maxAttempts int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := RetryState{Count: 1, Cause: cause, MaxAttempts: maxAttempts}
	if cur := c.state.Load(); cur != nil {
		next.Count = cur.Count + 1
	}
	c.state.Store(&next)
}

type retryCounterKey struct{}

// WithRetryCounter attaches a counter to ctx. Every provider retry made under
// that context is recorded on it.
func WithRetryCounter(ctx context.Context, c *RetryCounter) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, retryCounterKey{}, c)
}

// RetryCounterFrom returns the counter attached to ctx, or nil. A nil counter is
// usable: every method on it is nil-safe.
func RetryCounterFrom(ctx context.Context) *RetryCounter {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(retryCounterKey{}).(*RetryCounter)
	return c
}

// RetriesFrom returns how many retries were recorded under ctx.
func RetriesFrom(ctx context.Context) int { return RetryCounterFrom(ctx).Count() }

// noteBedrockRetry logs one retry and records it on ctx's counter, if any. It is
// called before the backoff sleep, so the log says how long the wait will be
// rather than only that one happened.
func noteBedrockRetry(ctx context.Context, plane string, attempt, max int, wait time.Duration, err error) {
	cause := bedrockRetryCause(err)
	slog.Info("bedrock retry",
		"plane", plane,
		"attempt", attempt,
		"of", max,
		"wait", wait,
		"cause", cause,
		"err", err,
	)
	RetryCounterFrom(ctx).Record("Bedrock "+cause, max)
}

// bedrockRetryCause names why a retryable Bedrock error is being retried, in
// words a user can act on: "throttled" points at ai.embed_concurrency, a server
// error points at nothing the user controls. Kept in step with
// isBedrockRetryable, which decides what reaches here at all.
func bedrockRetryCause(err error) string {
	var throttle *runtimetypes.ThrottlingException
	if errors.As(err, &throttle) {
		return "throttled"
	}
	var internal *runtimetypes.InternalServerException
	if errors.As(err, &internal) {
		return "server error"
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch code := apiErr.ErrorCode(); {
		case strings.EqualFold(code, "ThrottlingException"),
			strings.EqualFold(code, "TooManyRequestsException"):
			return "throttled"
		case strings.EqualFold(code, "ServiceUnavailableException"):
			return "service unavailable"
		case strings.EqualFold(code, "ModelTimeoutException"):
			return "model timeout"
		case strings.EqualFold(code, "InternalServerException"):
			return "server error"
		case code != "":
			return code
		}
	}
	return "retryable error"
}

// noteMantleRetry logs one mantle-plane retry. That plane answers with an HTTP
// status rather than a typed AWS error, so the cause is fixed rather than
// classified. Same counter, so a slow mantle call explains itself the way a
// slow classic one does.
func noteMantleRetry(ctx context.Context, attempt, max int, wait time.Duration, status int) {
	slog.Info("bedrock retry",
		"plane", "mantle",
		"attempt", attempt,
		"of", max,
		"wait", wait,
		"cause", "throttled",
		"status", status,
	)
	RetryCounterFrom(ctx).Record("Bedrock throttled", max)
}
