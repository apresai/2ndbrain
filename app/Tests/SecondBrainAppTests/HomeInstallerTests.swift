import Foundation
import Testing
@testable import SecondBrain

// MARK: - HomePlugin (Obsidian plugin row on the Vault card)

@Test("HomePlugin.rowState offers Install when missing, Update when older, nothing when current or newer")
func homePluginRowState() {
    let missing = HomePlugin.rowState(installed: nil, appVersion: "0.8.0")
    #expect(missing.label == "not installed")
    #expect(missing.button == "Install")

    let older = HomePlugin.rowState(installed: "0.7.0", appVersion: "0.8.0")
    #expect(older.label == "v0.7.0 (update available)")
    #expect(older.button == "Update")

    let current = HomePlugin.rowState(installed: "0.8.0", appVersion: "0.8.0")
    #expect(current.label == "v0.8.0")
    #expect(current.button == nil)

    // Newer than the app (a dev/BRAT build): installing the latest release
    // can't help, so no button.
    let newer = HomePlugin.rowState(installed: "0.9.0", appVersion: "0.8.0")
    #expect(newer.label == "v0.9.0")
    #expect(newer.button == nil)

    // Non-semver manifest versions show raw (no "v" prefix) and no button:
    // CLIVersion.isOlder is deliberately false on unparseable input.
    let dev = HomePlugin.rowState(installed: "dev", appVersion: "0.8.0")
    #expect(dev.label == "dev")
    #expect(dev.button == nil)
    let twoPart = HomePlugin.rowState(installed: "0.8", appVersion: "0.8.0")
    #expect(twoPart.label == "0.8")
    #expect(twoPart.button == nil)
}

@Test("HomePlugin.successMessage includes the enable step only on first install")
func homePluginSuccessMessage() {
    let fresh = HomePlugin.successMessage(updated: false)
    #expect(fresh.contains("enable"))
    #expect(fresh.contains("Community plugins"))

    let updated = HomePlugin.successMessage(updated: true)
    #expect(updated.contains("Reload Obsidian"))
    #expect(!updated.contains("enable"))
}

// MARK: - HomeCLIUpdate (Update CLI button result message)

@Test("HomeCLIUpdate.resultMessage distinguishes a real update from a no-op brew run")
func homeCLIUpdateResultMessage() {
    #expect(HomeCLIUpdate.resultMessage(before: "0.7.0", after: "0.8.0") == "CLI updated to 0.8.0.")

    // brew exits 0 with nothing to do: must not claim an update happened.
    let noop = HomeCLIUpdate.resultMessage(before: "0.7.0", after: "0.7.0")
    #expect(noop.contains("unchanged at 0.7.0"))
    #expect(!noop.contains("updated to"))

    // Version unreadable on both sides still produces a sane message.
    let unknown = HomeCLIUpdate.resultMessage(before: nil, after: nil)
    #expect(unknown.contains("unknown"))
}

// MARK: - BrewLocator (Update CLI button on the drift banner)

@Test("BrewLocator prefers the Apple Silicon prefix, falls back to Intel, nil when absent")
func brewLocatorResolution() {
    #expect(BrewLocator.resolve(fileExists: { _ in true }) == "/opt/homebrew/bin/brew")
    #expect(BrewLocator.resolve(fileExists: { $0 == "/usr/local/bin/brew" }) == "/usr/local/bin/brew")
    #expect(BrewLocator.resolve(fileExists: { _ in false }) == nil)
}
