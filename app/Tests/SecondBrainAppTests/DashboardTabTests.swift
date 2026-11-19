import Foundation
import Testing
@testable import SecondBrain

/// `DashboardTab` is split into the primary Home plus the `secondary` group in
/// the sidebar (`ContentView`). These tests guard that split and verify that the
/// simplified 4-tab Beta sidebar stays focused on user capabilities.
@Test("DashboardTab: Home plus the secondary group covers every case exactly once")
func dashboardTabParity() {
    #expect(!DashboardTab.secondary.contains(.home))
    let covered = Set([DashboardTab.home] + DashboardTab.secondary)
    #expect(covered == Set(DashboardTab.allCases))
    #expect(DashboardTab.secondary.count == Set(DashboardTab.secondary).count)
    #expect(1 + DashboardTab.secondary.count == DashboardTab.allCases.count)
}

@Test("DashboardTab: every tab has a non-empty systemImage and rawValue")
func dashboardTabHasIconAndLabel() {
    for tab in DashboardTab.allCases {
        #expect(!tab.systemImage.isEmpty, "\(tab) is missing a systemImage")
        #expect(!tab.rawValue.isEmpty, "\(tab) is missing a label")
    }
}

@Test("DashboardTab: the Beta sidebar stays focused at four destinations")
func dashboardTabCount() {
    #expect(DashboardTab.allCases.count == 4)
}

@Test("DashboardTab: secondary keeps capability order (Ask & Search, Notes, Activity)")
func dashboardTabSecondaryOrder() {
    #expect(DashboardTab.secondary == [.askSearch, .notes, .activity])
}

@Test("DashboardRoute: every deep link lands on the right tab and pane")
func deepLinksRouteToTabAndPane() {
    #expect(DashboardRoute.tab(for: .askSearch) == .askSearch)
    #expect(DashboardRoute.notesSection(for: .askSearch) == nil)
    #expect(DashboardRoute.activitySection(for: .askSearch) == nil)

    // The two that share the Notes tab.
    #expect(DashboardRoute.tab(for: .lintResults) == .notes)
    #expect(DashboardRoute.tab(for: .notePolish) == .notes)
    #expect(DashboardRoute.notesSection(for: .lintResults) == .linksAndSchema)
    #expect(DashboardRoute.notesSection(for: .notePolish) == .polish)

    // The four that share the Activity tab.
    #expect(DashboardRoute.tab(for: .guiActions) == .activity)
    #expect(DashboardRoute.tab(for: .gitActivity) == .activity)
    #expect(DashboardRoute.tab(for: .mcpStatus) == .activity)
    #expect(DashboardRoute.tab(for: .aiUsage) == .activity)
    #expect(DashboardRoute.activitySection(for: .guiActions) == .actions)
    #expect(DashboardRoute.activitySection(for: .gitActivity) == .git)
    #expect(DashboardRoute.activitySection(for: .mcpStatus) == .mcp)
    #expect(DashboardRoute.activitySection(for: .aiUsage) == .aiUsage)
}

@Test("DashboardRoute: a target lands in at most one section group")
func deepLinksRequestAtMostOneGroupSection() {
    #expect(DashboardRoute.activitySection(for: .lintResults) == nil)
    #expect(DashboardRoute.activitySection(for: .notePolish) == nil)
    #expect(DashboardRoute.notesSection(for: .guiActions) == nil)
    #expect(DashboardRoute.notesSection(for: .gitActivity) == nil)
    #expect(DashboardRoute.notesSection(for: .mcpStatus) == nil)
    #expect(DashboardRoute.notesSection(for: .aiUsage) == nil)
}

@Test("Notes and Activity keep every section they contain")
func groupSectionsCoverAbsorbedViews() {
    #expect(NotesView.Section.allCases.map(\.rawValue) == ["Links & Schema", "AI Polish"])
    #expect(ActivityView.Section.allCases.map(\.rawValue) == ["Actions & Status", "Git", "MCP Server", "AI Usage & Cost"])
    for s in NotesView.Section.allCases { #expect(!s.rawValue.isEmpty) }
    for s in ActivityView.Section.allCases { #expect(!s.rawValue.isEmpty) }
}
