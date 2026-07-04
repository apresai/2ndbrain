package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/eval"
	"github.com/apresai/2ndbrain/internal/vault"
)

func TestEvalQACacheHas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qa.json")
	if qaCacheHas(path, 1) {
		t.Error("a missing cache file must be a cache-miss")
	}
	items := []eval.QAItem{{Question: "q1", SourceID: "d1"}, {Question: "q2", SourceID: "d2"}}
	data, _ := json.Marshal(items)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !qaCacheHas(path, 2) {
		t.Error("2 cached items must satisfy n=2")
	}
	if !qaCacheHas(path, 1) {
		t.Error("2 cached items must satisfy n=1")
	}
	if qaCacheHas(path, 3) {
		t.Error("2 cached items must NOT satisfy n=3 (triggers regeneration)")
	}
}

func TestEnsureVaultIgnores(t *testing.T) {
	// No .gitignore yet: the full 2nb block (including the eval entry) is written.
	root := t.TempDir()
	ensureVaultIgnores(root, ".2ndbrain/eval/")
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if !strings.Contains(string(gi), ".2ndbrain/eval/") {
		t.Errorf("no-gitignore case must write the eval entry, got: %q", gi)
	}

	// Existing .gitignore missing the entry: it is appended, old lines preserved.
	root2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root2, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureVaultIgnores(root2, ".2ndbrain/eval/")
	gi2, _ := os.ReadFile(filepath.Join(root2, ".gitignore"))
	if !strings.Contains(string(gi2), "node_modules/") || !strings.Contains(string(gi2), ".2ndbrain/eval/") {
		t.Errorf("existing gitignore must keep old lines and append the entry, got: %q", gi2)
	}

	// Idempotent: a second call does not duplicate the entry.
	ensureVaultIgnores(root2, ".2ndbrain/eval/")
	gi3, _ := os.ReadFile(filepath.Join(root2, ".gitignore"))
	if n := strings.Count(string(gi3), ".2ndbrain/eval/"); n != 1 {
		t.Errorf("entry must appear exactly once, got %d", n)
	}
}

func TestEvalReadout(t *testing.T) {
	if got := evalReadout(eval.ConfigMetrics{RecallAtK: 0.98, RecallAt1: 0.70}); !strings.Contains(got, "strong") {
		t.Errorf("high scores should read as strong: %q", got)
	}
	if got := evalReadout(eval.ConfigMetrics{RecallAtK: 0.92, RecallAt1: 0.40}); !strings.Contains(got, "Recall is high") {
		t.Errorf("high-recall/low-R@1 should nudge tuning: %q", got)
	}
	if got := evalReadout(eval.ConfigMetrics{RecallAtK: 0.60, RecallAt1: 0.30}); !strings.Contains(got, "lower than ideal") {
		t.Errorf("low recall should flag a problem: %q", got)
	}
}

// TestEval_E2E_Bedrock drives the whole command through the real CLI on a real
// embedded vault (no mocks): index → embed → generate a small QA set → scorecard.
// Skips when no embeddings result from indexing (no Bedrock credentials).
func TestEval_E2E_Bedrock(t *testing.T) {
	v, root := newContractVault(t)

	// A few substantial notes (>500 chars of body each — the QA generator's
	// threshold) so it has real content to bind a specific question to. Created
	// via `2nb create` so they carry proper frontmatter (id + title), which
	// candidateDocs requires.
	notes := []struct{ title, body string }{
		{"Authentication", "We authenticate users with JWT access tokens issued after an OAuth 2.0 authorization-code flow against Google as the identity provider. Access tokens are short-lived (15 minutes) and carry the user id and role claims. Refresh tokens rotate every 30 days and are stored in httpOnly, Secure, SameSite=strict cookies so browser JavaScript can never read them. On refresh we detect token reuse: if an already-rotated refresh token is presented we revoke the entire session family, which defeats stolen-token replay. Logout clears the cookie and adds the token id to a short-lived deny list."},
		{"Database Design", "The backend uses a single-table DynamoDB design. Every item has a composite primary key of partition key and sort key with entity-prefixed values such as USER and ORDER. Two global secondary indexes support the access patterns: GSI1 inverts the keys for reverse lookups, and GSI2 indexes by status and created-at for time-ordered queries. Identifiers are ULIDs, which are lexicographically sortable and encode their creation timestamp, so a range query on the sort key returns items in chronological order without a separate timestamp attribute. All writes use typed Go structs with dynamodbav tags."},
		{"Deployment", "All compute runs on AWS Lambda using the provided.al2023 runtime on ARM64 Graviton2, which is about twenty percent cheaper than x86 for equal or better performance. There are no EC2 instances or containers anywhere in the stack. Infrastructure is defined with AWS CDK v2 in TypeScript under the infrastructure directory, and every deploy runs clean, build, then cdk deploy via a Makefile target. Production is the only environment; there is no staging. The frontend is a Next.js app deployed with OpenNext to Lambda behind CloudFront and served from an edge cache."},
		{"Caching", "A CloudFront distribution fronts the public API. GET responses are cached at the edge for sixty seconds with a stale-while-revalidate window of ten minutes, so a burst of identical requests collapses to a single origin call while users still get fast responses during revalidation. Cache keys include the Accept-Language header and the authenticated user tier but deliberately exclude tracking query parameters. Mutations send an explicit invalidation for the affected path. Origin responses set Cache-Control and a content hash ETag so conditional requests short-circuit with a 304 Not Modified."},
		{"Hybrid Search", "Search is hybrid: a BM25 keyword channel backed by SQLite FTS5 runs alongside a dense vector channel backed by sqlite-vec, and the two rankings are fused with reciprocal rank fusion using a k of sixty. The vector channel embeds queries with an asymmetric retrieval purpose that differs from the document-side index purpose, which sharpens the match-versus-noise separation. Results below a cosine similarity threshold are dropped so weak neighbors do not pad the output. When no AI provider is configured the system degrades gracefully to keyword-only search rather than failing outright."},
		{"Billing", "Subscriptions and payments are handled entirely by Stripe. When a customer subscribes, Stripe sends a webhook that our reconciler verifies with the signing secret and then uses to grant or revoke entitlements in DynamoDB. We never store card numbers; only the Stripe customer id and subscription id live in our database. Failed payments trigger a dunning flow with three retry attempts over a week before the entitlement is suspended. Backup codes and the webhook signing secret are kept in the private certs repository, never in application config or environment variables."},
	}
	for _, n := range notes {
		if _, err := runCLIArgs(t, root, "create", "--type", "note", "--title", n.title, "--content", n.body); err != nil {
			t.Fatalf("create %q: %v", n.title, err)
		}
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// Re-open to observe committed embeddings from the index command's own handle.
	rv, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer rv.Close()
	if c, _ := rv.DB.EmbeddingCount(); c == 0 {
		t.Skip("no embeddings after index (no Bedrock credentials); skipping eval E2E")
	}
	_ = v

	out, err := runCLIArgs(t, root, "eval", "--json", "--n", "3", "--yes")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	var rep EvalReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("decode EvalReport: %v\noutput: %s", err, out)
	}
	if rep.N == 0 {
		t.Errorf("expected a non-zero question count, got %+v", rep)
	}
	if rep.RecallAtK < 0 || rep.RecallAtK > 1 || rep.RecallAt1 < 0 || rep.RecallAt1 > 1 {
		t.Errorf("metrics out of [0,1]: %+v", rep)
	}
}
