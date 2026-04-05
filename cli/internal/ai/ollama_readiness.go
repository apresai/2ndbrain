package ai

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// OllamaStatus describes Ollama's installation and runtime state.
type OllamaStatus struct {
	Installed  bool   `json:"installed"`
	BinaryPath string `json:"binary_path,omitempty"`
	Running    bool   `json:"running"`
	Endpoint   string `json:"endpoint"`
}

// LocalModelStatus describes the readiness of a required model.
type LocalModelStatus struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Dimensions    int    `json:"dimensions,omitempty"`
	Pulled        bool   `json:"pulled"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	SizeHuman     string `json:"size_human,omitempty"`
	DownloadBytes int64  `json:"download_bytes,omitempty"`
	DownloadHuman string `json:"download_human,omitempty"`
	ParameterSize string `json:"parameter_size,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	Family        string `json:"family,omitempty"`
	RAMBytes      int64  `json:"ram_bytes"`
	RAMHuman      string `json:"ram_human"`
}

// EmbeddingStatus tracks indexing progress.
type EmbeddingStatus struct {
	Indexed      int `json:"indexed"`
	Total        int `json:"total"`
	NeedIndexing int `json:"need_indexing"`
}

// UpgradeRec suggests a better model when resources allow.
type UpgradeRec struct {
	Model         string `json:"model"`
	Reason        string `json:"reason"`
	DownloadHuman string `json:"download_human"`
}

// ReadinessReport is the full local AI readiness output.
type ReadinessReport struct {
	Ollama      OllamaStatus      `json:"ollama"`
	Disk        DiskInfo           `json:"disk"`
	Memory      MemInfo            `json:"memory"`
	EmbedModel  *LocalModelStatus  `json:"embedding_model"`
	GenModel    *LocalModelStatus  `json:"generation_model"`
	RAMVerdict  string             `json:"ram_verdict"`
	Embeddings  *EmbeddingStatus   `json:"embeddings,omitempty"`
	Upgrade     *UpgradeRec        `json:"upgrade_recommendation,omitempty"`
	Overall     string             `json:"overall"`
	Actions     []string           `json:"actions,omitempty"`
}

// Default recommended models.
const (
	DefaultLocalEmbedModel = "embeddinggemma"
	DefaultLocalGenModel   = "qwen2.5:0.5b"
	UpgradeGenModel        = "qwen3:30b-a3b"
	UpgradeDiskThreshold   = 100 * 1024 * 1024 * 1024 // 100 GB
)

// knownDownloadSizes maps model names to approximate download sizes in bytes.
var knownDownloadSizes = map[string]int64{
	"embeddinggemma":      278 * 1024 * 1024,
	"nomic-embed-text":    261 * 1024 * 1024,
	"all-minilm":          23 * 1024 * 1024,
	"mxbai-embed-large":   638 * 1024 * 1024,
	"qwen2.5:0.5b":        375 * 1024 * 1024,
	"qwen2.5:1.5b":        935 * 1024 * 1024,
	"gemma3:1b":            815 * 1024 * 1024,
	"gemma3:4b":           3100 * 1024 * 1024,
	"qwen3:30b-a3b":      18 * 1024 * 1024 * 1024,
}

// knownRAMEstimates maps model names to approximate RAM needed in bytes.
var knownRAMEstimates = map[string]int64{
	"embeddinggemma":      400 * 1024 * 1024,
	"nomic-embed-text":    350 * 1024 * 1024,
	"all-minilm":          100 * 1024 * 1024,
	"mxbai-embed-large":   800 * 1024 * 1024,
	"qwen2.5:0.5b":        500 * 1024 * 1024,
	"qwen2.5:1.5b":       1200 * 1024 * 1024,
	"gemma3:1b":           1000 * 1024 * 1024,
	"gemma3:4b":          3200 * 1024 * 1024,
	"qwen3:30b-a3b":      6 * 1024 * 1024 * 1024,
}

// CheckOllamaStatus checks if Ollama is installed and running.
func CheckOllamaStatus(ctx context.Context, endpoint string) OllamaStatus {
	status := OllamaStatus{Endpoint: endpoint}

	if path, err := exec.LookPath("ollama"); err == nil {
		status.Installed = true
		status.BinaryPath = path
	}

	client := &http.Client{Timeout: 3 * time.Second}
	status.Running = ollamaHeartbeat(ctx, client, endpoint)

	return status
}

// BuildReadinessReport assembles the full readiness report.
func BuildReadinessReport(ctx context.Context, cfg AIConfig, endpoint, vaultRoot string, embedCount, docCount, staleCount int) (*ReadinessReport, error) {
	report := &ReadinessReport{}

	// Ollama status
	report.Ollama = CheckOllamaStatus(ctx, endpoint)

	// Disk info
	diskPath := vaultRoot
	if diskPath == "" {
		diskPath = "/"
	}
	disk, err := GetDiskInfo(diskPath)
	if err != nil {
		disk = DiskInfo{Path: diskPath}
	}
	report.Disk = disk

	// Memory info
	mem, err := GetMemInfo()
	if err != nil {
		mem = MemInfo{}
	}
	report.Memory = mem

	// Determine required models
	embedModelName := cfg.EmbeddingModel
	if embedModelName == "" {
		embedModelName = DefaultLocalEmbedModel
	}
	genModelName := cfg.GenerationModel
	if genModelName == "" {
		genModelName = DefaultLocalGenModel
	}

	// Check installed models
	var installed map[string]OllamaModelEntry
	if report.Ollama.Running {
		client := &http.Client{Timeout: 5 * time.Second}
		installed, _ = ListOllamaModelEntries(ctx, client, endpoint)
	}

	report.EmbedModel = buildModelStatus(embedModelName, "embedding", cfg.Dimensions, installed)
	report.GenModel = buildModelStatus(genModelName, "generation", 0, installed)

	// RAM verdict
	totalRAMNeeded := report.EmbedModel.RAMBytes + report.GenModel.RAMBytes
	headroom := int64(4 * 1024 * 1024 * 1024) // 4 GB for OS + apps
	if mem.TotalBytes > 0 && int64(mem.TotalBytes) >= totalRAMNeeded+headroom {
		report.RAMVerdict = "sufficient for both models"
	} else if mem.TotalBytes > 0 {
		report.RAMVerdict = fmt.Sprintf("tight — models need ~%s, system has %s",
			HumanBytes(uint64(totalRAMNeeded)), mem.TotalHuman)
	} else {
		report.RAMVerdict = "unknown (could not read system memory)"
	}

	// Embeddings (only if vault provided)
	if docCount > 0 || embedCount > 0 {
		report.Embeddings = &EmbeddingStatus{
			Indexed:      embedCount,
			Total:        docCount,
			NeedIndexing: staleCount,
		}
	}

	// Build actions
	if !report.Ollama.Installed {
		report.Actions = append(report.Actions, "Install Ollama: brew install ollama")
	} else if !report.Ollama.Running {
		report.Actions = append(report.Actions, "Start Ollama as a service: brew services start ollama")
	}

	if report.Ollama.Running {
		if !report.EmbedModel.Pulled {
			needed := report.EmbedModel.DownloadBytes
			if needed > 0 && disk.AvailBytes > 0 && uint64(needed) > disk.AvailBytes {
				report.Actions = append(report.Actions,
					fmt.Sprintf("Free up disk space — need %s for %s, have %s available",
						report.EmbedModel.DownloadHuman, embedModelName, disk.AvailHuman))
			} else {
				report.Actions = append(report.Actions,
					fmt.Sprintf("Pull embedding model: ollama pull %s", embedModelName))
			}
		}
		if !report.GenModel.Pulled {
			needed := report.GenModel.DownloadBytes
			if needed > 0 && disk.AvailBytes > 0 && uint64(needed) > disk.AvailBytes {
				report.Actions = append(report.Actions,
					fmt.Sprintf("Free up disk space — need %s for %s, have %s available",
						report.GenModel.DownloadHuman, genModelName, disk.AvailHuman))
			} else {
				report.Actions = append(report.Actions,
					fmt.Sprintf("Pull generation model: ollama pull %s", genModelName))
			}
		}
	}

	if report.Embeddings != nil && report.Embeddings.NeedIndexing > 0 {
		report.Actions = append(report.Actions,
			fmt.Sprintf("Run `2nb index` to embed %d remaining documents", report.Embeddings.NeedIndexing))
	}

	// Upgrade recommendation
	if disk.AvailBytes >= UpgradeDiskThreshold {
		upgradeInstalled := false
		if installed != nil {
			if _, ok := installed[UpgradeGenModel]; ok {
				upgradeInstalled = true
			}
		}
		if !upgradeInstalled && genModelName != UpgradeGenModel {
			dlSize := knownDownloadSizes[UpgradeGenModel]
			report.Upgrade = &UpgradeRec{
				Model:         UpgradeGenModel,
				Reason:        fmt.Sprintf("You have %s free — upgrade for much better answers", disk.AvailHuman),
				DownloadHuman: HumanBytes(uint64(dlSize)),
			}
		}
	}

	// Overall
	if len(report.Actions) == 0 {
		report.Overall = "ready"
	} else {
		report.Overall = "action_needed"
	}

	return report, nil
}

func buildModelStatus(name, modelType string, dims int, installed map[string]OllamaModelEntry) *LocalModelStatus {
	ms := &LocalModelStatus{
		Name: name,
		Type: modelType,
	}

	if modelType == "embedding" {
		if dims > 0 {
			ms.Dimensions = dims
		} else {
			ms.Dimensions = lookupEmbeddingDims(name)
		}
	}

	// Check if pulled
	if installed != nil {
		if entry, ok := findModel(name, installed); ok {
			ms.Pulled = true
			ms.SizeBytes = entry.Size
			ms.SizeHuman = HumanBytes(uint64(entry.Size))
			ms.ParameterSize = entry.Details.ParameterSize
			ms.Quantization = entry.Details.QuantizationLevel
			ms.Family = entry.Details.Family
		}
	}

	// RAM estimate
	if ram, ok := knownRAMEstimates[stripTag(name)]; ok {
		ms.RAMBytes = ram
	} else if ms.ParameterSize != "" {
		ms.RAMBytes = EstimateModelRAM(ms.ParameterSize, ms.Quantization)
	}
	ms.RAMHuman = "~" + HumanBytes(uint64(ms.RAMBytes))

	// Download size for unpulled models
	if !ms.Pulled {
		if dl, ok := knownDownloadSizes[stripTag(name)]; ok {
			ms.DownloadBytes = dl
			ms.DownloadHuman = "~" + HumanBytes(uint64(dl))
		} else if dl, ok := knownDownloadSizes[name]; ok {
			ms.DownloadBytes = dl
			ms.DownloadHuman = "~" + HumanBytes(uint64(dl))
		} else {
			ms.DownloadHuman = "unknown"
		}
	}

	return ms
}

// findModel looks up a model name in the installed map, handling tag variations.
func findModel(name string, installed map[string]OllamaModelEntry) (OllamaModelEntry, bool) {
	if entry, ok := installed[name]; ok {
		return entry, true
	}
	// Try with :latest tag
	if !strings.Contains(name, ":") {
		if entry, ok := installed[name+":latest"]; ok {
			return entry, true
		}
	}
	// Try prefix match
	base := stripTag(name)
	for k, entry := range installed {
		if stripTag(k) == base {
			return entry, true
		}
	}
	return OllamaModelEntry{}, false
}

func stripTag(name string) string {
	if idx := strings.Index(name, ":"); idx > 0 {
		return name[:idx]
	}
	return name
}

// EstimateModelRAM estimates RAM needed from parameter size string and quantization.
func EstimateModelRAM(paramSize, quantization string) int64 {
	params := parseParameterSize(paramSize)
	if params == 0 {
		return 0
	}

	bytesPerParam := 0.6 // default Q4_K_M
	switch strings.ToUpper(quantization) {
	case "Q4_0", "Q4_K_S":
		bytesPerParam = 0.5
	case "Q4_K_M":
		bytesPerParam = 0.6
	case "Q5_K_M", "Q5_K_S":
		bytesPerParam = 0.7
	case "Q8_0":
		bytesPerParam = 1.0
	case "F16", "FP16":
		bytesPerParam = 2.0
	}

	overhead := int64(500 * 1024 * 1024) // 500 MB for KV cache + runtime
	return int64(float64(params)*bytesPerParam) + overhead
}

// parseParameterSize parses strings like "4.0B", "0.5B", "494.03M" into a count.
func parseParameterSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	var multiplier float64
	switch {
	case strings.HasSuffix(s, "B"):
		multiplier = 1e9
		s = strings.TrimSuffix(s, "B")
	case strings.HasSuffix(s, "M"):
		multiplier = 1e6
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		multiplier = 1e3
		s = strings.TrimSuffix(s, "K")
	default:
		return 0
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * multiplier))
}
