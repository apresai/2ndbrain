import Foundation
#if canImport(AppKit)
import AppKit
#endif

/// Pure copy for the paid-operation confirm dialog, split out so the exact
/// wording (estimate present, unknown pricing, estimator unavailable) is
/// unit-testable without AppKit.
enum PaidOpCopy {
    /// Dialog body for a paid operation. `preview` nil means the estimator
    /// itself failed; the confirm still shows (never block the action on the
    /// estimator), just without a number.
    static func message(_ preview: CostPreviewResponse?, operation: String) -> String {
        guard let preview else {
            return "\(operation) makes real API calls to your provider. A cost estimate isn't available right now; probes are typically fractions of a cent."
        }
        let unknown = preview.estimates.filter { !$0.knownPricing }.count
        // All-unknown pricing with a zero total: a "$0.000000" number would
        // be misleading, so lead with the unknown instead.
        if unknown == preview.estimates.count && preview.totalUSD == 0 && unknown > 0 {
            return "\(operation) makes real API calls to your provider. Pricing is unknown for the selected model(s); probes are typically fractions of a cent."
        }
        var text = String(format: "%@ makes real API calls to your provider. Estimated cost: $%.6f.", operation, preview.totalUSD)
        if unknown > 0 {
            text += " Pricing is unknown for \(unknown) model(s), so the real cost may be slightly higher."
        }
        return text
    }
}

/// Presents a cost-estimate confirm before a paid provider call (Test,
/// Benchmark). Returns true when the user chose to proceed. The estimate is
/// best-effort: a cost-preview failure degrades to a numberless confirm
/// rather than blocking the operation.
@MainActor
func confirmPaidOperation(appState: AppState, modelIDs: [String], probe: String, operation: String) async -> Bool {
    let preview = try? await appState.costPreview(modelIDs: modelIDs, probe: probe)
    return confirmPaidOperation(preview: preview, operation: operation)
}

/// Presents the same cost-estimate confirm from an ALREADY-computed preview, so
/// a caller that needs the estimate for other decisions (e.g. deriving a
/// verify `--cost-cap`) can preview once and reuse it instead of re-shelling
/// `cost-preview` inside the confirm.
@MainActor
func confirmPaidOperation(preview: CostPreviewResponse?, operation: String) -> Bool {
    #if canImport(AppKit)
    let alert = NSAlert()
    alert.messageText = "\(operation)?"
    alert.informativeText = PaidOpCopy.message(preview, operation: operation)
    alert.addButton(withTitle: operation)
    alert.addButton(withTitle: "Cancel")
    return alert.runModal() == .alertFirstButtonReturn
    #else
    return true
    #endif
}
