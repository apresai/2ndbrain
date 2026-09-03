// Package aitest holds test-only helpers built on the ai package's exported
// surface. It exists so the production ai package carries no test-only symbol:
// RecordRetryForTest used to live beside the retry loops it imitates, where it
// was indistinguishable from production API to anything reading that file.
//
// Nothing in the product imports this package; it is imported only from _test.go
// files in packages that cannot reach ai's unexported internals.
package aitest

import (
	"context"

	"github.com/apresai/2ndbrain/internal/ai"
)

// RecordRetry records one throttling retry against the counter attached to ctx,
// through the same Record the provider loops call. It lets a test in another
// package exercise a retry-counting call site without reaching a provider, which
// the no-mocks policy would otherwise leave untestable.
func RecordRetry(ctx context.Context, maxAttempts int) {
	ai.RetryCounterFrom(ctx).Record("Bedrock throttled", maxAttempts)
}
