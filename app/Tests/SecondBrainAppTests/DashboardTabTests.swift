import Foundation
import Testing
@testable import SecondBrain

/// `DashboardTab` is split into the primary Home plus the `secondary` group in
/// the sidebar (`ContentView`). These tests guard that split: if a case is added
/// to the enum but forgotten in `secondary` (or vice versa), it would silently
/// vanish from the sidebar. They also ensure every tab has a real SF Symbol.
///
/// The group used to be called `advanced`, which was accurate when it held the
/// configuration knobs. With those in the Settings window, everything here is
/// ordinary status and the label was actively discouraging people from opening
/// tabs that answer real questions.
@Test("DashboardTab: Home plus the secondary group covers every case exactly once")
func dashboardTabParity() {
    // Home is intentionally not in `secondary`; together they must equal allCases.
    #expect(!DashboardTab.secondary.contains(.home))
    let covered = Set([DashboardTab.home] + DashboardTab.secondary)
    #expect(covered == Set(DashboardTab.allCases))
    // No duplicates hiding inside `secondary`.
    #expect(DashboardTab.secondary.count == Set(DashboardTab.secondary).count)
    // Home + secondary count equals the total case count (no overlap, no gaps).
    #expect(1 + DashboardTab.secondary.count == DashboardTab.allCases.count)
}

@Test("DashboardTab: every tab has a non-empty systemImage and rawValue")
func dashboardTabHasIconAndLabel() {
    for tab in DashboardTab.allCases {
        #expect(!tab.systemImage.isEmpty, "\(tab) is missing a systemImage")
        #expect(!tab.rawValue.isEmpty, "\(tab) is missing a label")
    }
}

/// The sidebar is the thing this change is about, so its size is asserted
/// rather than left to drift back. Eight entries is what made "is my vault
/// healthy?" a three-tab question. Six is deliberate: the Settings sidebar
/// tab was added as a first-class destination, and this pin is bumped with it.
@Test("DashboardTab: the sidebar stays small")
func dashboardTabCount() {
    #expect(DashboardTab.allCases.count == 6)
}

/// Settings is a destination, not a status group, and it sits last the way
/// Mac sidebars put settings at the bottom. Pinning the full order also
/// catches an accidental reshuffle when the next tab lands.
@Test("DashboardTab: secondary keeps its order, Settings last")
func dashboardTabSecondaryOrder() {
    #expect(DashboardTab.secondary == [.models, .notes, .health, .activity, .settings])
    #expect(DashboardTab.secondary.last == .settings)
}

/// Every menu deep link lands on the right tab AND the right pane.
///
/// This is the regression test for a bug this change shipped: folding MCP
/// Server and Git Integration into one Activity tab made both links select
/// `.activity`, and with the pane defaulting to Git, "MCP Server Status…"
/// (Cmd+Shift+M) opened the Git view. Two targets now differ ONLY in the pane,
/// so the pane is the part worth asserting.
@Test("DashboardRoute: every deep link lands on the right tab and pane")
func deepLinksRouteToTabAndPane() {
    #expect(DashboardRoute.tab(for: .aiHub) == .models)
    #expect(DashboardRoute.tab(for: .lintResults) == .notes)

    // The two that share a tab.
    #expect(DashboardRoute.tab(for: .mcpStatus) == .activity)
    #expect(DashboardRoute.tab(for: .gitActivity) == .activity)
    #expect(DashboardRoute.activitySection(for: .mcpStatus) == .mcp)
    #expect(DashboardRoute.activitySection(for: .gitActivity) == .git)

    #expect(DashboardRoute.tab(for: .vaultStatus) == .health)
    #expect(DashboardRoute.healthSection(for: .vaultStatus) == .vault)

    // The sidebar Settings tab (OpenSettingsTabButton with a vault bound).
    #expect(DashboardRoute.tab(for: .settings) == .settings)
}

/// A target that does not land in a group must not carry a pane, or routing
/// would silently move a pane the user is not looking at.
@Test("DashboardRoute: non-group targets request no pane")
func deepLinksWithoutPanesRequestNone() {
    for target in [DashboardRoute.Target.aiHub, .lintResults, .settings] {
        #expect(DashboardRoute.activitySection(for: target) == nil)
        #expect(DashboardRoute.healthSection(for: target) == nil)
    }
    // A target lands in at most one group.
    #expect(DashboardRoute.healthSection(for: .mcpStatus) == nil)
    #expect(DashboardRoute.activitySection(for: .vaultStatus) == nil)
}

/// The group containers host the existing inline views behind a segmented
/// picker; a section dropped from either enum is a status view that silently
/// became unreachable.
@Test("Health and Activity keep every section they absorbed")
func groupSectionsCoverAbsorbedViews() {
    #expect(HealthView.Section.allCases.count == 3)
    #expect(ActivityView.Section.allCases.count == 2)
    for s in HealthView.Section.allCases { #expect(!s.rawValue.isEmpty) }
    for s in ActivityView.Section.allCases { #expect(!s.rawValue.isEmpty) }
}
