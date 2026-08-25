package e2e_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Capability gating for the credential-dependent tests.
//
// hasAWSCreds/hasOpenRouterKey answer "is a credential env var SET", which is
// not the question these tests need. In the state where the variables exist but
// the provider cannot actually embed (an expired token, a wrong region, a
// network that is down), an env-var check says "go ahead" and the test FAILS
// instead of skipping. That is exactly how a transient outage once read as a
// product regression and a blocked release.
//
// embeddingCapable answers the real question by asking the binary to probe the
// configured embedding model once per test binary. Going through the binary
// rather than importing internal/ai matters: the binary resolves its own token,
// region, and config (including the stored-key-over-env precedence), and that
// resolution is precisely what these tests depend on, so an in-process probe
// could disagree with the thing under test.
var (
	embedCapOnce   sync.Once
	embedCapOK     bool
	embedCapReason string
)

// embeddingCapable reports whether the configured embedding provider can
// actually embed right now, plus a human reason when it cannot. Probes at most
// once per test binary.
func embeddingCapable(t *testing.T) (bool, string) {
	t.Helper()
	embedCapOnce.Do(func() {
		// Cheap negative first: with no credentials at all there is nothing to
		// probe, so spawn nothing. This is the CI path.
		if !hasAWSCreds() && !hasOpenRouterKey() {
			embedCapOK, embedCapReason = false, "no AI provider credentials configured"
			return
		}
		embedCapOK, embedCapReason = probeEmbedding(t)
	})
	return embedCapOK, embedCapReason
}

// requireEmbedding skips the test unless the provider can really embed.
func requireEmbedding(t *testing.T) {
	t.Helper()
	if ok, reason := embeddingCapable(t); !ok {
		t.Skipf("embedding provider not usable: %s", reason)
	}
}

// probeEmbedding runs one real embedding call in a throwaway vault and reports
// the outcome. It uses `ai embed`, the shortest path to "can this actually
// embed", rather than a readiness probe, because readiness and the runtime are
// different API surfaces and only the runtime is what these tests exercise.
func probeEmbedding(t *testing.T) (bool, string) {
	t.Helper()
	home := t.TempDir()
	vault := filepath.Join(home, "cap-vault")

	if out, code := runWithHome(t, home, "vault", "create", vault); code != 0 {
		return false, "vault create failed during the capability probe: " + firstLine(out)
	}
	out, code := runWithHome(t, home, "--vault", vault, "--unconfigured", "ai", "embed", "capability probe", "--json")
	if code != 0 {
		return false, firstLine(out)
	}
	// A JSON array of floats is a successful embedding.
	var vec []float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &vec); err != nil || len(vec) == 0 {
		return false, "embedding probe returned no vector"
	}
	return true, ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

// hasAnyProviderEnv is the cheap pre-filter the guards used to use directly.
// Kept because it is genuinely useful as a first cut, but never as the whole
// answer: see embeddingCapable.
func hasAnyProviderEnv() bool { return hasAWSCreds() || hasOpenRouterKey() }
