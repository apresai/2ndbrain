package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/eval"
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
	requireEmbeddings(t, root)
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

	// Second run reuses the cached QA set (no regeneration) — qa_cached=true.
	// Pins the GUI's exact argv shape (Testing tab Quality pane): --yes plus a
	// derived --cost-cap, which a cached run must never trip.
	out2, err := runCLIArgs(t, root, "eval", "--json", "--n", "3", "--yes", "--cost-cap", "0.51")
	if err != nil {
		t.Fatalf("eval re-run: %v", err)
	}
	var rep2 EvalReport
	if err := json.Unmarshal(out2, &rep2); err != nil {
		t.Fatalf("decode re-run EvalReport: %v\noutput: %s", err, out2)
	}
	if !rep2.QACached {
		t.Errorf("second run should reuse the cached QA set (qa_cached=true), got %+v", rep2)
	}
}

// TestEval_NoEmbeddings covers the guard path with no credentials: an
// un-embedded vault errors before any API call.
func TestEval_NoEmbeddings(t *testing.T) {
	_, root := newContractVault(t)
	out, err := runCLIArgs(t, root, "eval", "--json", "--yes")
	if err == nil {
		t.Fatalf("eval on an un-embedded vault should error, got output: %s", out)
	}
	if !strings.Contains(err.Error(), "no embeddings") {
		t.Errorf("expected a 'no embeddings' hint, got: %v", err)
	}
}

// TestBuildEvalEstimate covers the estimate composition without a vault or a
// live catalog: cached kills the generation term, answers adds the jury term,
// and the total is always the sum of the parts.
func TestBuildEvalEstimate(t *testing.T) {
	genM := ai.ModelInfo{ID: "g", Provider: "bedrock", PriceIn: 10, PriceOut: 30}
	embM := ai.ModelInfo{ID: "e", Provider: "bedrock", PriceIn: 0.1}

	fresh := buildEvalEstimate("eval", false, 20, genM, embM, nil, 0.25)
	if fresh.GenerationUSD <= 0 || fresh.EmbedUSD < 0 {
		t.Fatalf("uncached estimate must carry the generation cost: %+v", fresh)
	}
	if fresh.TotalUSD != fresh.GenerationUSD+fresh.EmbedUSD+fresh.AnswersUSD {
		t.Errorf("total must sum the parts: %+v", fresh)
	}

	cached := buildEvalEstimate("eval", true, 20, genM, embM, nil, 0.25)
	if cached.GenerationUSD != 0 {
		t.Errorf("cached estimate must zero the one-time generation cost: %+v", cached)
	}
	if cached.EmbedUSD != fresh.EmbedUSD {
		t.Errorf("query embeds recur every run, cached or not: %+v vs %+v", cached, fresh)
	}

	answers := buildEvalEstimate("answers", true, 20, genM, embM, []ai.ModelInfo{genM}, 0.25)
	if answers.AnswersUSD <= 0 {
		t.Errorf("answers estimate must carry the answers+judging cost: %+v", answers)
	}
	if answers.TotalUSD <= cached.TotalUSD {
		t.Errorf("answers must cost more than the bare scorecard: %+v", answers)
	}
}

// TestEvalEstimate_Contract drives `--estimate` through the real CLI argv the
// GUI sends. Credential-free by design: an estimate must work on exactly the
// vaults the real run would refuse (no embeddings, no reachable provider).
func TestEvalEstimate_Contract(t *testing.T) {
	_, root := newContractVault(t)

	out, err := runCLIArgs(t, root, "eval", "--estimate", "--json")
	if err != nil {
		t.Fatalf("eval --estimate: %v", err)
	}
	var rep EvalEstimateReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("decode estimate: %v\noutput: %s", err, out)
	}
	if rep.Command != "eval" || rep.QACached || rep.N != 20 || rep.TotalUSD < 0 {
		t.Errorf("fresh-vault estimate looks wrong: %+v", rep)
	}
	if rep.AnswersUSD != 0 {
		t.Errorf("bare eval estimate must not carry an answers term: %+v", rep)
	}

	// Seed a QA cache: the estimate must flip to cached, drop the generation
	// term, and clamp n to the cache size.
	qaPath := filepath.Join(root, ".2ndbrain", "eval", "qa.json")
	if err := os.MkdirAll(filepath.Dir(qaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	items := []eval.QAItem{{Question: "q1", SourceID: "d1"}, {Question: "q2", SourceID: "d2"}, {Question: "q3", SourceID: "d3"}}
	data, _ := json.Marshal(items)
	if err := os.WriteFile(qaPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out2, err := runCLIArgs(t, root, "eval", "--estimate", "--json")
	if err != nil {
		t.Fatalf("cached eval --estimate: %v", err)
	}
	var rep2 EvalEstimateReport
	if err := json.Unmarshal(out2, &rep2); err != nil {
		t.Fatalf("decode cached estimate: %v\noutput: %s", err, out2)
	}
	if !rep2.QACached || rep2.GenerationUSD != 0 || rep2.N != 3 {
		t.Errorf("cached estimate must zero generation and clamp n to the cache: %+v", rep2)
	}

	out3, err := runCLIArgs(t, root, "eval", "answers", "--estimate", "--json")
	if err != nil {
		t.Fatalf("eval answers --estimate: %v", err)
	}
	var rep3 EvalEstimateReport
	if err := json.Unmarshal(out3, &rep3); err != nil {
		t.Fatalf("decode answers estimate: %v\noutput: %s", err, out3)
	}
	if rep3.Command != "answers" || rep3.AnswersUSD < 0 {
		t.Errorf("answers estimate looks wrong: %+v", rep3)
	}

	out4, err := runCLIArgs(t, root, "eval", "tune", "--estimate", "--json")
	if err != nil {
		t.Fatalf("eval tune --estimate: %v", err)
	}
	var rep4 EvalEstimateReport
	if err := json.Unmarshal(out4, &rep4); err != nil {
		t.Fatalf("decode tune estimate: %v\noutput: %s", err, out4)
	}
	if rep4.Command != "tune" || rep4.AnswersUSD != 0 {
		t.Errorf("tune estimate looks wrong: %+v", rep4)
	}
}

// TestEvalCostGate covers the cost math + the --cost-cap abort without a live
// catalog: a pricey generation model must cross a tight cap.
func TestEvalCostGate(t *testing.T) {
	genM := ai.ModelInfo{ID: "g", Provider: "bedrock", PriceIn: 10, PriceOut: 30} // $/M tokens
	embM := ai.ModelInfo{ID: "e", Provider: "bedrock", PriceIn: 0.1}
	total, gen, emb := estimateEvalCostUSD(genM, embM, 20)
	if gen <= 0 || emb < 0 || total < gen {
		t.Fatalf("cost math looks wrong: total=%.5f gen=%.5f emb=%.5f", total, gen, emb)
	}
	if err := evalCostGate(total, 1.0); err != nil {
		t.Errorf("under a $1.00 cap the ~$0.35 estimate should pass, got: %v", err)
	}
	if err := evalCostGate(total, 0.10); err == nil {
		t.Errorf("over a $0.10 cap the ~$%.4f estimate should abort", total)
	}
}
