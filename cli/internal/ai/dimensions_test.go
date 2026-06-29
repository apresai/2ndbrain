package ai

import "testing"

func TestSupportedDimensionsFor(t *testing.T) {
	nova := SupportedDimensionsFor("", "bedrock", "amazon.nova-2-multimodal-embeddings-v1:0")
	want := []int{256, 384, 1024, 3072}
	if len(nova) != len(want) {
		t.Fatalf("nova-2 supported = %v, want %v", nova, want)
	}
	for i := range want {
		if nova[i] != want[i] {
			t.Fatalf("nova-2 supported = %v, want %v", nova, want)
		}
	}
	// A model with no declared Matryoshka set -> nil (callers treat as "no constraint").
	if got := SupportedDimensionsFor("", "bedrock", "amazon.titan-embed-text-v2:0"); got != nil {
		t.Errorf("titan supported = %v, want nil (no constraint)", got)
	}
	if got := SupportedDimensionsFor("", "bedrock", "nonexistent-model"); got != nil {
		t.Errorf("unknown model = %v, want nil", got)
	}
	if got := SupportedDimensionsFor("", "", ""); got != nil {
		t.Errorf("empty provider/model = %v, want nil", got)
	}
}
