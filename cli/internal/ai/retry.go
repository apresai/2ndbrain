package ai

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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

// RetryCounter accumulates provider retries for one logical operation. Safe for
// concurrent use: the bulk embed pass runs many calls at once and they all
// report into the counter their shared context carries.
type RetryCounter struct {
	n     atomic.Int64
	cause atomic.Value // string, the most recent retry's human cause
}

// Count returns how many retries have been recorded so far.
func (c *RetryCounter) Count() int {
	if c == nil {
		return 0
	}
	return int(c.n.Load())
}

// Cause returns the most recent retry's human cause ("Bedrock throttled"), or
// "" when nothing has retried.
func (c *RetryCounter) Cause() string {
	if c == nil {
		return ""
	}
	s, _ := c.cause.Load().(string)
	return s
}

func (c *RetryCounter) record(cause string) {
	if c == nil {
		return
	}
	c.n.Add(1)
	c.cause.Store(cause)
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
	RetryCounterFrom(ctx).record("Bedrock " + cause)
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
	RetryCounterFrom(ctx).record("Bedrock throttled")
}
