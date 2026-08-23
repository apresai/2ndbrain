import Foundation

/// Shared display formatters for durations, byte sizes, and relative dates.
///
/// Extracted from MetricsView's private helpers so the Testing tab's benchmark
/// feed and the performance observatory render "1.2s", "3.4 MB", and "2d ago"
/// identically, instead of each surface growing a private copy that drifts.
enum Formatters {
    /// "412ms", "1.2s", "2m05s".
    static func duration(_ ms: Int) -> String { durationD(Double(ms)) }

    /// Double-precision variant for averaged latencies (aggregate rows).
    static func durationD(_ ms: Double) -> String {
        if ms < 1000 { return "\(Int(ms.rounded()))ms" }
        if ms < 60000 { return String(format: "%.1fs", ms / 1000) }
        let total = Int(ms)
        return "\(total / 60000)m\(String(format: "%02d", (total % 60000) / 1000))s"
    }

    /// "512 B", "1.5 KB", "3.4 MB". Sub-kilobyte counts stay exact.
    static func bytes(_ n: Int) -> String {
        let units = ["B", "KB", "MB", "GB", "TB"]
        var v = Double(n)
        var i = 0
        while v >= 1024 && i < units.count - 1 {
            v /= 1024
            i += 1
        }
        return i == 0 ? "\(n) B" : String(format: "%.1f %@", v, units[i])
    }

    /// Best-effort relative "2d ago" for an RFC3339 timestamp, or nil when it
    /// does not parse. Fractional seconds are tolerated (the CLI emits both
    /// shapes depending on the field), so callers can omit a clause instead of
    /// rendering a raw timestamp mid-sentence.
    static func relativeDateIfParseable(_ raw: String) -> String? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = fractional.date(from: raw) ?? ISO8601DateFormatter().date(from: raw) else {
            return nil
        }
        let fmt = RelativeDateTimeFormatter()
        fmt.unitsStyle = .short
        return fmt.localizedString(for: date, relativeTo: Date())
    }

    /// Like relativeDateIfParseable, but falls back to the raw string so a
    /// table cell never renders empty.
    static func relativeDate(_ raw: String) -> String {
        relativeDateIfParseable(raw) ?? raw
    }
}
