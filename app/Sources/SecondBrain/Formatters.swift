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

    /// Formats a USD cost estimate, showing "unpriced" when the model lacks
    /// published rate tables and sub-cent precision below $0.01.
    static func usd(_ amount: Double, knownPricing: Bool = true) -> String {
        guard knownPricing else { return "unpriced" }
        if amount <= 0 || !amount.isFinite { return "$0.00" }
        if amount < 0.0001 { return "<$0.0001" }
        let posix = Locale(identifier: "en_US_POSIX")
        if amount < 0.01 {
            return String(format: "$%.4f", locale: posix, amount)
        }
        return String(format: "$%.2f", locale: posix, amount)
    }

    /// Formats a duration in seconds as a compact ETA string ("<1s", "42s", "1m05s", "1h12m").
    static func eta(_ seconds: Double?) -> String {
        guard let seconds, seconds.isFinite, seconds >= 0 else { return "—" }
        if seconds == 0 { return "0s" }
        if seconds < 1.0 { return "<1s" }
        let posix = Locale(identifier: "en_US_POSIX")
        let total = Int(seconds.rounded())
        if total < 60 { return "\(total)s" }
        let minutes = total / 60
        let secs = total % 60
        if minutes < 60 {
            return String(format: "%dm%02ds", locale: posix, minutes, secs)
        }
        let hours = minutes / 60
        let mins = minutes % 60
        return String(format: "%dh%02dm", locale: posix, hours, mins)
    }

    /// Formats a token count cleanly ("640", "18.4K", "1.2M").
    static func tokens(_ count: Int) -> String {
        let clamped = max(0, count)
        if clamped < 1_000 { return "\(clamped)" }
        let posix = Locale(identifier: "en_US_POSIX")
        if clamped < 1_000_000 {
            let k = Double(clamped) / 1_000.0
            return String(format: k >= 100 ? "%.0fK" : "%.1fK", locale: posix, k)
        }
        let m = Double(clamped) / 1_000_000.0
        return String(format: m >= 100 ? "%.0fM" : "%.1fM", locale: posix, m)
    }
}
