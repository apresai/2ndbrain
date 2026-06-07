package ai

import (
	"fmt"
	"testing"
)

func TestEnsureBedrockBearerToken(t *testing.T) {
	t.Run("env already set: keychain not consulted, no override", func(t *testing.T) {
		setCalls, keychainCalls := 0, 0
		ensureBedrockBearerToken(
			func(string) string { return "from-env" },
			func(string, string) error { setCalls++; return nil },
			func(string) (string, error) { keychainCalls++; return "from-keychain", nil },
		)
		if setCalls != 0 || keychainCalls != 0 {
			t.Errorf("env present should win untouched; got set=%d keychain=%d", setCalls, keychainCalls)
		}
	})

	t.Run("env empty + keychain token: exported under AWS_BEARER_TOKEN_BEDROCK", func(t *testing.T) {
		exported := map[string]string{}
		ensureBedrockBearerToken(
			func(string) string { return "" },
			func(k, v string) error { exported[k] = v; return nil },
			func(account string) (string, error) {
				if account != "bedrock" {
					t.Errorf("expected keychain account \"bedrock\", got %q", account)
				}
				return "ABSK-token", nil
			},
		)
		if exported[bedrockBearerTokenEnv] != "ABSK-token" {
			t.Errorf("expected token exported to %s, got %v", bedrockBearerTokenEnv, exported)
		}
	})

	t.Run("env empty + no keychain token: no-op", func(t *testing.T) {
		setCalls := 0
		ensureBedrockBearerToken(
			func(string) string { return "" },
			func(string, string) error { setCalls++; return nil },
			func(string) (string, error) { return "", fmt.Errorf("not found") },
		)
		if setCalls != 0 {
			t.Errorf("no token should be a no-op; got %d setenv calls", setCalls)
		}
	})
}
