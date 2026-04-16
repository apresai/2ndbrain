import Foundation
import SwiftUI
import SecondBrainCore

/// Persisted per-vault graph UI state. Copied into Codable form for UserDefaults;
/// kept as a value type so SwiftUI .onChange(of:) can diff it cheaply.
struct GraphSettings: Equatable, Codable {
    enum Mode: String, Codable, CaseIterable, Identifiable {
        case global, local
        var id: String { rawValue }
        var displayName: String { self == .global ? "Global" : "Local" }
    }

    var mode: Mode = .global
    var localDepth: Int = 2
    var filters: GraphFilters = GraphFilters()
    var forces: GraphSimulationParameters = GraphSimulationParameters()
    var groups: [GraphGroup] = []
    var showArrows: Bool = true
    var labelScale: Double = 1.0

    // MARK: - Persistence

    private static let keyPrefix = "graphSettings."

    static func key(for vaultPath: String?) -> String {
        let suffix = vaultPath.map { String($0.hash & 0x7FFFFFFF) } ?? "default"
        return keyPrefix + suffix
    }

    /// Load settings from UserDefaults for this vault, keeping defaults on
    /// failure so a corrupt JSON payload doesn't wipe the user's graph.
    mutating func load(for vaultPath: String?) {
        let k = Self.key(for: vaultPath)
        guard let data = UserDefaults.standard.data(forKey: k),
              let decoded = try? JSONDecoder().decode(GraphSettings.self, from: data) else {
            return
        }
        self = decoded
    }

    func save(for vaultPath: String?) {
        let k = Self.key(for: vaultPath)
        guard let data = try? JSONEncoder().encode(self) else { return }
        UserDefaults.standard.set(data, forKey: k)
    }
}

// MARK: - Codable bridges

extension GraphFilters: Codable {
    enum CodingKeys: String, CodingKey {
        case searchText, includeTags, excludeTags, includeDocTypes,
             showOrphans, showUnresolvedLinks, showTagNodes
    }
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init()
        self.searchText = try c.decodeIfPresent(String.self, forKey: .searchText) ?? ""
        self.includeTags = try c.decodeIfPresent([String].self, forKey: .includeTags) ?? []
        self.excludeTags = try c.decodeIfPresent([String].self, forKey: .excludeTags) ?? []
        self.includeDocTypes = try c.decodeIfPresent(Set<String>.self, forKey: .includeDocTypes) ?? []
        self.showOrphans = try c.decodeIfPresent(Bool.self, forKey: .showOrphans) ?? true
        self.showUnresolvedLinks = try c.decodeIfPresent(Bool.self, forKey: .showUnresolvedLinks) ?? false
        self.showTagNodes = try c.decodeIfPresent(Bool.self, forKey: .showTagNodes) ?? false
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(searchText, forKey: .searchText)
        try c.encode(includeTags, forKey: .includeTags)
        try c.encode(excludeTags, forKey: .excludeTags)
        try c.encode(includeDocTypes, forKey: .includeDocTypes)
        try c.encode(showOrphans, forKey: .showOrphans)
        try c.encode(showUnresolvedLinks, forKey: .showUnresolvedLinks)
        try c.encode(showTagNodes, forKey: .showTagNodes)
    }
}

extension GraphSimulationParameters: Codable {
    enum CodingKeys: String, CodingKey {
        case centerStrength, repelStrength, linkStrength, linkDistance,
             velocityDecay, alphaDecay, theta, centerX, centerY
    }
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init()
        self.centerStrength = try c.decodeIfPresent(Double.self, forKey: .centerStrength) ?? self.centerStrength
        self.repelStrength = try c.decodeIfPresent(Double.self, forKey: .repelStrength) ?? self.repelStrength
        self.linkStrength = try c.decodeIfPresent(Double.self, forKey: .linkStrength) ?? self.linkStrength
        self.linkDistance = try c.decodeIfPresent(Double.self, forKey: .linkDistance) ?? self.linkDistance
        self.velocityDecay = try c.decodeIfPresent(Double.self, forKey: .velocityDecay) ?? self.velocityDecay
        self.alphaDecay = try c.decodeIfPresent(Double.self, forKey: .alphaDecay) ?? self.alphaDecay
        self.theta = try c.decodeIfPresent(Double.self, forKey: .theta) ?? self.theta
        self.centerX = try c.decodeIfPresent(Double.self, forKey: .centerX) ?? 0
        self.centerY = try c.decodeIfPresent(Double.self, forKey: .centerY) ?? 0
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(centerStrength, forKey: .centerStrength)
        try c.encode(repelStrength, forKey: .repelStrength)
        try c.encode(linkStrength, forKey: .linkStrength)
        try c.encode(linkDistance, forKey: .linkDistance)
        try c.encode(velocityDecay, forKey: .velocityDecay)
        try c.encode(alphaDecay, forKey: .alphaDecay)
        try c.encode(theta, forKey: .theta)
        try c.encode(centerX, forKey: .centerX)
        try c.encode(centerY, forKey: .centerY)
    }
}
