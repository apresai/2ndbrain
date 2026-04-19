package mcp

import (
	"context"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestWithTimeout_EnforcesDeadline(t *testing.T) {
	// A slow handler (500ms) wrapped with a 50ms budget must see ctx.Done
	// before it finishes work. We verify cancellation propagated by checking
	// the ctx err that the handler captured.
	var observedErr error
	slow := func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		select {
		case <-ctx.Done():
			observedErr = ctx.Err()
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return mcplib.NewToolResultText("late"), nil
		}
	}

	wrapped := withTimeout(50*time.Millisecond, slow)
	start := time.Now()
	_, err := wrapped(context.Background(), mcplib.CallToolRequest{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from deadline-exceeded handler, got nil")
	}
	if observedErr != context.DeadlineExceeded {
		t.Errorf("handler saw ctx.Err() = %v, want DeadlineExceeded", observedErr)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("wrapper waited %v before returning — should have cut at ~50ms", elapsed)
	}
}

func TestWithTimeout_PropagatesResult(t *testing.T) {
	fast := func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText("ok"), nil
	}
	wrapped := withTimeout(1*time.Second, fast)
	res, err := wrapped(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("result was nil")
	}
}

func TestWithTimeout_PropagatesIncomingCancellation(t *testing.T) {
	// If the parent context is already cancelled, the wrapped handler should
	// see that cancellation immediately — the fresh deadline shouldn't reset
	// the clock.
	var observedErr error
	handler := func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		observedErr = ctx.Err()
		return nil, ctx.Err()
	}

	parent, cancel := context.WithCancel(context.Background())
	cancel()

	wrapped := withTimeout(1*time.Second, handler)
	_, err := wrapped(parent, mcplib.CallToolRequest{})
	if err == nil {
		t.Fatal("expected error from pre-cancelled parent, got nil")
	}
	if observedErr != context.Canceled {
		t.Errorf("handler saw ctx.Err() = %v, want Canceled", observedErr)
	}
}
