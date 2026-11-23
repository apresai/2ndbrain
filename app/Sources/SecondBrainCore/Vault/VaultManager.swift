import Foundation

public final class VaultManager: @unchecked Sendable {
    public let rootURL: URL
    public let dotDirURL: URL

    public init(rootURL: URL) {
        self.rootURL = rootURL
        self.dotDirURL = rootURL.appendingPathComponent(".2ndbrain")
    }

    public var indexDBPath: String {
        dotDirURL.appendingPathComponent("index.db").path
    }

    public var isInitialized: Bool {
        FileManager.default.fileExists(atPath: dotDirURL.path)
    }

    /// True when the folder is a real Obsidian vault — it contains an
    /// `.obsidian/` config directory. 2ndbrain operates on Obsidian vaults; a
    /// bare folder that merely has (or could get) a `.2ndbrain/` sidecar is not
    /// one, and opening it is almost always a mistake.
    public var isObsidianVault: Bool {
        var isDir: ObjCBool = false
        let exists = FileManager.default.fileExists(
            atPath: rootURL.appendingPathComponent(".obsidian").path, isDirectory: &isDir)
        return exists && isDir.boolValue
    }

    /// Initialize a new vault at the given directory by shelling out to
    /// `2nb vault create`, so config.yaml and schemas.yaml always match what
    /// the CLI considers canonical. The prior hand-rolled YAML here drifted
    /// from `DefaultConfig()`/builtin schemas — missing the `ai:` block, and
    /// omitting the `prd`/`prfaq` types that the editor exposes in the new
    /// note menu.
    public static func initializeVault(at url: URL) throws {
        let dotDir = url.appendingPathComponent(".2ndbrain")
        if FileManager.default.fileExists(atPath: dotDir.path) {
            throw VaultError.alreadyInitialized
        }

        // Ensure the parent exists — the CLI creates the vault directory
        // itself but not arbitrary parent chains.
        try FileManager.default.createDirectory(
            at: url.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )

        let process = Process()
        process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
        // `vault create` takes the path as a positional argument; the
        // CLIPath.args helper is for post-init commands that need --vault.
        process.arguments = ["vault", "create", url.path]
        let errPipe = Pipe()
        process.standardError = errPipe
        // Discard the "Next steps" banner. It used to go to a Pipe nobody
        // read, which deadlocks the child once it fills the pipe buffer.
        process.standardOutput = FileHandle.nullDevice
        try process.run()
        // Read stderr to EOF BEFORE waiting, so a long error cannot fill the
        // pipe and block the child's exit. This blocks the calling thread for
        // the life of the command: callers on the main actor go through
        // AppState.createVaultAsync, which runs this detached.
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()

        guard process.terminationStatus == 0 else {
            let stderr = String(data: errData, encoding: .utf8) ?? ""
            throw VaultError.cliFailed("vault create exit \(process.terminationStatus): \(stderr)")
        }
    }

    /// List all markdown files in the vault.
    public func listMarkdownFiles() -> [URL] {
        let fm = FileManager.default
        guard let enumerator = fm.enumerator(
            at: rootURL,
            includingPropertiesForKeys: [.isRegularFileKey],
            options: [.skipsHiddenFiles, .skipsPackageDescendants]
        ) else { return [] }

        var files: [URL] = []
        for case let url as URL in enumerator {
            if url.pathExtension.lowercased() == "md" {
                files.append(url)
            }
        }
        return files.sorted { $0.lastPathComponent < $1.lastPathComponent }
    }

    /// Get the vault-relative path for a file URL.
    public func relativePath(for url: URL) -> String {
        let root = rootURL.resolvingSymlinksInPath().standardizedFileURL.path
        let full = url.resolvingSymlinksInPath().standardizedFileURL.path
        if full == root { return "" }
        let prefix = root.hasSuffix("/") ? root : root + "/"
        if full.hasPrefix(prefix) {
            return String(full.dropFirst(prefix.count))
        }
        return url.lastPathComponent
    }

    // MARK: - Obsidian Template Exclusion (mirrors cli/internal/vault/templates_folder.go)

    /// Vault-relative folder paths that hold Obsidian or Templater template files.
    public func obsidianTemplateFolders() -> [String] {
        var seen = Set<String>()
        func add(_ raw: String?) {
            guard let raw, let rel = Self.normalizeTemplateFolder(raw) else { return }
            seen.insert(rel)
        }

        let templatesConfig = rootURL.appendingPathComponent(".obsidian/templates.json")
        add(Self.readJSONStringField(at: templatesConfig, field: "folder"))

        let templaterConfig = rootURL.appendingPathComponent(".obsidian/plugins/templater-obsidian/data.json")
        add(Self.readJSONStringField(at: templaterConfig, field: "templates_folder"))

        if seen.isEmpty && conventionalTemplatesApply() {
            seen.insert("templates")
        }
        return seen.sorted()
    }

    /// True if `relPath` is inside any excluded template folder (segment-aware).
    public static func isExcludedFolderPath(_ relPath: String, excludedFolders: [String]) -> Bool {
        guard !excludedFolders.isEmpty else { return false }
        let rel = relPath.replacingOccurrences(of: "\\", with: "/")
        return excludedFolders.contains { folder in
            rel == folder || rel.hasPrefix(folder + "/")
        }
    }

    /// True if the leading YAML frontmatter block contains unresolved Obsidian
    /// (`{{...}}`) or Templater (`<%...%>`) placeholder tokens. Body prose outside
    /// frontmatter is never matched.
    public static func hasTemplatePlaceholders(_ raw: String) -> Bool {
        let normalized = raw
            .replacingOccurrences(of: "\r\n", with: "\n")
            .replacingOccurrences(of: "\r", with: "\n")
        guard let firstNL = normalized.firstIndex(of: "\n") else { return false }
        let firstLine = stripTrailingCR(String(normalized[..<firstNL]))
        guard isTemplateFenceLine(firstLine) else { return false }

        let rest = String(normalized[normalized.index(after: firstNL)...])
        guard !rest.isEmpty else { return false }

        let secondLine: String
        if let nextNL = rest.firstIndex(of: "\n") {
            secondLine = stripTrailingCR(String(rest[..<nextNL]))
        } else {
            secondLine = stripTrailingCR(rest)
        }
        if secondLine.trimmingCharacters(in: .whitespaces).isEmpty {
            return false
        }

        let lines = rest.split(separator: "\n", omittingEmptySubsequences: false).map {
            stripTrailingCR(String($0))
        }
        for (i, line) in lines.enumerated() {
            if isTemplateFenceLine(line) {
                let fm = lines[..<i].joined(separator: "\n")
                return fm.contains("{{") || fm.contains("<%")
            }
        }

        var fmLines: [String] = []
        for (i, line) in lines.enumerated() {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty {
                break
            }
            if isMarkdownHeadingLine(line) && !isLeadingYAMLComment(lines, after: i) {
                break
            }
            if isYAMLKeyLine(line)
                || trimmed.hasPrefix("#")
                || trimmed.hasPrefix("- ")
                || trimmed == "-"
                || line.hasPrefix(" ")
                || line.hasPrefix("\t")
                || trimmed.hasPrefix("<%")
                || trimmed.hasPrefix("{{") {
                fmLines.append(line)
                continue
            }
            break
        }
        let fm = fmLines.joined(separator: "\n")
        return fm.contains("{{") || fm.contains("<%")
    }

    private func conventionalTemplatesApply() -> Bool {
        let corePlugins = rootURL.appendingPathComponent(".obsidian/core-plugins.json")
        guard Self.readJSONBoolField(at: corePlugins, field: "templates") else {
            return false
        }
        var isDir: ObjCBool = false
        let templatesDir = rootURL.appendingPathComponent("templates")
        return FileManager.default.fileExists(atPath: templatesDir.path, isDirectory: &isDir) && isDir.boolValue
    }

    private static func readJSONStringField(at url: URL, field: String) -> String? {
        guard let data = try? Data(contentsOf: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let value = obj[field] as? String else {
            return nil
        }
        return value
    }

    private static func readJSONBoolField(at url: URL, field: String) -> Bool {
        guard let data = try? Data(contentsOf: url),
              let json = try? JSONSerialization.jsonObject(with: data) else {
            return false
        }
        if let obj = json as? [String: Any] {
            return (obj[field] as? Bool) == true
        }
        if let list = json as? [Any] {
            return list.contains { ($0 as? String) == field }
        }
        return false
    }

    private static func normalizeTemplateFolder(_ raw: String) -> String? {
        if raw.hasPrefix("/") {
            return nil
        }
        var s = raw
            .replacingOccurrences(of: "\\", with: "/")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        while s.hasPrefix("/") { s.removeFirst() }
        while s.hasSuffix("/") { s.removeLast() }
        if s.isEmpty || s == "." {
            return nil
        }
        var parts: [String] = []
        for component in s.split(separator: "/") {
            let seg = String(component)
            if seg.isEmpty || seg == "." {
                continue
            }
            if seg == ".." {
                if parts.isEmpty { return nil }
                parts.removeLast()
                continue
            }
            parts.append(seg)
        }
        guard !parts.isEmpty else { return nil }
        return parts.joined(separator: "/")
    }

    private static func stripTrailingCR(_ s: String) -> String {
        s.hasSuffix("\r") ? String(s.dropLast()) : s
    }

    private static func isTemplateFenceLine(_ line: String) -> Bool {
        guard line.hasPrefix("---") else { return false }
        return line.dropFirst(3).allSatisfy { $0 == " " || $0 == "\t" || $0 == "\r" }
    }

    private static func isLeadingYAMLComment(_ lines: [String], after idx: Int) -> Bool {
        var j = idx + 1
        while j < lines.count {
            let next = lines[j]
            let trimmed = next.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty {
                return false
            }
            if isYAMLKeyLine(next) || trimmed.hasPrefix("<%") || trimmed.hasPrefix("{{") {
                return true
            }
            if trimmed.hasPrefix("#") || next.hasPrefix(" ") || next.hasPrefix("\t") {
                j += 1
                continue
            }
            return false
        }
        return false
    }

    private static func isYAMLKeyLine(_ line: String) -> Bool {
        var idx = line.startIndex
        while idx < line.endIndex && (line[idx] == " " || line[idx] == "\t") {
            idx = line.index(after: idx)
        }
        let keyStart = idx
        while idx < line.endIndex {
            let ch = line[idx]
            let isKeyChar = (ch >= "A" && ch <= "Z")
                || (ch >= "a" && ch <= "z")
                || (ch >= "0" && ch <= "9")
                || ch == "_" || ch == "." || ch == "-"
            if !isKeyChar { break }
            idx = line.index(after: idx)
        }
        guard idx > keyStart else { return false }
        while idx < line.endIndex && (line[idx] == " " || line[idx] == "\t") {
            idx = line.index(after: idx)
        }
        return idx < line.endIndex && line[idx] == ":"
    }

    private static func isMarkdownHeadingLine(_ line: String) -> Bool {
        var idx = line.startIndex
        while idx < line.endIndex && (line[idx] == " " || line[idx] == "\t") {
            idx = line.index(after: idx)
        }
        var hashes = 0
        while idx < line.endIndex && line[idx] == "#" {
            hashes += 1
            idx = line.index(after: idx)
        }
        guard (1...6).contains(hashes), idx < line.endIndex else { return false }
        return line[idx] == " " || line[idx] == "\t"
    }
}

public enum VaultError: LocalizedError {
    case alreadyInitialized
    case cliFailed(String)

    public var errorDescription: String? {
        switch self {
        case .alreadyInitialized:
            return "This directory is already a 2ndbrain vault."
        case .cliFailed(let details):
            return "Vault CLI failed: \(details)"
        }
    }
}
