import Foundation

/// Logs errors to `.2ndbrain/logs/` with timestamps and context.
public final class ErrorLogger: @unchecked Sendable {
    private let logsDir: URL
    private let queue = DispatchQueue(label: "com.apresai.secondbrain.errorlog")

    public init(vaultDotDir: URL) {
        self.logsDir = vaultDotDir.appendingPathComponent("logs")
        try? FileManager.default.createDirectory(at: logsDir, withIntermediateDirectories: true)
    }

    public func log(_ message: String, error: Error? = nil, file: String = #file, line: Int = #line) {
        queue.async { [logsDir] in
            let timestamp = ISO8601DateFormatter().string(from: Date())
            let source = URL(fileURLWithPath: file).lastPathComponent
            var entry = "[\(timestamp)] [\(source):\(line)] \(message)"
            if let error {
                entry += " | Error: \(error.localizedDescription)"
            }
            entry += "\n"

            let logFile = logsDir.appendingPathComponent("secondbrain.log")
            if let handle = try? FileHandle(forWritingTo: logFile) {
                handle.seekToEndOfFile()
                if let data = entry.data(using: .utf8) {
                    handle.write(data)
                }
                handle.closeFile()
            } else {
                try? entry.write(to: logFile, atomically: true, encoding: .utf8)
            }
        }
    }
}
