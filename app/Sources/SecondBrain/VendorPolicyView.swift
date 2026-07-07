import SwiftUI
import os

private let vendorPolicyLog = Logger(subsystem: "dev.apresai.2ndbrain", category: "vendorpolicy")

/// Pure helpers for the vendor-policy sheet: grouping the loaded catalog into
/// per-provider vendor rows, computing which vendors start checked, and folding
/// the checkbox state back into the vendor list to persist. Split out of the
/// SwiftUI view so the grouping / pre-population / apply math is unit-testable
/// without AppKit or a live CLI.
enum VendorPolicyBuilder {
    /// One vendor's checkbox row within a provider section. `vendor` is the
    /// machine slug used by `--enable-only` and the policy's `by_vendor` keys.
    struct VendorRow: Identifiable, Equatable {
        let vendor: String
        let display: String
        let modelCount: Int
        let verified: Int   // models on this vendor that passed a probe
        let noAccess: Int   // models classified access_denied by a probe
        var id: String { vendor }
    }

    /// One provider section: its vendor rows, sorted by display name.
    struct ProviderSection: Identifiable, Equatable {
        let provider: String
        let vendors: [VendorRow]
        var id: String { provider }
    }

    /// Providers that have at least one model in the loaded catalog, each with
    /// its vendors grouped from the models' `vendor` / `vendorDisplay` (the same
    /// grouping the Hub's `groupedByVendor` uses). Providers are ordered
    /// bedrock, openrouter, ollama, llama-local, then any others alphabetically;
    /// vendors within a provider are ordered by display name.
    static func providerSections(from models: [CatalogModelInfo]) -> [ProviderSection] {
        var byProvider: [String: [CatalogModelInfo]] = [:]
        for m in models { byProvider[m.provider, default: []].append(m) }
        let order = ["bedrock", "openrouter", "ollama", "llama-local"]
        let providers = byProvider.keys.sorted { a, b in
            let ia = order.firstIndex(of: a) ?? order.count
            let ib = order.firstIndex(of: b) ?? order.count
            if ia != ib { return ia < ib }
            return a < b
        }
        return providers.map { provider in
            ProviderSection(provider: provider, vendors: vendorRows(byProvider[provider] ?? []))
        }
    }

    /// Vendor rows for one provider's models: counts plus a verified /
    /// no-access summary derived from each model's persisted test state.
    static func vendorRows(_ models: [CatalogModelInfo]) -> [VendorRow] {
        var byVendor: [String: [CatalogModelInfo]] = [:]
        var displayByVendor: [String: String] = [:]
        for m in models {
            let vendor = m.vendor ?? "other"
            byVendor[vendor, default: []].append(m)
            if displayByVendor[vendor] == nil { displayByVendor[vendor] = m.vendorDisplay ?? "Other" }
        }
        let rows = byVendor.map { (vendor, list) -> VendorRow in
            VendorRow(
                vendor: vendor,
                display: displayByVendor[vendor] ?? "Other",
                modelCount: list.count,
                verified: list.filter { verifiedOK($0) }.count,
                noAccess: list.filter { $0.testErrorCode == "access_denied" }.count
            )
        }
        return rows.sorted { $0.display < $1.display }
    }

    private static func verifiedOK(_ m: CatalogModelInfo) -> Bool {
        (m.testedAt?.isEmpty == false) && (m.testError ?? "").isEmpty
    }

    /// The vendors that start CHECKED for a provider: the policy's vendor list
    /// when a policy exists, else every vendor (no policy = nothing restricted,
    /// so everything is enabled).
    static func initialCheckedVendors(section: ProviderSection, policy: VendorPolicyResult?) -> Set<String> {
        if let policy { return Set(policy.vendors) }
        return Set(section.vendors.map(\.vendor))
    }

    /// The vendor list to persist when applying: the checked catalog vendors
    /// PLUS any existing-policy vendors that have no catalog row. A policy can
    /// name a future vendor (one with no model yet), and the sheet only shows
    /// vendors present in the catalog, so applying must not silently drop those
    /// future-intent vendors.
    static func vendorsToApply(checked: Set<String>, catalogVendors: [String], existingPolicy: VendorPolicyResult?) -> [String] {
        var result = checked
        if let existingPolicy {
            let catalogSet = Set(catalogVendors)
            for v in existingPolicy.vendors where !catalogSet.contains(v) {
                result.insert(v)
            }
        }
        return result.sorted()
    }
}

/// Pure helpers for the AI Hub's disabled-model visibility: hiding disabled
/// rows from the curated view and auto-collapsing a fully-disabled vendor
/// group. Split out so the filter and the collapse rule are unit-testable.
enum CatalogVisibility {
    /// In the curated view, explicitly-disabled models are hidden until the
    /// user reveals them. Returns the visible slice and the hidden count so the
    /// "Show disabled (N)" reveal can be sized. A nil `enabled` is treated as
    /// enabled (the tri-state default), so only models the user or a policy
    /// disabled are hidden.
    static func hideDisabled(_ models: [CatalogModelInfo], showDisabled: Bool) -> (visible: [CatalogModelInfo], hiddenDisabled: Int) {
        let hiddenCount = models.filter { $0.enabled == false }.count
        if showDisabled { return (models, hiddenCount) }
        return (models.filter { $0.enabled != false }, hiddenCount)
    }

    /// Effective collapsed state for a vendor group. The user's explicit toggle
    /// always wins. With `summaryFirst` (the AI Hub's default), every group
    /// starts collapsed so the catalog reads as compact per-vendor summary rows
    /// rather than a wall of model rows; otherwise only a fully-disabled group
    /// auto-collapses (its rows are noise until re-enabled) and every other
    /// group defaults expanded. `summaryFirst` defaults false so the A3/A4
    /// callers and their tests keep their exact behavior.
    static func groupCollapsed(userOverride: Bool?, allDisabled: Bool, summaryFirst: Bool = false) -> Bool {
        if let userOverride { return userOverride }
        return summaryFirst || allDisabled
    }
}

/// The "Manage vendors" sheet: the payoff for "disable all Bedrock models
/// except Anthropic (or Anthropic plus DeepSeek)". Each provider that has
/// models gets a checkbox per vendor; Apply persists an enable-only vendor
/// policy (`2nb models policy set`) so newly discovered models from unchecked
/// vendors arrive pre-disabled. Clear removes the policy. After an Apply it
/// offers to validate the now-enabled models via the Hub's existing flow.
struct VendorPolicyView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss

    /// The catalog the Hub already loaded, grouped client-side into vendors.
    let models: [CatalogModelInfo]
    /// Asks the Hub to run its Validate flow for the provider after an apply.
    let onValidate: (String) -> Void

    @State private var policies: [VendorPolicyResult] = []
    /// provider -> checked vendor slugs.
    @State private var checked: [String: Set<String>] = [:]
    /// provider -> the last apply's effect, shown as "N enabled, M disabled".
    @State private var results: [String: VendorPolicyEffect] = [:]
    /// The provider whose just-applied policy is offering a Validate-now prompt.
    @State private var validateOffer: String?
    @State private var busyProvider: String?
    @State private var errorText: String?
    @State private var loaded = false

    private var sections: [VendorPolicyBuilder.ProviderSection] {
        VendorPolicyBuilder.providerSections(from: models)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if sections.isEmpty {
                        Text("No models are loaded yet. Configure a provider in the AI Hub first.")
                            .foregroundStyle(.secondary)
                            .padding(.vertical, 8)
                    }
                    ForEach(sections) { section in
                        providerSection(section)
                    }
                }
                .padding()
            }
            Divider()
            footer
        }
        .frame(width: 560, height: 620)
        .task { await load() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Manage vendors").font(.title3.bold())
            Text("Enable only the model vendors you want, per provider. Models from unchecked vendors are disabled, and new ones discovered later arrive disabled too.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding()
    }

    private var footer: some View {
        HStack(spacing: 8) {
            if let errorText {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Text(errorText).font(.caption).foregroundStyle(.red).lineLimit(2)
            }
            Spacer()
            Button("Done") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
        .padding()
    }

    @ViewBuilder
    private func providerSection(_ section: VendorPolicyBuilder.ProviderSection) -> some View {
        let policy = policyFor(section.provider)
        let checkedSet = checked[section.provider] ?? []
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(ProviderDisplay.name(section.provider)).font(.headline)
                Spacer()
                if busyProvider == section.provider { ProgressView().controlSize(.small) }
            }
            Text(policyStatusLine(policy))
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            VStack(alignment: .leading, spacing: 4) {
                ForEach(section.vendors) { row in
                    Toggle(isOn: vendorBinding(provider: section.provider, vendor: row.vendor)) {
                        HStack(spacing: 6) {
                            Text(row.display)
                            Text("(\(row.modelCount))").font(.caption).foregroundStyle(.secondary)
                            if let summary = vendorAccessSummary(row) {
                                Text(summary).font(.caption2).foregroundStyle(.tertiary)
                            }
                        }
                    }
                    .toggleStyle(.checkbox)
                    .disabled(busyProvider != nil)
                }
            }

            if let effect = results[section.provider] {
                Text(appliedEffectLine(effect))
                    .font(.caption)
                    .foregroundStyle(.green)
            }

            if validateOffer == section.provider {
                validateOfferRow(section.provider)
            }

            HStack(spacing: 8) {
                Button("Apply") { apply(section) }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(busyProvider != nil || checkedSet.isEmpty)
                if policy != nil {
                    Button("Clear policy") { clear(section.provider) }
                        .controlSize(.small)
                        .disabled(busyProvider != nil)
                }
                if checkedSet.isEmpty {
                    Text("Check at least one vendor, or use Clear policy to remove the restriction.")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }
        }
        .padding(12)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }

    private func validateOfferRow(_ provider: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "checkmark.seal").foregroundStyle(.secondary)
            Text("Validate the enabled models now?").font(.caption)
            Button("Validate now") {
                dismiss()
                onValidate(provider)
            }
            .controlSize(.small)
            Button("Not now") { validateOffer = nil }
                .controlSize(.small)
                .buttonStyle(.borderless)
        }
        .padding(6)
        .background(Color.accentColor.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    // MARK: - Derived text

    private func policyStatusLine(_ policy: VendorPolicyResult?) -> String {
        guard let policy, !policy.vendors.isEmpty else {
            return "No policy. Every vendor is enabled."
        }
        return "Policy: enable only \(policy.vendors.joined(separator: ", ")) (\(policy.scope) scope)."
    }

    private func appliedEffectLine(_ effect: VendorPolicyEffect) -> String {
        var line = "Applied: \(effect.enabled) enabled, \(effect.disabled) disabled"
        if effect.overridden > 0 {
            line += ", \(effect.overridden) kept by a per-model override"
        }
        return line + "."
    }

    private func vendorAccessSummary(_ row: VendorPolicyBuilder.VendorRow) -> String? {
        var parts: [String] = []
        if row.verified > 0 { parts.append("\(row.verified) verified") }
        if row.noAccess > 0 { parts.append("\(row.noAccess) no access") }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    // MARK: - State

    private func policyFor(_ provider: String) -> VendorPolicyResult? {
        let matching = policies.filter { $0.provider == provider }
        return matching.first { $0.scope == "vault" } ?? matching.first
    }

    private func vendorBinding(provider: String, vendor: String) -> Binding<Bool> {
        Binding(
            get: { checked[provider]?.contains(vendor) ?? false },
            set: { isOn in
                var set = checked[provider] ?? []
                if isOn { set.insert(vendor) } else { set.remove(vendor) }
                checked[provider] = set
            }
        )
    }

    // MARK: - Actions

    private func load() async {
        guard !loaded else { return }
        loaded = true
        // A CLI predating `models policy` throws; degrade to no policies so the
        // sheet still opens with all-vendors-checked (nothing restricted).
        policies = (try? await appState.fetchVendorPolicy()) ?? []
        var initial: [String: Set<String>] = [:]
        for section in sections {
            initial[section.provider] = VendorPolicyBuilder.initialCheckedVendors(
                section: section, policy: policyFor(section.provider))
        }
        checked = initial
    }

    private func apply(_ section: VendorPolicyBuilder.ProviderSection) {
        let provider = section.provider
        let checkedSet = checked[provider] ?? []
        guard !checkedSet.isEmpty else { return }
        let vendors = VendorPolicyBuilder.vendorsToApply(
            checked: checkedSet,
            catalogVendors: section.vendors.map(\.vendor),
            existingPolicy: policyFor(provider)
        )
        busyProvider = provider
        errorText = nil
        Task {
            defer { busyProvider = nil }
            do {
                let res = try await appState.setVendorPolicy(provider: provider, vendors: vendors)
                results[provider] = res.effect
                validateOffer = provider
                policies = (try? await appState.fetchVendorPolicy()) ?? policies
            } catch {
                vendorPolicyLog.error("apply policy failed: \(error.localizedDescription, privacy: .public)")
                errorText = "Apply failed: \(error.localizedDescription)"
            }
        }
    }

    private func clear(_ provider: String) {
        busyProvider = provider
        errorText = nil
        Task {
            defer { busyProvider = nil }
            do {
                try await appState.clearVendorPolicy(provider: provider)
                results[provider] = nil
                validateOffer = nil
                policies = (try? await appState.fetchVendorPolicy()) ?? []
                if let section = sections.first(where: { $0.provider == provider }) {
                    checked[provider] = Set(section.vendors.map(\.vendor))
                }
            } catch {
                vendorPolicyLog.error("clear policy failed: \(error.localizedDescription, privacy: .public)")
                errorText = "Clear failed: \(error.localizedDescription)"
            }
        }
    }
}
