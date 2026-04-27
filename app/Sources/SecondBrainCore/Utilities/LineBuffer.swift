import Foundation

public final class LineBuffer: @unchecked Sendable {
    private let lock = NSLock()
    private var buffer = Data()

    public init() {}

    public func append(_ data: Data) -> [String] {
        lock.lock()
        defer { lock.unlock() }

        buffer.append(data)
        var lines: [String] = []
        while let newline = buffer.firstIndex(of: 0x0A) {
            let lineData = buffer[..<newline]
            let line = String(decoding: lineData, as: UTF8.self)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            if !line.isEmpty {
                lines.append(line)
            }
            buffer.removeSubrange(...newline)
        }
        return lines
    }

    public func append(_ text: String) -> [String] {
        append(Data(text.utf8))
    }

    public func finish() -> [String] {
        lock.lock()
        defer { lock.unlock() }

        let line = String(decoding: buffer, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        buffer.removeAll(keepingCapacity: true)
        return line.isEmpty ? [] : [line]
    }
}
