package mcp

import (
	"context"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestIdleWatchdog_ExpiresWhenIdle(t *testing.T) {
	fired := make(chan struct{}, 1)
	w := newIdleWatchdog(40*time.Millisecond, func() { fired <- struct{}{} })
	go w.run()
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not fire after the idle timeout")
	}
}

func TestIdleWatchdog_DisabledNeverFires(t *testing.T) {
	fired := make(chan struct{}, 1)
	w := newIdleWatchdog(0, func() { fired <- struct{}{} })
	go w.run() // returns immediately
	select {
	case <-fired:
		t.Fatal("a disabled (timeout<=0) watchdog must never fire")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestIdleWatchdog_ActivityDefersExpiry(t *testing.T) {
	fired := make(chan struct{}, 1)
	w := newIdleWatchdog(80*time.Millisecond, func() { fired <- struct{}{} })
	go w.run()

	handler := w.wrap(func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return nil, nil
	})
	// Keep invoking for ~160ms (2x the timeout); each call refreshes the clock,
	// so the watchdog must NOT fire while activity continues.
	deadline := time.Now().Add(160 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _ = handler(context.Background(), mcplib.CallToolRequest{})
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case <-fired:
		t.Fatal("watchdog fired despite continuous activity")
	default:
	}
}

func TestIdleWatchdog_InFlightBlocksExpiry(t *testing.T) {
	fired := make(chan struct{}, 1)
	w := newIdleWatchdog(40*time.Millisecond, func() { fired <- struct{}{} })
	go w.run()

	// A handler that blocks holds inFlight > 0; the watchdog must not fire while
	// the request is in flight even though the clock is well past the timeout.
	release := make(chan struct{})
	handler := w.wrap(func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		<-release
		return nil, nil
	})
	done := make(chan struct{})
	go func() { _, _ = handler(context.Background(), mcplib.CallToolRequest{}); close(done) }()

	select {
	case <-fired:
		t.Fatal("watchdog fired while a request was in flight")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-done
	// After the in-flight request completes, it should eventually fire.
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not fire after the in-flight request finished")
	}
}
