package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Pure logic tests — no API calls

func TestEstimateModelRAM(t *testing.T) {
	tests := []struct {
		paramSize string
		quant     string
		wantMin   int64 // at least this many bytes
		wantMax   int64 // at most this many bytes
	}{
		{"4.0B", "Q4_K_M", 2_500_000_000, 3_500_000_000},
		{"0.5B", "Q4_K_M", 500_000_000, 1_000_000_000},
		{"8.0B", "Q8_0", 8_000_000_000, 10_000_000_000},
		{"7.0B", "F16", 14_000_000_000, 16_000_000_000},
		{"", "Q4_K_M", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.paramSize+"/"+tt.quant, func(t *testing.T) {
			got := EstimateModelRAM(tt.paramSize, tt.quant)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("EstimateModelRAM(%q, %q) = %d, want [%d, %d]",
					tt.paramSize, tt.quant, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestParseParameterSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"4.0B", 4_000_000_000},
		{"0.5B", 500_000_000},
		{"494.03M", 494_030_000},
		{"308M", 308_000_000},
		{"7B", 7_000_000_000},
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseParameterSize(tt.input)
			if got != tt.want {
				t.Errorf("parseParameterSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimateDownloadSize(t *testing.T) {
	// Known model
	if got := knownDownloadSizes["embeddinggemma"]; got == 0 {
		t.Error("embeddinggemma should have a known size")
	}
	if got := knownDownloadSizes["qwen2.5:0.5b"]; got == 0 {
		t.Error("qwen2.5:0.5b should have a known size")
	}
	// Unknown model
	if got := knownDownloadSizes["unknown-model-xyz"]; got != 0 {
		t.Errorf("unknown model should return 0, got %d", got)
	}
}

func TestStripTag(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"gemma3:4b", "gemma3"},
		{"embeddinggemma", "embeddinggemma"},
		{"qwen2.5:0.5b", "qwen2.5"},
		{"model:latest", "model"},
	}
	for _, tt := range tests {
		got := stripTag(tt.input)
		if got != tt.want {
			t.Errorf("stripTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Integration tests — require real Ollama

func TestCheckOllamaStatus(t *testing.T) {
	status := CheckOllamaStatus(context.Background(), "http://localhost:11434")

	// Ollama should be installed (we know it is on this machine)
	if !status.Installed {
		t.Skip("Ollama not installed")
	}
	if status.BinaryPath == "" {
		t.Error("binary path should be set when installed")
	}
	t.Logf("ollama: installed=%v running=%v path=%s", status.Installed, status.Running, status.BinaryPath)
}

func TestGetInstalledModelEntries(t *testing.T) {
	requireOllamaForReadiness(t)

	client := &http.Client{Timeout: 5 * time.Second}
	entries, err := ListOllamaModelEntries(context.Background(), client, "http://localhost:11434")
	if err != nil {
		t.Fatalf("ListOllamaModelEntries: %v", err)
	}

	t.Logf("found %d installed models", len(entries))
	for name, entry := range entries {
		t.Logf("  %s: size=%s params=%s quant=%s",
			name, HumanBytes(uint64(entry.Size)), entry.Details.ParameterSize, entry.Details.QuantizationLevel)
	}
}

func TestBuildReadinessReport(t *testing.T) {
	requireOllamaForReadiness(t)

	cfg := AIConfig{
		Provider:        "ollama",
		EmbeddingModel:  DefaultLocalEmbedModel,
		GenerationModel: DefaultLocalGenModel,
		Dimensions:      768,
	}

	report, err := BuildReadinessReport(context.Background(), cfg, "http://localhost:11434", "/tmp", 0, 0, 0)
	if err != nil {
		t.Fatalf("BuildReadinessReport: %v", err)
	}

	// Verify JSON serialization works
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty JSON output")
	}

	t.Logf("overall: %s", report.Overall)
	t.Logf("actions: %v", report.Actions)
	t.Logf("embed model pulled: %v", report.EmbedModel.Pulled)
	t.Logf("gen model pulled: %v", report.GenModel.Pulled)
	if report.Upgrade != nil {
		t.Logf("upgrade: %s (%s)", report.Upgrade.Model, report.Upgrade.Reason)
	}
}

func requireOllamaForReadiness(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skip("Ollama not running at localhost:11434")
	}
	resp.Body.Close()
}
