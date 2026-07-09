import Foundation

/// Maps the CLI's classified probe-failure codes (ai.TestErrorCode: the
/// `test_error_code` field on catalog entries and `code` on test results)
/// to short badge labels and actionable guidance. Pure and unit-tested;
/// the raw error detail always accompanies this, never replaced by it.
enum ModelAccessPresentation {
    struct Guidance: Equatable {
        let badge: String
        let title: String
        let advice: String
        let actionLabel: String?
        let actionURL: URL?
    }

    /// Guidance for a classified code. `remediation` (the CLI's own hint)
    /// wins as the advice text when present; note the catalog persists only
    /// the code (not the remediation), so persistent surfaces like the
    /// picker callout use the local fallback text while transient probe
    /// results carry the CLI's own wording. `region` scopes the AWS console
    /// link. `strategy` is the model's invoke strategy: a mantle-plane model
    /// (`bedrock_mantle_responses`) is entitled per-account via AWS Sales and
    /// is invisible to the Bedrock console's Model-access page, so the console
    /// link is suppressed for it (it can't unblock the model).
    static func guidance(code: String?, provider: String, remediation: String? = nil, region: String? = nil, strategy: String? = nil) -> Guidance? {
        guard let code, !code.isEmpty, code != "unknown" else { return nil }
        let isMantle = strategy == "bedrock_mantle_responses"
        let advice: String
        var actionLabel: String?
        var actionURL: URL?
        switch code {
        case "access_denied":
            if isMantle {
                advice = remediation ?? "Your account isn't entitled to this model on the Bedrock mantle plane. This is AWS's staged, per-account rollout, not a problem with 2nb or your credentials, and your other models still work. Request access by contacting AWS Sales; the Bedrock console's Model access page does not govern mantle models."
            } else {
                advice = remediation ?? "Your account can't invoke this model yet. For Bedrock, request access under Model access in the AWS console; newer frontier models can stay gated by AWS's staged rollout even when the console shows access as granted."
                if provider == "bedrock" {
                    actionLabel = "Open AWS console"
                    actionURL = bedrockConsoleURL(region: region)
                }
            }
            return Guidance(badge: "no access", title: "This account can't invoke the model", advice: advice, actionLabel: actionLabel, actionURL: actionURL)
        case "bad_credentials":
            advice = remediation ?? "Credentials are missing, expired, or invalid for \(ProviderDisplay.name(provider))."
            return Guidance(badge: "bad credentials", title: "Credentials problem", advice: advice, actionLabel: nil, actionURL: nil)
        case "throttled":
            advice = remediation ?? "The request was rate-limited, so the model very likely works. Retry in a minute, or lower Embed concurrency under Advanced settings."
            return Guidance(badge: "throttled", title: "Rate-limited (the model likely works)", advice: advice, actionLabel: nil, actionURL: nil)
        case "not_found":
            advice = remediation ?? "The model ID wasn't found for this account or region."
            return Guidance(badge: "not found", title: "Model not found", advice: advice, actionLabel: nil, actionURL: nil)
        case "provider_unreachable":
            advice = remediation ?? "The provider endpoint is unreachable. Check your network, or that the local server is running."
            return Guidance(badge: "unreachable", title: "Provider unreachable", advice: advice, actionLabel: nil, actionURL: nil)
        case "timeout":
            advice = remediation ?? "The probe timed out. Retry; the model may be cold-starting."
            return Guidance(badge: "timeout", title: "Probe timed out", advice: advice, actionLabel: nil, actionURL: nil)
        case "incompatible":
            advice = remediation ?? "2nb doesn't support this model's invoke path, so it was not called."
            return Guidance(badge: "incompatible", title: "Not supported by 2nb", advice: advice, actionLabel: nil, actionURL: nil)
        case "invalid_request":
            advice = remediation ?? "The provider rejected the request as invalid; the model may need a different invoke strategy."
            return Guidance(badge: "invalid request", title: "Request rejected", advice: advice, actionLabel: nil, actionURL: nil)
        default:
            return Guidance(badge: code.replacingOccurrences(of: "_", with: " "), title: "Test failed (\(code))", advice: remediation ?? "", actionLabel: nil, actionURL: nil)
        }
    }

    /// Short badge label for a model row: the classified code when present,
    /// else the generic "failed" for an unclassified failure.
    static func badgeLabel(testError: String?, testErrorCode: String?, provider: String) -> String? {
        guard let err = testError, !err.isEmpty else { return nil }
        if let g = guidance(code: testErrorCode, provider: provider) { return g.badge }
        return "failed"
    }

    /// The Bedrock Model-access console page, region-scoped when known.
    /// The region interpolates into the URL HOST, so anything that isn't a
    /// plain AWS region token falls back to the regionless console URL: a
    /// hostile value in a shared vault's config.yaml must never turn the
    /// "Open AWS console" link into an open redirect.
    static func bedrockConsoleURL(region: String?) -> URL? {
        if let region, !region.isEmpty,
           region.range(of: "^[a-z0-9-]+$", options: .regularExpression) != nil {
            return URL(string: "https://\(region).console.aws.amazon.com/bedrock/home?region=\(region)#/modelaccess")
        }
        return URL(string: "https://console.aws.amazon.com/bedrock/home#/modelaccess")
    }
}
