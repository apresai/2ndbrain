package ai

import (
	"regexp"
	"sort"
	"strings"
)

// VendorInfo carries the derived vendor identity for a model. The GUI
// uses this to group the catalog under disclosure headers.
//
// Vendor is a stable machine key ("anthropic", "amazon", "meta", …)
// suitable for filtering / bulk toggle. Display is the human label
// the UI shows ("Anthropic", "Amazon", "Meta"). Family names the
// model series inside a vendor ("Claude", "Nova", "Llama") — not
// always reliable but useful for sub-grouping.
type VendorInfo struct {
	Vendor  string
	Display string
	Family  string
}

// VendorOf returns the vendor identity for a model. Routing rules:
//   - bedrock: strip geo inference-profile prefix (us./eu./ap./global.),
//     then first dotted segment is the vendor ("anthropic.claude-…"
//     → "anthropic"). Family is the second segment's leading family
//     name ("claude-haiku-4-5" → "Claude").
//   - openrouter: vendor is the part before the slash
//     ("anthropic/claude-haiku-4-5" → "anthropic"). Family is the
//     model base before the first dash or version separator.
//   - ollama: vendor is inferred from known model families
//     (llama* → Meta, gemma* → Google, nomic-* → Nomic, etc.).
//     Unknown falls back to "community".
//
// Unrecognized providers return ("other", "Other", "") so downstream
// grouping still has something to key on.
func VendorOf(modelID, provider string) VendorInfo {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	switch provider {
	case "bedrock":
		return vendorForBedrock(lower)
	case "openrouter":
		return vendorForOpenRouter(lower)
	case "ollama":
		return vendorForOllama(lower)
	}
	return VendorInfo{Vendor: "other", Display: "Other"}
}

func vendorForBedrock(lower string) VendorInfo {
	// Strip geo inference profile prefix if present.
	stripped := inferenceProfileBaseID(lower)
	parts := strings.SplitN(stripped, ".", 3)
	if len(parts) < 2 {
		return VendorInfo{Vendor: "other", Display: "Other"}
	}
	vendor := parts[0]
	display := bedrockDisplayName(vendor)
	family := ""
	if len(parts) >= 2 {
		// "claude-haiku-4-5-…" → "Claude"
		family = bedrockFamilyFromTail(vendor, parts[1])
	}
	return VendorInfo{Vendor: vendor, Display: display, Family: family}
}

// bedrockVendorDisplay maps the Bedrock vendor slugs 2nb recognizes to their
// display names. It doubles as the static vendor vocabulary vendor policies
// accept (KnownVendorSlugs in vendor_policy.go), so an enable-only policy can
// name a vendor whose models are not yet in the merged catalog; they arrive
// later via discovery, pre-verdicted by the policy.
var bedrockVendorDisplay = map[string]string{
	"anthropic":  "Anthropic",
	"amazon":     "Amazon",
	"meta":       "Meta",
	"mistral":    "Mistral",
	"cohere":     "Cohere",
	"ai21":       "AI21",
	"deepseek":   "DeepSeek",
	"moonshot":   "Moonshot",
	"moonshotai": "Moonshot",
	"qwen":       "Qwen",
	"zai":        "Z.ai",
	"writer":     "Writer",
	"minimax":    "MiniMax",
	"nvidia":     "NVIDIA",
	"openai":     "OpenAI",
	"twelvelabs": "TwelveLabs",
	"google":     "Google",
	"stability":  "Stability AI",
}

func bedrockDisplayName(vendor string) string {
	if d, ok := bedrockVendorDisplay[vendor]; ok {
		return d
	}
	return strings.Title(vendor) //nolint:staticcheck // Title is fine for ASCII vendor slugs
}

func bedrockFamilyFromTail(vendor, tail string) string {
	// tail examples: "claude-haiku-4-5-20251001-v1:0", "nova-lite-v1:0",
	// "llama3-1-70b-instruct-v1:0", "titan-embed-text-v2:0".
	first := strings.SplitN(tail, "-", 2)[0]
	switch vendor {
	case "anthropic":
		return "Claude"
	case "amazon":
		switch {
		case strings.HasPrefix(tail, "titan-embed"):
			return "Titan Embed"
		case strings.HasPrefix(tail, "titan"):
			return "Titan"
		case strings.HasPrefix(tail, "nova-2-multimodal-embeddings"):
			return "Nova Embed"
		case strings.HasPrefix(tail, "nova"):
			return "Nova"
		}
	case "meta":
		if strings.HasPrefix(first, "llama") {
			return "Llama"
		}
	case "mistral":
		if strings.HasPrefix(first, "mixtral") {
			return "Mixtral"
		}
		if strings.HasPrefix(first, "pixtral") {
			return "Pixtral"
		}
		if strings.HasPrefix(first, "magistral") {
			return "Magistral"
		}
		if strings.HasPrefix(first, "devstral") {
			return "Devstral"
		}
		if strings.HasPrefix(first, "voxtral") {
			return "Voxtral"
		}
		if strings.HasPrefix(first, "ministral") {
			return "Ministral"
		}
		return "Mistral"
	case "cohere":
		if strings.HasPrefix(first, "embed") {
			return "Embed"
		}
		if strings.HasPrefix(first, "command") {
			return "Command"
		}
		if strings.HasPrefix(first, "rerank") {
			return "Rerank"
		}
	case "google":
		if strings.HasPrefix(first, "gemma") {
			return "Gemma"
		}
	case "twelvelabs":
		if strings.HasPrefix(first, "marengo") {
			return "Marengo"
		}
	case "nvidia":
		if strings.HasPrefix(first, "nemotron") {
			return "Nemotron"
		}
	case "openai":
		if strings.HasPrefix(first, "gpt-oss") {
			return "GPT-OSS"
		}
	case "qwen":
		if strings.HasPrefix(first, "qwen") {
			return "Qwen"
		}
	case "zai":
		if strings.HasPrefix(first, "glm") {
			return "GLM"
		}
	case "moonshot", "moonshotai":
		if strings.HasPrefix(first, "kimi") {
			return "Kimi"
		}
	case "minimax":
		return "MiniMax"
	case "writer":
		if strings.HasPrefix(first, "palmyra") {
			return "Palmyra"
		}
	case "ai21":
		if strings.HasPrefix(first, "jamba") {
			return "Jamba"
		}
	case "deepseek":
		return "DeepSeek"
	}
	// Unknown vendor → family stays empty rather than guessing. The
	// GUI falls back to grouping by vendor only.
	return ""
}

func vendorForOpenRouter(lower string) VendorInfo {
	slash := strings.Index(lower, "/")
	if slash <= 0 {
		return VendorInfo{Vendor: "other", Display: "Other"}
	}
	vendor := lower[:slash]
	return VendorInfo{
		Vendor:  vendor,
		Display: openrouterDisplayName(vendor),
		Family:  "",
	}
}

func openrouterDisplayName(vendor string) string {
	// Reuse Bedrock's map where vendor slugs overlap; fall through to
	// Title-case for anything exotic OpenRouter ships.
	if d := bedrockDisplayName(vendor); d != strings.Title(vendor) {
		return d
	}
	return strings.Title(vendor)
}

func vendorForOllama(lower string) VendorInfo {
	base := strings.SplitN(lower, ":", 2)[0] // strip tag (qwen3:30b-a3b → qwen3)
	switch {
	case strings.HasPrefix(base, "llama"):
		return VendorInfo{Vendor: "meta", Display: "Meta", Family: "Llama"}
	case strings.HasPrefix(base, "gemma"):
		return VendorInfo{Vendor: "google", Display: "Google", Family: "Gemma"}
	case strings.HasPrefix(base, "qwen"):
		return VendorInfo{Vendor: "qwen", Display: "Qwen", Family: "Qwen"}
	case strings.HasPrefix(base, "mistral"), strings.HasPrefix(base, "mixtral"):
		return VendorInfo{Vendor: "mistral", Display: "Mistral", Family: "Mistral"}
	case strings.HasPrefix(base, "phi"):
		return VendorInfo{Vendor: "microsoft", Display: "Microsoft", Family: "Phi"}
	case strings.HasPrefix(base, "deepseek"):
		return VendorInfo{Vendor: "deepseek", Display: "DeepSeek", Family: "DeepSeek"}
	case strings.HasPrefix(base, "nomic-"):
		return VendorInfo{Vendor: "nomic", Display: "Nomic", Family: "Nomic Embed"}
	case strings.HasPrefix(base, "mxbai-"):
		return VendorInfo{Vendor: "mixedbread", Display: "Mixedbread", Family: "mxbai"}
	case strings.HasPrefix(base, "snowflake-"):
		return VendorInfo{Vendor: "snowflake", Display: "Snowflake", Family: "Arctic Embed"}
	case strings.HasPrefix(base, "bge-"):
		return VendorInfo{Vendor: "baai", Display: "BAAI", Family: "BGE"}
	case strings.HasPrefix(base, "all-minilm"):
		return VendorInfo{Vendor: "sentence-transformers", Display: "Sentence Transformers", Family: "MiniLM"}
	}
	return VendorInfo{Vendor: "community", Display: "Community"}
}

// VersionSortKey produces a sort key where "larger" means "newer". The
// AI Hub sorts each vendor group by this key in descending order so the
// latest model shows first.
//
// Strategy: extract every number run in the ID, concatenate them into
// a zero-padded string, then append the full lower-cased ID as a
// deterministic tiebreaker. Date-stamped IDs (20251001) naturally sort
// newest-first. For family-coded IDs (claude-haiku-4-5, nova-micro-v1),
// the numbers still order them monotonically.
//
// This is intentionally cheap — no per-vendor parsers to maintain.
// Edge cases (Opus 4.7 outranks Haiku 4.5 in the same group) don't
// matter because the Hub groups by Family first, then sorts within.
func VersionSortKey(modelID string) string {
	lower := strings.ToLower(modelID)
	// Pad each number to 8 digits so "v9" sorts before "v10" when
	// compared lexicographically.
	padded := numRun.ReplaceAllStringFunc(lower, func(match string) string {
		if len(match) >= 8 {
			return match
		}
		return strings.Repeat("0", 8-len(match)) + match
	})
	return padded + "|" + lower
}

var numRun = regexp.MustCompile(`\d+`)

// SortByVersionDesc sorts models in-place newest-first using
// VersionSortKey. Stable — models with equal keys stay in input order.
func SortByVersionDesc(models []ModelInfo) {
	sort.SliceStable(models, func(i, j int) bool {
		return VersionSortKey(models[i].ID) > VersionSortKey(models[j].ID)
	})
}
