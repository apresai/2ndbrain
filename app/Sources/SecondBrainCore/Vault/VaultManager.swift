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
        process.standardOutput = Pipe() // discard the "Next steps" banner
        try process.run()
        process.waitUntilExit()

        guard process.terminationStatus == 0 else {
            let stderr = String(
                data: errPipe.fileHandleForReading.readDataToEndOfFile(),
                encoding: .utf8
            ) ?? ""
            throw VaultError.cliFailed("vault create exit \(process.terminationStatus): \(stderr)")
        }
    }

    /// Run the CLI indexer on this vault.
    public func runIndex() throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
        process.arguments = CLIPath.args(["index"], vault: rootURL)
        process.currentDirectoryURL = rootURL
        try process.run()
        process.waitUntilExit()
    }

    /// Run the CLI linter and return JSON results.
    public func runLint() throws -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: CLIPath.resolve())
        process.arguments = CLIPath.args(["lint", "--json"], vault: rootURL)
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
