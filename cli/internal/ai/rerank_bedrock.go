package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

// maxRerankDocsPerCall is the Bedrock Rerank per-query document cap (docs beyond
// 100 bill as additional queries). Callers over-fetch ~50, so this is a safety
// net, not the normal path.
const maxRerankDocsPerCall = 100

// BedrockReranker implements RerankProvider via the Amazon Bedrock Rerank API
// (bedrockagentruntime.Rerank) with a Cohere Rerank model. It scores raw chunk
// TEXT jointly with the query (a cross-encoder), so it is decoupled from the
// embedder — the same AWS auth/region as our Nova-2 embeddings. Cohere Rerank
// 3.5 is us-east-1 in-region only.
type BedrockReranker struct {
	client *bedrockagentruntime.Client
	ctrl   *bedrock.Client // control plane for the lightweight Available() probe
	model  string
	region string
	avail  availableCache
}

// NewBedrockReranker creates a Bedrock rerank provider. It reuses
// loadBedrockAWSConfig (SigV4 / bearer-token / region), identical to the
// embedder and generator.
func NewBedrockReranker(ctx context.Context, cfg BedrockConfig, model string) (*BedrockReranker, error) {
	awsCfg, err := loadBedrockAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &BedrockReranker{
		client: bedrockagentruntime.NewFromConfig(awsCfg),
		ctrl:   bedrock.NewFromConfig(awsCfg),
		model:  model,
		region: cfg.Region,
	}, nil
}

func (b *BedrockReranker) Name() string { return "bedrock" }

// Available uses the same cached control-plane probe the embedder uses: it
// confirms credentials + region reachability (not the specific model).
func (b *BedrockReranker) Available(ctx context.Context) bool {
	if v, hit := b.avail.get(); hit {
		return v
	}
	_, err := b.ctrl.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	ok := err == nil
	b.avail.set(ok)
	return ok
}

// modelARN builds the region-scoped foundation-model ARN the Rerank API
// requires. A caller may also pass a full ARN, which passes through.
func (b *BedrockReranker) modelARN() string {
	if strings.HasPrefix(b.model, "arn:") {
		return b.model
	}
	return fmt.Sprintf("arn:aws:bedrock:%s::foundation-model/%s", b.region, b.model)
}

// Rerank scores each (query, doc) pair with the cross-encoder and returns hits
// best-first. It retries throttling with the shared Bedrock backoff and honors
// context cancellation mid-backoff.
func (b *BedrockReranker) Rerank(ctx context.Context, query string, docs []string, topN int) ([]RerankHit, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if len(docs) > maxRerankDocsPerCall {
		docs = docs[:maxRerankDocsPerCall]
	}
	n := topN
	if n <= 0 || n > len(docs) {
		n = len(docs)
	}

	sources := make([]brtypes.RerankSource, len(docs))
	for i := range docs {
		sources[i] = brtypes.RerankSource{
			Type: brtypes.RerankSourceTypeInline,
			InlineDocumentSource: &brtypes.RerankDocument{
				Type:         brtypes.RerankDocumentTypeText,
				TextDocument: &brtypes.RerankTextDocument{Text: aws.String(docs[i])},
			},
		}
	}

	nres := int32(n)
	input := &bedrockagentruntime.RerankInput{
		Queries: []brtypes.RerankQuery{{
			Type:      brtypes.RerankQueryContentTypeText,
			TextQuery: &brtypes.RerankTextDocument{Text: aws.String(query)},
		}},
		Sources: sources,
		RerankingConfiguration: &brtypes.RerankingConfiguration{
			Type: brtypes.RerankingConfigurationTypeBedrockRerankingModel,
			BedrockRerankingConfiguration: &brtypes.BedrockRerankingConfiguration{
				NumberOfResults: &nres,
				ModelConfiguration: &brtypes.BedrockRerankingModelConfiguration{
					ModelArn: aws.String(b.modelARN()),
				},
			},
		},
	}

	var out *bedrockagentruntime.RerankOutput
	var err error
	for attempt := 1; attempt <= maxBedrockAttempts; attempt++ {
		out, err = b.client.Rerank(ctx, input)
		if err == nil {
			break
		}
		if !isBedrockRetryable(err) || attempt == maxBedrockAttempts {
			return nil, fmt.Errorf("bedrock rerank %s: %w", b.model, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(bedrockRetryDelay(attempt)):
		}
	}

	hits := make([]RerankHit, 0, len(out.Results))
	for _, r := range out.Results {
		if r.Index == nil {
			continue
		}
		idx := int(*r.Index)
		if idx < 0 || idx >= len(docs) {
			continue // defensive: never index outside the input slice
		}
		var score float64
		if r.RelevanceScore != nil {
			score = float64(*r.RelevanceScore)
		}
		hits = append(hits, RerankHit{Index: idx, Score: score})
	}
	// Bedrock returns results best-first, but sort defensively so a caller can
	// rely on descending score regardless of service ordering.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits, nil
}

var _ RerankProvider = (*BedrockReranker)(nil)
