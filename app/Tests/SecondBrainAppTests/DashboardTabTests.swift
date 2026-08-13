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
/// healthy?" a three-tab question.
@Test("DashboardTab: the sidebar stays small")
func dashboardTabCount() {
    #expect(DashboardTab.allCases.count == 5)
}

/// Every menu deep link must land on a tab that still exists. `ContentView`
/// maps five AppState flags onto tabs; when `.status`, `.metrics`, `.updates`,
/// `.mcpServer`, and `.gitIntegration` were folded into Health and Activity,
/// a missed remap would have pointed a menu item at a deleted case.
@Test("DashboardTab: the grouping tabs that absorb the old ones exist")
func dashboardGroupingTabsExist() {
    #expect(DashboardTab.allCases.contains(.health))   // absorbed Vault Status, Metrics, Updates
    #expect(DashboardTab.allCases.contains(.activity)) // absorbed Git Integration, MCP Server
    #expect(DashboardTab.allCases.contains(.models))   // renamed from AI Settings
    #expect(DashboardTab.allCases.contains(.notes))    // renamed from Validation
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
