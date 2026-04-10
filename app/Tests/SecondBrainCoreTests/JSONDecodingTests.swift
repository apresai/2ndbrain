import Testing
import Foundation

// These structs mirror the Codable types in AppState.swift.
// They test the JSON contract with the Go CLI — if Go changes a field name,
// these tests break immediately instead of failing silently at runtime.

// MARK: - Mirror Types

private struct AIStatusInfo: Codable {
    let provider: String
    let embeddingModel: String
    let genModel: String
    let dimensions: Int
    let embedAvailable: Bool
    let genAvailable: Bool
    let embeddingCount: Int
    let documentCount: Int

    enum CodingKeys: String, CodingKey {
        case provider
        case embeddingModel = "embedding_model"
        case genModel = "generation_model"
        case dimensions
        case embedAvailable = "embed_available"
        case genAvailable = "gen_available"
        case embeddingCount = "embedding_count"
        case documentCount = "document_count"
    }
}

private struct AIAskResult: Codable {
    let answer: String
    let sources: [String]
}

private struct CLISearchResult: Codable {
    let docID: String
    let path: String
    let title: String
    let docType: String?
    let headingPath: String?
    let score: Double
    let status: String?

    enum CodingKeys: String, CodingKey {
        case docID = "doc_id"
        case path, title
        case docType = "type"
        case headingPath = "heading_path"
        case score, status
    }
}

private struct LintIssue: Codable {
    let path: String
    let line: Int?
    let level: String
    let message: String
}

private struct LintReport: Codable {
    let issues: [LintIssue]
    let filesChecked: Int
    let errors: Int
    let warnings: Int

    enum CodingKeys: String, CodingKey {
        case issues, errors, warnings
        case filesChecked = "files_checked"
    }
}

private struct AIProbeResult: Codable {
    let modelID: String
    let provider: String
    let modelType: String
    let ok: Bool
    let detail: String
    let latency: String

    enum CodingKeys: String, CodingKey {
        case modelID = "model_id"
        case provider
        case modelType = "type"
        case ok, detail, latency
    }
}

private struct CatalogModelInfo: Codable {
    let modelID: String
    let name: String
    let provider: String
    let modelType: String
    let dimensions: Int?
    let priceIn: Double?
    let priceOut: Double?
    let contextLen: Int?

    enum CodingKeys: String, CodingKey {
        case modelID = "id"
        case name, provider
        case modelType = "type"
        case dimensions
        case priceIn = "price_input_per_million"
        case priceOut = "price_output_per_million"
        case contextLen = "context_length"
    }
}

private struct OllamaReadiness: Codable {
    let installed: Bool
    let running: Bool
}

private struct OllamaReport: Codable {
    let ollama: OllamaReadiness
}

// MARK: - AIStatusInfo Tests

@Test("AIStatusInfo decodes all fields from Go CLI JSON")
func aiStatusFullDecode() throws {
    let json = """
    {
        "provider": "bedrock",
        "embedding_model": "amazon.nova-2-multimodal-embeddings-v1:0",
        "generation_model": "amazon.nova-micro-v1:0",
        "dimensions": 1024,
        "embed_available": true,
        "gen_available": true,
        "embedding_count": 42,
        "document_count": 100
    }
    """
    let status = try JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
    #expect(status.provider == "bedrock")
    #expect(status.embeddingModel == "amazon.nova-2-multimodal-embeddings-v1:0")
    #expect(status.genModel == "amazon.nova-micro-v1:0")
    #expect(status.dimensions == 1024)
    #expect(status.embedAvailable == true)
    #expect(status.genAvailable == true)
    #expect(status.embeddingCount == 42)
    #expect(status.documentCount == 100)
}

@Test("AIStatusInfo decodes when AI is not configured")
func aiStatusUnconfigured() throws {
    let json = """
    {
        "provider": "",
        "embedding_model": "",
        "generation_model": "",
        "dimensions": 0,
        "embed_available": false,
        "gen_available": false,
        "embedding_count": 0,
        "document_count": 5
    }
    """
    let status = try JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
    #expect(status.provider == "")
    #expect(status.embedAvailable == false)
    #expect(status.documentCount == 5)
}

// MARK: - LintReport Tests

@Test("LintReport decodes with errors and warnings")
func lintReportWithIssues() throws {
    let json = """
    {
        "issues": [
            {"path": "adr/use-jwt.md", "level": "error", "message": "missing 'id' in frontmatter"},
            {"path": "notes/draft.md", "line": 15, "level": "warning", "message": "broken wikilink: [[nonexistent]]"}
        ],
        "files_checked": 12,
        "errors": 1,
        "warnings": 1
    }
    """
    let report = try JSONDecoder().decode(LintReport.self, from: Data(json.utf8))
    #expect(report.filesChecked == 12)
    #expect(report.errors == 1)
    #expect(report.warnings == 1)
    #expect(report.issues.count == 2)
    #expect(report.issues[0].path == "adr/use-jwt.md")
    #expect(report.issues[0].level == "error")
    #expect(report.issues[0].line == nil)
    #expect(report.issues[1].line == 15)
    #expect(report.issues[1].level == "warning")
}

@Test("LintReport decodes clean vault with no issues")
func lintReportClean() throws {
    let json = """
    {"issues": [], "files_checked": 8, "errors": 0, "warnings": 0}
    """
    let report = try JSONDecoder().decode(LintReport.self, from: Data(json.utf8))
    #expect(report.issues.isEmpty)
    #expect(report.filesChecked == 8)
    #expect(report.errors == 0)
}

// MARK: - AIProbeResult Tests

@Test("AIProbeResult decodes successful probe")
func probeResultSuccess() throws {
    let json = """
    {
        "model_id": "amazon.nova-micro-v1:0",
        "provider": "bedrock",
        "type": "generation",
        "ok": true,
        "detail": "4",
        "latency": "891ms"
    }
    """
    let result = try JSONDecoder().decode(AIProbeResult.self, from: Data(json.utf8))
    #expect(result.modelID == "amazon.nova-micro-v1:0")
    #expect(result.provider == "bedrock")
    #expect(result.modelType == "generation")
    #expect(result.ok == true)
    #expect(result.latency == "891ms")
}

@Test("AIProbeResult decodes failed probe")
func probeResultFailure() throws {
    let json = """
    {
        "model_id": "nomic-embed-text",
        "provider": "ollama",
        "type": "embedding",
        "ok": false,
        "detail": "connection refused",
        "latency": "0s"
    }
    """
    let result = try JSONDecoder().decode(AIProbeResult.self, from: Data(json.utf8))
    #expect(result.ok == false)
    #expect(result.detail == "connection refused")
}

// MARK: - CatalogModelInfo Tests

@Test("CatalogModelInfo decodes embedding model with pricing")
func catalogEmbeddingModel() throws {
    let json = """
    {
        "id": "nvidia/llama-nemotron-embed-vl-1b-v2:free",
        "name": "Nemotron Embed VL 1B v2",
        "provider": "openrouter",
        "type": "embedding",
        "dimensions": 1024,
        "price_input_per_million": 0,
        "price_output_per_million": 0,
        "context_length": 32768
    }
    """
    let model = try JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
    #expect(model.modelID == "nvidia/llama-nemotron-embed-vl-1b-v2:free")
    #expect(model.modelType == "embedding")
    #expect(model.dimensions == 1024)
    #expect(model.priceIn == 0)
    #expect(model.contextLen == 32768)
}

@Test("CatalogModelInfo decodes generation model with optional fields missing")
func catalogGenerationModel() throws {
    let json = """
    {
        "id": "qwen2.5:0.5b",
        "name": "Qwen 2.5 0.5B",
        "provider": "ollama",
        "type": "generation"
    }
    """
    let model = try JSONDecoder().decode(CatalogModelInfo.self, from: Data(json.utf8))
    #expect(model.modelID == "qwen2.5:0.5b")
    #expect(model.provider == "ollama")
    #expect(model.dimensions == nil)
    #expect(model.priceIn == nil)
    #expect(model.contextLen == nil)
}

// MARK: - CLISearchResult Tests

@Test("CLISearchResult decodes with all fields")
func searchResultFull() throws {
    let json = """
    {
        "doc_id": "abc-123",
        "path": "adr/use-jwt.md",
        "title": "Use JWT for Authentication",
        "type": "adr",
        "heading_path": "Decision",
        "score": 0.85,
        "status": "accepted"
    }
    """
    let result = try JSONDecoder().decode(CLISearchResult.self, from: Data(json.utf8))
    #expect(result.docID == "abc-123")
    #expect(result.docType == "adr")
    #expect(result.headingPath == "Decision")
    #expect(result.score == 0.85)
    #expect(result.status == "accepted")
}

@Test("CLISearchResult decodes with optional fields null")
func searchResultMinimal() throws {
    let json = """
    {
        "doc_id": "xyz-789",
        "path": "notes/test.md",
        "title": "Test Note",
        "score": 0.5
    }
    """
    let result = try JSONDecoder().decode(CLISearchResult.self, from: Data(json.utf8))
    #expect(result.docType == nil)
    #expect(result.headingPath == nil)
    #expect(result.status == nil)
}

// MARK: - AIAskResult Tests

@Test("AIAskResult decodes with sources")
func askResultWithSources() throws {
    let json = """
    {"answer": "We chose JWT because...", "sources": ["adr/use-jwt.md", "notes/auth.md"]}
    """
    let result = try JSONDecoder().decode(AIAskResult.self, from: Data(json.utf8))
    #expect(result.answer.contains("JWT"))
    #expect(result.sources.count == 2)
}

@Test("AIAskResult decodes with empty sources")
func askResultNoSources() throws {
    let json = """
    {"answer": "I don't have enough information.", "sources": []}
    """
    let result = try JSONDecoder().decode(AIAskResult.self, from: Data(json.utf8))
    #expect(result.sources.isEmpty)
}

// MARK: - OllamaReadiness Tests

@Test("OllamaReport decodes nested ollama status")
func ollamaReportReady() throws {
    let json = """
    {
        "ollama": {"installed": true, "running": true},
        "disk": {},
        "memory": {},
        "overall": "ready"
    }
    """
    let report = try JSONDecoder().decode(OllamaReport.self, from: Data(json.utf8))
    #expect(report.ollama.installed == true)
    #expect(report.ollama.running == true)
}

@Test("OllamaReport decodes when not installed")
func ollamaReportNotInstalled() throws {
    let json = """
    {
        "ollama": {"installed": false, "running": false, "endpoint": ""},
        "disk": {},
        "memory": {},
        "overall": "action_needed"
    }
    """
    let report = try JSONDecoder().decode(OllamaReport.self, from: Data(json.utf8))
    #expect(report.ollama.installed == false)
    #expect(report.ollama.running == false)
}

// MARK: - Array Decoding (models list returns array)

@Test("Models list decodes as array of CatalogModelInfo")
func modelsListArray() throws {
    let json = """
    [
        {"id": "nova-embed", "name": "Nova Embed", "provider": "bedrock", "type": "embedding", "dimensions": 1024},
        {"id": "nova-micro", "name": "Nova Micro", "provider": "bedrock", "type": "generation"}
    ]
    """
    let models = try JSONDecoder().decode([CatalogModelInfo].self, from: Data(json.utf8))
    #expect(models.count == 2)
    #expect(models[0].modelType == "embedding")
    #expect(models[1].modelType == "generation")
}
