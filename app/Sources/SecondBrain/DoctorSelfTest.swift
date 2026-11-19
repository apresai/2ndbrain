import Foundation

/// Swift side of `2nb doctor --json`: the self-test payload, plus the
/// translation from the CLI's classified codes into a sentence a person can act
/// on.
///
/// The CLI already does the hard part — it distinguishes "your key was
/// rejected" from "your key is fine, this account cannot reach that model", a
/// distinction that only a real invoke can make. This layer's only job is to
/// not throw that away by collapsing both into "AI failed".

/// One check line. Mirrors the Go `DoctorCheck`, shared with config/mcp doctor.
struct SelfTestCheck: Codable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let ok: Bool
    let warn: Bool?
    let detail: String
    let fix: String?

    var isWarning: Bool { ok && (warn ?? false) }
}

/// The `selftest` block of `2nb doctor --json`.
struct SelfTestReport: Codable, Equatable {
    let ok: Bool
    let vaultBound: Bool
    let vaultPath: String?
    let provider: String
    let credentials: String
    let checks: [SelfTestCheck]

    enum CodingKeys: String, CodingKey {
        case ok
        case vaultBound = "vault_bound"
        case vaultPath = "vault_path"
        case provider
        case credentials
        case checks
    }
}

/// The full `2nb doctor --json` payload. SuiteStatus's fields stay at the top
/// level (the Go struct embeds it), so this decodes them alongside.
struct DoctorReport: Codable {
    let ok: Bool
    let selftest: SelfTestReport?
}

/// Credential verdict values, matching the CLI's vocabulary exactly.
enum CredentialVerdict: String {
    case accepted
    case rejected
    case unreachable
    case unknown

    init(_ raw: String) {
        self = CredentialVerdict(rawValue: raw) ?? .unknown
    }
}

enum SelfTestPresentation {
    /// The one-line headline above the check list.
    ///
    /// Phrased around the key, because that is the question a user opening this
    /// page is actually asking. "Rejected" and "works but not entitled" get
    /// different sentences on purpose: sending someone to replace a working key
    /// is the most expensive wrong answer this screen can give.
    static func headline(_ report: SelfTestReport) -> String {
        if report.provider == "llama-local" {
            switch CredentialVerdict(report.credentials) {
            case .accepted:
                return report.ok
                    ? "Local Gemma is ready (on-device, no API key needed)."
                    : "Local Gemma runtime answered, but something else is wrong — see below."
            case .rejected:
                return "Local Gemma configuration was rejected."
            case .unreachable, .unknown:
                return "Local Gemma runtime could not be reached — check that models are downloaded."
            }
        }
        switch CredentialVerdict(report.credentials) {
        case .accepted:
            return report.ok
                ? "Everything works."
                : "Your API key works, but something else is wrong — see below."
        case .rejected:
            return "Your API key was rejected by \(report.provider)."
        case .unreachable:
            return "Could not reach \(report.provider) to check. Network?"
        case .unknown:
            return "Could not confirm your API key — no model answered."
        }
    }

    /// Whether the headline should read as a failure.
    static func isFailure(_ report: SelfTestReport) -> Bool { !report.ok }

    /// SF Symbol for a check row.
    static func symbol(for check: SelfTestCheck) -> String {
        if !check.ok { return "xmark.circle.fill" }
        if check.isWarning { return "exclamationmark.triangle.fill" }
        return "checkmark.circle.fill"
    }

    /// Checks worth surfacing when collapsed: anything not plainly passing.
    static func problems(_ report: SelfTestReport) -> [SelfTestCheck] {
        report.checks.filter { !$0.ok || $0.isWarning }
    }

    /// Key checks to render inline during onboarding: search/answer model
    /// probes plus any failing or warning check.
    static func onboardingChecks(_ report: SelfTestReport) -> [SelfTestCheck] {
        report.checks.filter { check in
            isModelProbeCheck(check) || !check.ok || check.isWarning
        }
    }

    // MARK: - Focused AI Model Test Presentation

    static func isModelProbeCheck(_ check: SelfTestCheck) -> Bool {
        check.name == "search model"
            || check.name.hasPrefix("search model (")
            || check.name == "answer model"
            || check.name.hasPrefix("answer model (")
    }

    static func isReembedPendingFailure(_ check: SelfTestCheck, in report: SelfTestReport) -> Bool {
        guard !check.ok else { return false }
        if check.name == "embeddings match selection" {
            return true
        }
        if check.name == "kb_search round-trip" && check.detail.contains("fell back to") {
            return report.checks.contains { $0.name == "embeddings match selection" && !$0.ok }
        }
        return false
    }

    private static let mcpOrStorageCheckNames: Set<String> = [
        "vault",
        "similarity threshold resolves",
        "ai readiness",
        "mcp tools registered",
        "kb_info round-trip",
        "kb_list round-trip",
        "kb_search round-trip",
        "initialize instructions",
        "index WAL health",
        "mcp server processes",
    ]

    static func isMCPOrStorageCheck(_ check: SelfTestCheck) -> Bool {
        mcpOrStorageCheckNames.contains(check.name) || check.name.hasPrefix("mcp configured")
    }

    private static let aiConfigAuthCheckNames: Set<String> = [
        "api key",
        "provider known",
        "active provider enabled",
        "model belongs to active provider",
        "ai.dimensions matches model",
    ]

    static func modelsTestPassed(_ report: SelfTestReport) -> Bool {
        guard CredentialVerdict(report.credentials) == .accepted else {
            return false
        }
        let modelProbes = report.checks.filter { isModelProbeCheck($0) }
        if !modelProbes.isEmpty && modelProbes.contains(where: { !$0.ok }) {
            return false
        }
        for check in report.checks where !check.ok {
            if aiConfigAuthCheckNames.contains(check.name) {
                return false
            }
            if isModelProbeCheck(check)
                || isReembedPendingFailure(check, in: report)
                || isMCPOrStorageCheck(check) {
                continue
            }
            return false
        }
        return true
    }

    static func parseCheckModelID(_ name: String, prefix: String) -> String {
        let basePrefix = prefix.hasSuffix(" (") ? String(prefix.dropLast(2)) : prefix
        let fullPrefix = "\(basePrefix) ("
        guard name.hasPrefix(fullPrefix), name.hasSuffix(")") else {
            return ""
        }
        let start = name.index(name.startIndex, offsetBy: fullPrefix.count)
        let end = name.index(before: name.endIndex)
        guard start <= end else { return "" }
        return String(name[start..<end]).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    static func parsePassingDetail(_ detail: String) -> (latency: String?, proof: String) {
        let trimmed = detail.trimmingCharacters(in: .whitespacesAndNewlines)
        let prefix = "responded in "
        guard trimmed.hasPrefix(prefix) else {
            return (nil, trimmed)
        }
        let rest = String(trimmed.dropFirst(prefix.count))
        if let openParen = rest.range(of: " ("), rest.hasSuffix(")") {
            let latency = String(rest[..<openParen.lowerBound]).trimmingCharacters(in: .whitespaces)
            let innerStart = openParen.upperBound
            let innerEnd = rest.index(before: rest.endIndex)
            let proof = innerStart <= innerEnd
                ? String(rest[innerStart..<innerEnd]).trimmingCharacters(in: .whitespaces)
                : ""
            return (latency.isEmpty ? nil : latency, proof)
        }
        let latency = rest.trimmingCharacters(in: .whitespaces)
        return (latency.isEmpty ? nil : latency, "")
    }

    private static let knownProbeErrorCodes: Set<String> = [
        "access_denied",
        "permission_denied",
        "not_found",
        "throttled",
        "bad_credentials",
        "provider_unreachable",
        "invalid_request",
        "incompatible",
        "timeout",
        "unknown",
    ]

    private static func parseFailingDetail(_ detail: String) -> (detail: String, errorCode: String?) {
        let trimmed = detail.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasSuffix(")"), let openIdx = trimmed.lastIndex(of: "(") else {
            return (trimmed, nil)
        }
        let codeStart = trimmed.index(after: openIdx)
        let codeEnd = trimmed.index(before: trimmed.endIndex)
        guard codeStart <= codeEnd else { return (trimmed, nil) }
        let candidate = String(trimmed[codeStart..<codeEnd]).trimmingCharacters(in: .whitespaces)
        guard knownProbeErrorCodes.contains(candidate) else {
            return (trimmed, nil)
        }
        let prefixEnd = trimmed[..<openIdx].lastIndex(where: { !$0.isWhitespace }).map { trimmed.index(after: $0) } ?? openIdx
        let clean = String(trimmed[..<prefixEnd]).trimmingCharacters(in: .whitespacesAndNewlines)
        return (clean.isEmpty ? trimmed : clean, candidate)
    }

    private static func slotResult(
        from check: SelfTestCheck,
        slot: ModelSlotTestResult.Slot,
        prefix: String,
        provider: String
    ) -> ModelSlotTestResult {
        let modelID = parseCheckModelID(check.name, prefix: prefix)
        if check.ok {
            let (latency, proof) = parsePassingDetail(check.detail)
            return ModelSlotTestResult(
                slot: slot,
                modelID: modelID,
                provider: provider,
                ok: true,
                latency: latency,
                responseProof: proof,
                errorCode: nil,
                remediation: nil
            )
        }
        let (cleanDetail, errorCode) = parseFailingDetail(check.detail)
        return ModelSlotTestResult(
            slot: slot,
            modelID: modelID,
            provider: provider,
            ok: false,
            latency: nil,
            responseProof: cleanDetail,
            errorCode: errorCode,
            remediation: check.fix
        )
    }

    static func extractSlotResults(
        _ report: SelfTestReport,
        genProvider: String,
        embedProvider: String
    ) -> [ModelSlotTestResult] {
        var results: [ModelSlotTestResult] = []
        if let genCheck = report.checks.first(where: { $0.name == "answer model" || $0.name.hasPrefix("answer model (") }) {
            let resolved = genProvider.isEmpty ? report.provider : genProvider
            results.append(slotResult(from: genCheck, slot: .generation, prefix: "answer model", provider: resolved))
        }
        if let embedCheck = report.checks.first(where: { $0.name == "search model" || $0.name.hasPrefix("search model (") }) {
            let resolved = embedProvider.isEmpty ? report.provider : embedProvider
            results.append(slotResult(from: embedCheck, slot: .embedding, prefix: "search model", provider: resolved))
        }
        return results
    }

    static func relevantConfigIssues(_ report: SelfTestReport) -> [SelfTestCheck] {
        report.checks.filter { check in
            (!check.ok || check.isWarning)
                && !isModelProbeCheck(check)
                && !isReembedPendingFailure(check, in: report)
                && !isMCPOrStorageCheck(check)
        }
    }

    static func modelsHeadline(
        _ report: SelfTestReport,
        genProvider: String? = nil,
        embedProvider: String? = nil
    ) -> String {
        if modelsTestPassed(report) {
            let reembedPending = report.checks.contains { isReembedPendingFailure($0, in: report) }
            if reembedPending {
                return "Both AI models responded and passed — re-embed the vault when ready to update search vectors."
            }
            let effGen = (genProvider?.isEmpty == false ? genProvider! : report.provider)
            let effEmbed = (embedProvider?.isEmpty == false ? embedProvider! : report.provider)
            if effGen == "llama-local" && effEmbed == "llama-local" {
                return "Both Local Gemma models responded and passed (on-device, no API key needed)."
            }
            return "Both AI models responded and passed."
        }

        if CredentialVerdict(report.credentials) != .accepted {
            return headline(report)
        }

        let genFail = report.checks.first { !$0.ok && ($0.name == "answer model" || $0.name.hasPrefix("answer model (")) }
        let embedFail = report.checks.first { !$0.ok && ($0.name == "search model" || $0.name.hasPrefix("search model (")) }
        if genFail != nil && embedFail != nil {
            return "Both generation and embedding model tests failed."
        }
        if let genFail {
            let modelID = parseCheckModelID(genFail.name, prefix: "answer model")
            return modelID.isEmpty ? "Generation model test failed." : "Generation model (\(modelID)) failed."
        }
        if let embedFail {
            let modelID = parseCheckModelID(embedFail.name, prefix: "search model")
            return modelID.isEmpty ? "Embedding model test failed." : "Embedding model (\(modelID)) failed."
        }
        if let configFail = report.checks.first(where: { !$0.ok && !isReembedPendingFailure($0, in: report) && !isMCPOrStorageCheck($0) }) {
            return "AI configuration check failed: \(configFail.name)."
        }
        return headline(report)
    }
}

/// Structured per-slot live model probe verdict shared across Settings and Home.
struct ModelSlotTestResult: Equatable, Identifiable, Sendable {
    enum Slot: String, Sendable {
        case generation
        case embedding
    }

    var id: String { slot.rawValue }
    let slot: Slot
    let modelID: String
    let provider: String
    let ok: Bool
    let latency: String?
    let responseProof: String
    let errorCode: String?
    let remediation: String?

    /// Human-readable verification proof for passing probes (`nil` on failures or empty details).
    var proofLabel: String? {
        guard ok else { return nil }
        let trimmed = responseProof.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        switch slot {
        case .embedding:
            return "Verified vector: \(trimmed)"
        case .generation:
            if trimmed.hasPrefix("replied ") {
                let raw = String(trimmed.dropFirst("replied ".count))
                return "Verified response: \(raw)"
            }
            if trimmed.hasPrefix("\"") && trimmed.hasSuffix("\"") {
                return "Verified response: \(trimmed)"
            }
            return "Verified response: \"\(trimmed)\""
        }
    }

    static func fromProbe(_ r: AIProbeResult, slot: Slot) -> ModelSlotTestResult {
        let trimmedLatency = r.latency.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedDetail = r.detail?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let proof: String
        if r.ok {
            proof = trimmedDetail
        } else {
            proof = trimmedDetail.isEmpty ? "Probe failed" : trimmedDetail
        }
        return ModelSlotTestResult(
            slot: slot,
            modelID: r.modelID,
            provider: r.provider,
            ok: r.ok,
            latency: trimmedLatency.isEmpty ? nil : trimmedLatency,
            responseProof: proof,
            errorCode: r.errorCode,
            remediation: r.remediation
        )
    }
}
