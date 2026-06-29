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

// TestNova2CatalogTruth pins the flagship's declared capabilities so the catalog
// can't silently drift from Nova-2's real spec (the catalog-truth-fix from #108).
func TestNova2CatalogTruth(t *testing.T) {
	var nova *ModelInfo
	for _, m := range BuiltinCatalog() {
		if m.ID == "amazon.nova-2-multimodal-embeddings-v1:0" {
			mm := m
			nova = &mm
			break
		}
	}
	if nova == nil {
		t.Fatal("nova-2 missing from builtin catalog")
	}
	if nova.ContextLen != 8192 {
		t.Errorf("ContextLen = %d, want 8192", nova.ContextLen)
	}
	if nova.Dimensions != 1024 {
		t.Errorf("default Dimensions = %d, want 1024", nova.Dimensions)
	}
	wantDims := []int{256, 384, 1024, 3072}
	if len(nova.SupportedDimensions) != len(wantDims) {
		t.Fatalf("SupportedDimensions = %v, want %v", nova.SupportedDimensions, wantDims)
	}
	for i := range wantDims {
		if nova.SupportedDimensions[i] != wantDims[i] {
			t.Errorf("SupportedDimensions = %v, want %v", nova.SupportedDimensions, wantDims)
		}
	}
	wantMod := map[string]bool{"text": true, "image": true, "video": true, "audio": true}
	if len(nova.Modalities) != len(wantMod) {
		t.Errorf("Modalities = %v, want text/image/video/audio", nova.Modalities)
	}
	for _, mod := range nova.Modalities {
		if !wantMod[mod] {
			t.Errorf("unexpected modality %q", mod)
		}
	}
	if nova.RecommendedSimilarityThreshold != 0.25 {
		t.Errorf("threshold = %g, want 0.25 (asymmetric purpose)", nova.RecommendedSimilarityThreshold)
	}
}
