package ai

import "testing"

func TestResolveEmbedOptions(t *testing.T) {
	tests := []struct {
		name     string
		opts     []EmbedOption
		wantPurp string
		wantDim  int
	}{
		{"default is index", nil, PurposeIndex, 0},
		{"query purpose", []EmbedOption{WithPurpose(PurposeQuery)}, PurposeQuery, 0},
		{"dimension only keeps index default", []EmbedOption{WithDimension(384)}, PurposeIndex, 384},
		{"purpose + dimension", []EmbedOption{WithPurpose(PurposeQuery), WithDimension(256)}, PurposeQuery, 256},
		{"last purpose wins", []EmbedOption{WithPurpose(PurposeIndex), WithPurpose(PurposeQuery)}, PurposeQuery, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveEmbedOptions(tt.opts...)
			if got.Purpose != tt.wantPurp {
				t.Errorf("Purpose = %q, want %q", got.Purpose, tt.wantPurp)
			}
			if got.Dimension != tt.wantDim {
				t.Errorf("Dimension = %d, want %d", got.Dimension, tt.wantDim)
			}
		})
	}
}

func TestNovaEmbeddingPurpose(t *testing.T) {
	if got := novaEmbeddingPurpose(PurposeQuery); got != "GENERIC_RETRIEVAL" {
		t.Errorf("query purpose = %q, want GENERIC_RETRIEVAL", got)
	}
	if got := novaEmbeddingPurpose(PurposeIndex); got != "GENERIC_INDEX" {
		t.Errorf("index purpose = %q, want GENERIC_INDEX", got)
	}
	// Unknown/empty purpose defaults to index (the safe, stored-document side).
	if got := novaEmbeddingPurpose(""); got != "GENERIC_INDEX" {
		t.Errorf("empty purpose = %q, want GENERIC_INDEX", got)
	}
}

func TestIsAsymmetricEmbeddingModel(t *testing.T) {
	// This gate guards both migration-safety warnings (ai status > 0.45 and the
	// calibrate refuse-save), so a regression in the match silently disables them.
	cases := []struct {
		model string
		want  bool
	}{
		{"amazon.nova-2-multimodal-embeddings-v1:0", true},
		{"us.amazon.nova-2-multimodal-embeddings-v1:0", true}, // inference-profile prefix
		{"amazon.titan-embed-text-v2:0", false},
		{"cohere.embed-english-v3", false},
		{"nomic-embed-text", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsAsymmetricEmbeddingModel(tc.model); got != tc.want {
			t.Errorf("IsAsymmetricEmbeddingModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
