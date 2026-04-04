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

    /// Initialize a new vault at the given directory.
    /// Creates .2ndbrain/ with config.yaml, schemas.yaml, and index.db.
    public static func initializeVault(at url: URL) throws {
        let dotDir = url.appendingPathComponent(".2ndbrain")

        if FileManager.default.fileExists(atPath: dotDir.path) {
            throw VaultError.alreadyInitialized
        }

        let fm = FileManager.default

        // Create directory structure
        for sub in ["", "models", "recovery", "logs"] {
            try fm.createDirectory(
                at: dotDir.appendingPathComponent(sub),
                withIntermediateDirectories: true
            )
        }

        // Write config.yaml
        let vaultName = url.lastPathComponent
        let configYAML = """
        name: \(vaultName)
        version: "1"
        embedding:
          model: nomic-embed-text-v1.5.Q8_0.gguf
          dimensions: 768
          batch_size: 100
        """
        try configYAML.write(
            to: dotDir.appendingPathComponent("config.yaml"),
            atomically: true,
            encoding: .utf8
        )

        // Write schemas.yaml
        let schemasYAML = """
        types:
          adr:
            name: Architecture Decision Record
            description: Records an architecture decision with context and consequences
            fields:
              status:
                type: text
                enum: [proposed, accepted, deprecated, superseded]
              deciders:
                type: list
              superseded-by:
                type: text
            required: [title, status]
            status:
              initial: proposed
              transitions:
                proposed: [accepted, deprecated]
                accepted: [deprecated, superseded]
                deprecated: []
                superseded: []
          runbook:
            name: Runbook
            description: Step-by-step operational procedure
            fields:
              status:
                type: text
                enum: [draft, active, archived]
              service:
                type: text
              severity:
                type: text
                enum: [low, medium, high, critical]
            required: [title, status]
          note:
            name: Note
            description: General knowledge note
            fields:
              status:
                type: text
                enum: [draft, complete]
            required: [title]
          postmortem:
            name: Postmortem
            description: Incident postmortem analysis
            fields:
              status:
                type: text
                enum: [draft, reviewed, published]
              incident-date:
                type: date
              severity:
                type: text
                enum: [low, medium, high, critical]
              services:
                type: list
            required: [title, status, incident-date]
        """
        try schemasYAML.write(
            to: dotDir.appendingPathComponent("schemas.yaml"),
            atomically: true,
            encoding: .utf8
        )

        // Create index.db via DatabaseManager
        _ = try DatabaseManager(path: dotDir.appendingPathComponent("index.db").path)
    }

    /// Run the CLI indexer on this vault.
    public func runIndex() throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
        process.arguments = ["index"]
        process.currentDirectoryURL = rootURL
        try process.run()
        process.waitUntilExit()
    }

    /// Run the CLI linter and return JSON results.
    public func runLint() throws -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
        process.arguments = ["lint", "--json"]
        process.currentDirectoryURL = rootURL
        let pipe = Pipe()
        process.standardOutput = pipe
        try process.run()
        process.waitUntilExit()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        return String(data: data, encoding: .utf8) ?? ""
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
        let root = rootURL.path
        let full = url.path
        if full.hasPrefix(root) {
            var rel = String(full.dropFirst(root.count))
            if rel.hasPrefix("/") { rel = String(rel.dropFirst()) }
            return rel
        }
        return url.lastPathComponent
    }
}

public enum VaultError: LocalizedError {
    case alreadyInitialized

    public var errorDescription: String? {
        switch self {
        case .alreadyInitialized:
            return "This directory is already a 2ndbrain vault."
        }
    }
}
