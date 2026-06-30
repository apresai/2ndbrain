package ai

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

// TestIsBedrockRetryable locks the throttling fix: ThrottlingException (a client
// fault, HTTP 429) must now be retried, since the concurrent embed path makes it
// likely; a ValidationException (also client) must NOT be retried.
func TestIsBedrockRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"typed throttling", &runtimetypes.ThrottlingException{Message: aws.String("rate exceeded")}, true},
		{"typed internal server", &runtimetypes.InternalServerException{Message: aws.String("boom")}, true},
		{"generic throttling by code", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "x", Fault: smithy.FaultClient}, true},
		{"generic model timeout by code", &smithy.GenericAPIError{Code: "ModelTimeoutException", Message: "x", Fault: smithy.FaultServer}, true},
		{"generic server fault", &smithy.GenericAPIError{Code: "SomethingServer", Message: "x", Fault: smithy.FaultServer}, true},
		{"validation client fault not retryable", &runtimetypes.ValidationException{Message: aws.String("bad input")}, false},
		{"plain error not retryable", errors.New("network blip"), false},
		{"nil not retryable", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBedrockRetryable(tt.err); got != tt.want {
				t.Errorf("isBedrockRetryable(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestBedrockRetryDelayJitterBounds(t *testing.T) {
	if bedrockRetryDelay(0) != 0 {
		t.Errorf("attempt 0 delay = %v, want 0", bedrockRetryDelay(0))
	}
	// Equal jitter: delay ∈ [base/2, base]. attempt 1 base 200ms → [100ms,200ms];
	// attempt 4 base 1.6s → [800ms,1.6s]. Sample to catch a jitter that escapes.
	for i := 0; i < 200; i++ {
		if d := bedrockRetryDelay(1); d < 100*time.Millisecond || d > 200*time.Millisecond {
			t.Fatalf("attempt 1 delay %v out of [100ms,200ms]", d)
		}
		if d := bedrockRetryDelay(4); d < 800*time.Millisecond || d > 1600*time.Millisecond {
			t.Fatalf("attempt 4 delay %v out of [800ms,1.6s]", d)
		}
	}
}
