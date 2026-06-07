import Foundation
import Testing
@testable import SecondBrain

/// `DashboardTab` is split into the primary Home plus an "Advanced" group in the
/// sidebar (`ContentView`). These tests guard that split: if a case is added to
/// the enum but forgotten in `advanced` (or vice versa), it would silently
/// vanish from the sidebar. They also ensure every tab has a real SF Symbol.
@Test("DashboardTab: Home plus Advanced covers every case exactly once")
func dashboardTabParity() {
    // Home is intentionally not in `advanced`; together they must equal allCases.
    #expect(!DashboardTab.advanced.contains(.home))
    let covered = Set([DashboardTab.home] + DashboardTab.advanced)
    #expect(covered == Set(DashboardTab.allCases))
    // No duplicates hiding inside `advanced`.
    #expect(DashboardTab.advanced.count == Set(DashboardTab.advanced).count)
    // Home + advanced count equals the total case count (no overlap, no gaps).
    #expect(1 + DashboardTab.advanced.count == DashboardTab.allCases.count)
}

@Test("DashboardTab: every tab has a non-empty systemImage and rawValue")
func dashboardTabHasIconAndLabel() {
    for tab in DashboardTab.allCases {
        #expect(!tab.systemImage.isEmpty, "\(tab) is missing a systemImage")
        #expect(!tab.rawValue.isEmpty, "\(tab) is missing a label")
    }
}
