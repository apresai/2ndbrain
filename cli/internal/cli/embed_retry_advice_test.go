package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/vault"
)

// seedLastBuild records one build row in the vault's observatory.
func seedLastBuild(t *testing.T, v *vault.Vault, retries int) {
	t.Helper()
	mdb, err := openMetricsDB(v)
	if err != nil {
		t.Fatalf("open metrics db: %v", err)
	}
	defer mdb.Close()
	if err := mdb.Record(metrics.Operation{
		Operation: metrics.OpReembed, DurationMs: 60000, Embedded: 313,
		EmbedRetries: retries, OK: true,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
}

// TestEmbedRetryAdviceFiresOnlyWhenTheUserRaisedConcurrency: telling someone to
// lower a number they never raised is noise, and a throttled account at the
// automatic setting is the provider's quota, not a setting to change.
func TestEmbedRetryAdviceFiresOnlyWhenTheUserRaisedConcurrency(t *testing.T) {
	automatic := ai.ProviderEmbedConcurrencyDefault("bedrock")

	cases := []struct {
		name        string
		concurrency int
		retries     int
		want        bool
	}{
		{"raised and throttled", automatic + 8, 12, true},
		{"raised but no retries", automatic + 8, 0, false},
		{"automatic and throttled", 0, 12, false},
		{"at the automatic value and throttled", automatic, 12, false},
		{"below automatic and throttled", 1, 12, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := newContractVault(t)
			v.Config.AI.Provider = "bedrock"
			v.Config.AI.EmbedConcurrency = tc.concurrency
			seedLastBuild(t, v, tc.retries)

			got := embedRetryAdvice(v)
			if (got != "") != tc.want {
				t.Fatalf("embedRetryAdvice = %q, want present=%v", got, tc.want)
			}
			if !tc.want {
				return
			}
			for _, want := range []string{
				"12 throttled retries in the last index",
				"embed_concurrency is",
				"consider lowering ai.embed_concurrency",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("advice %q is missing %q", got, want)
				}
			}
		})
	}
}

// TestEmbedRetryAdviceWithNoHistorySaysNothing: a fresh vault has no build row,
// and the observatory is best-effort, so silence is the only right answer.
func TestEmbedRetryAdviceWithNoHistorySaysNothing(t *testing.T) {
	v, _ := newContractVault(t)
	v.Config.AI.Provider = "bedrock"
	v.Config.AI.EmbedConcurrency = 32
	if got := embedRetryAdvice(v); got != "" {
		t.Errorf("embedRetryAdvice on a vault with no index history = %q, want empty", got)
	}
}

// TestVaultStatusJSONCarriesTheAdvice: the macOS app reads the JSON, so the
// advice must be a field, not only a printed line.
func TestVaultStatusJSONCarriesTheAdvice(t *testing.T) {
	v, root := newContractVault(t)
	// Written through the real config save, since the assertion runs the CLI in
	// a fresh process-level command that reads the vault from disk.
	v.Config.AI.Provider = "bedrock"
	v.Config.AI.EmbedConcurrency = 32
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedLastBuild(t, v, 9)

	out, err := runCLIArgs(t, root, "vault", "status", "--json")
	if err != nil {
		t.Fatalf("vault status --json: %v", err)
	}
	var got struct {
		EmbedRetryAdvice string `json:"embed_retry_advice"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if !strings.Contains(got.EmbedRetryAdvice, "9 throttled retries in the last index") {
		t.Errorf("embed_retry_advice = %q, want it to report the 9 retries", got.EmbedRetryAdvice)
	}
}
