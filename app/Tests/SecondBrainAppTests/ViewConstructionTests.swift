import SwiftUI
import Testing
@testable import SecondBrain

@Test("Beta dashboard views can be constructed")
@MainActor
func betaDashboardViewsConstruct() {
    let home = HomeView()
    let askSearch = AskSearchView()
    let notePolish = NotePolishView()
    let notes = NotesView(section: .constant(.linksAndSchema))
    let activity = ActivityView(section: .constant(.actions))
    let guiActions = GUIActionsView()
    let lint = LintResultsView(isPresented: .constant(true), isInline: true)
    let git = GitActivityView(isPresented: .constant(true), isInline: true)
    let mcp = MCPStatusView(isPresented: .constant(true), isInline: true)
    let aiUsage = AIUsageView()
    let badge = BetaBadge()

    #expect(String(describing: type(of: home)) == "HomeView")
    #expect(String(describing: type(of: askSearch)) == "AskSearchView")
    #expect(String(describing: type(of: notePolish)) == "NotePolishView")
    #expect(String(describing: type(of: notes)) == "NotesView")
    #expect(String(describing: type(of: activity)) == "ActivityView")
    #expect(String(describing: type(of: guiActions)) == "GUIActionsView")
    #expect(String(describing: type(of: lint)) == "LintResultsView")
    #expect(String(describing: type(of: git)) == "GitActivityView")
    #expect(String(describing: type(of: mcp)) == "MCPStatusView")
    #expect(String(describing: type(of: aiUsage)) == "AIUsageView")
    #expect(String(describing: type(of: badge)) == "BetaBadge")
}

@Test("Settings window views can be constructed")
@MainActor
func settingsViewsConstruct() {
    let settings = SettingsView()
    let ai = SettingsAIView()
    let integrations = SettingsIntegrationsView()
    let general = SettingsGeneralView()
    let updates = UpdatesView()
    let embeddedUpdates = UpdatesView(embedded: true)

    #expect(String(describing: type(of: settings)) == "SettingsView")
    #expect(String(describing: type(of: ai)) == "SettingsAIView")
    #expect(String(describing: type(of: integrations)) == "SettingsIntegrationsView")
    #expect(String(describing: type(of: general)) == "SettingsGeneralView")
    #expect(String(describing: type(of: updates)) == "UpdatesView")
    #expect(String(describing: type(of: embeddedUpdates)) == "UpdatesView")
}
