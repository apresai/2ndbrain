import SwiftUI

/// The "patient probe" companion copy: renders nothing for the first
/// `delaySeconds` of an in-flight model probe, then explains that a slow
/// first response is expected rather than stuck.
///
/// The CLI's probe deadlines are deliberately generous
/// (cli/internal/ai/timeouts.go): a cold-starting reasoning model can think
/// for minutes before its first byte, and a budget must never fail a working
/// model. The cost of that patience is that a legitimate cold start LOOKS
/// like a hang; this hint manages the expectation on every Test/Validate
/// surface (Home Test, Models Validate, Settings "Test everything", the AI
/// Hub verify progress).
///
/// Host it inside the `if inFlight { ... }` branch of the probe UI: removal
/// on completion cancels the delay task and discards the state, so each
/// probe run starts a fresh countdown.
struct ColdStartHint: View {
    /// Seconds of in-flight probing before the hint appears. Short enough to
    /// land before anyone force-quits, long enough that a normal warm probe
    /// (a few seconds) never shows it.
    static let delaySeconds: TimeInterval = 15

    static let message = "Still working: a cold model can take a few minutes to first respond."

    @State private var visible = false

    var body: some View {
        Group {
            if visible {
                Label(Self.message, systemImage: "hourglass")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .task {
            try? await Task.sleep(nanoseconds: UInt64(Self.delaySeconds * 1_000_000_000))
            if !Task.isCancelled {
                visible = true
            }
        }
    }
}
