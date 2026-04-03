import Foundation

/// Maintains a crash recovery journal for unsaved document states.
/// Writes snapshots to `.2ndbrain/recovery/` that can be restored after abnormal termination.
public final class CrashJournal: @unchecked Sendable {
    private let recoveryDir: URL
    private let queue = DispatchQueue(label: "com.apresai.secondbrain.recovery")

    public init(vaultDotDir: URL) {
        self.recoveryDir = vaultDotDir.appendingPathComponent("recovery")
        try? FileManager.default.createDirectory(at: recoveryDir, withIntermediateDirectories: true)
    }

    /// Save an unsaved document state to the recovery journal.
    public func saveSnapshot(documentID: String, content: String) {
        queue.async { [recoveryDir] in
            let snapshotURL = recoveryDir.appendingPathComponent("\(documentID).recovery.md")
            try? content.write(to: snapshotURL, atomically: true, encoding: .utf8)
        }
    }

    /// Remove a snapshot when a document is successfully saved.
    public func clearSnapshot(documentID: String) {
        queue.async { [recoveryDir] in
            let snapshotURL = recoveryDir.appendingPathComponent("\(documentID).recovery.md")
            try? FileManager.default.removeItem(at: snapshotURL)
        }
    }

    /// List all recoverable documents after a crash.
    public func recoverableDocuments() -> [RecoveryEntry] {
        let fm = FileManager.default
        guard let files = try? fm.contentsOfDirectory(at: recoveryDir, includingPropertiesForKeys: [.contentModificationDateKey]) else {
            return []
        }

        return files
            .filter { $0.pathExtension == "md" && $0.lastPathComponent.contains(".recovery.") }
            .compactMap { url -> RecoveryEntry? in
                let docID = url.lastPathComponent
                    .replacingOccurrences(of: ".recovery.md", with: "")
                guard let content = try? String(contentsOf: url, encoding: .utf8) else { return nil }
                let modified = (try? url.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? Date()
                return RecoveryEntry(documentID: docID, content: content, snapshotDate: modified, snapshotURL: url)
            }
            .sorted { $0.snapshotDate > $1.snapshotDate }
    }

    /// Delete all recovery snapshots.
    public func clearAll() {
        queue.async { [recoveryDir] in
            let fm = FileManager.default
            if let files = try? fm.contentsOfDirectory(at: recoveryDir, includingPropertiesForKeys: nil) {
                for file in files {
                    try? fm.removeItem(at: file)
                }
            }
        }
    }
}

public struct RecoveryEntry: Identifiable, Sendable {
    public let id: String
    public let documentID: String
    public let content: String
    public let snapshotDate: Date
    public let snapshotURL: URL

    init(documentID: String, content: String, snapshotDate: Date, snapshotURL: URL) {
        self.id = documentID
        self.documentID = documentID
        self.content = content
        self.snapshotDate = snapshotDate
        self.snapshotURL = snapshotURL
    }
}
