package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
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
//
// There are TWO verdicts because the tests run in two different environments,
// and a single one is wrong for half of them. The battery drives the binary
// under a throwaway HOME (runWithHome), where only environment credentials are
// visible. TestE2E_* drives it under the caller's real HOME (run), where
// ~/.aws, ~/.config/2nb/bedrock.json, and the Keychain are visible too.
//
// A temp-HOME verdict is strictly more pessimistic, so gating the real-HOME
// tests on it can never make one fail: it makes them SKIP in exactly the setups
// they used to exercise (credentials in ~/.aws with AWS_PROFILE set), and a
// silent skip in the maintainer's own configuration is its own kind of lie.
var (
	embedCapTemp capVerdict // probed under a throwaway HOME
	embedCapHost capVerdict // probed under the caller's real HOME
)

type capVerdict struct {
	once   sync.Once
	ok     bool
	reason string
}

// resolve probes at most once per verdict per test binary.
func (c *capVerdict) resolve(t *testing.T, hostHome bool) (bool, string) {
	t.Helper()
	c.once.Do(func() {
		// Cheap negative first: with nothing that could carry a credential
		// there is nothing to probe, so spawn nothing. This is the CI path.
		if !hasAnyProviderCredentialSource(hostHome) {
			c.ok, c.reason = false, "no AI provider credentials configured"
			return
		}
		c.ok, c.reason = probeEmbedding(t, hostHome)
	})
	return c.ok, c.reason
}

// embeddingCapable reports whether the configured embedding provider can
// actually embed right now under a throwaway HOME, plus a human reason when it
// cannot.
func embeddingCapable(t *testing.T) (bool, string) {
	t.Helper()
	return embedCapTemp.resolve(t, false)
}

// requireEmbedding skips the test unless the provider can really embed under a
// throwaway HOME. Use it for tests that drive the binary via runWithHome.
func requireEmbedding(t *testing.T) {
	t.Helper()
	if ok, reason := embeddingCapable(t); !ok {
		t.Skipf("embedding provider not usable: %s", reason)
	}
}

// requireEmbeddingHostHome skips the test unless the provider can embed with
// the caller's real HOME visible. Use it for tests that drive the binary via
// run/runStdout, which inherit the environment wholesale.
func requireEmbeddingHostHome(t *testing.T) {
	t.Helper()
	if ok, reason := embedCapHost.resolve(t, true); !ok {
		t.Skipf("embedding provider not usable: %s", reason)
	}
}

// hasAnyProviderCredentialSource is the cheap pre-filter: is there anything
// here that could possibly carry a credential? Environment variables include
// the Bedrock API key, which is how the macOS app and most of this project's
// own tooling authenticate; omitting it would skip a fully working setup.
// Under the real HOME, the file-based sources count too.
func hasAnyProviderCredentialSource(hostHome bool) bool {
	if hasBedrockCredentialEnv() || hasOpenRouterKey() {
		return true
	}
	// File-based sources exist only where the real home is visible.
	return hostHome && hasBedrockCredentialFile()
}

// hasBedrockCredentialEnv covers the environment variables that can carry a
// Bedrock credential. AWS_BEARER_TOKEN_BEDROCK is the Bedrock API key, which is
// how the macOS app and most of this project's own tooling authenticate, and it
// sets none of the variables hasAWSCreds looks at.
func hasBedrockCredentialEnv() bool {
	return hasAWSCreds() || os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != ""
}

// hasBedrockCredentialFile covers the on-disk sources, visible only under the
// real home: the shared AWS config and 2nb's own machine-local key file.
func hasBedrockCredentialFile() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, p := range []string{
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".aws", "config"),
		filepath.Join(home, ".config", "2nb", "bedrock.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// hasBedrockCredentialSource answers "could this host reach Bedrock at all",
// for tests that run under the real home and must choose a provider. It is
// deliberately broader than hasAWSCreds, which sees only three env vars: a host
// with a Bedrock API key or ~/.aws credentials passes the capability gate, and
// picking a provider on the narrow question would then aim at the wrong one.
func hasBedrockCredentialSource() bool {
	return hasBedrockCredentialEnv() || hasBedrockCredentialFile()
}

// probeEmbedding runs one real embedding call in a throwaway vault and reports
// the outcome. It uses `ai embed`, the shortest path to "can this actually
// embed", rather than a readiness probe, because readiness and the runtime are
// different API surfaces and only the runtime is what these tests exercise.
//
// hostHome inherits the caller's HOME instead of a throwaway one, matching what
// run() gives the tests it gates. 2NB_TEST is set on that path so the probe
// cannot write recents or skill files into the developer's real home; the
// throwaway path deliberately leaves it unset, exactly as runWithHome does.
func probeEmbedding(t *testing.T, hostHome bool) (bool, string) {
	t.Helper()
	home := isolatedHome(t)
	vault := filepath.Join(t.TempDir(), "cap-vault")

	runner := func(args ...string) (string, int) { return runWithHome(t, home, args...) }
	if hostHome {
		runner = func(args ...string) (string, int) { return runHostHome(t, args...) }
	}

	if out, code := runner("vault", "create", vault); code != 0 {
		return false, "vault create failed during the capability probe: " + firstLine(out)
	}
	out, code := runner("--vault", vault, "--unconfigured", "ai", "embed", "capability probe", "--json")
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

// runHostHome runs the binary with the environment inherited wholesale, which
// is what run() does, plus 2NB_TEST=1 so a probe never writes into the real
// home. Returns stdout, with stderr appended on failure so the skip reason is
// the actual error.
func runHostHome(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), "2NB_TEST=1")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	out := stdout.String()
	if code != 0 {
		out += "\n" + stderr.String()
	}
	return out, code
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
