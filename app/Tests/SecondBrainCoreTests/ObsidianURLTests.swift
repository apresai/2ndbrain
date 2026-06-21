import Foundation
import Testing
@testable import SecondBrainCore

@Test("openURL builds obsidian://open with vault + file, stripping a trailing .md")
func obsidianURLBasic() {
    let url = ObsidianURL.openURL(vaultName: "MyVault", relativePath: "projects/roadmap.md")
    #expect(url?.absoluteString == "obsidian://open?vault=MyVault&file=projects/roadmap")
}

@Test("openURL keeps '/' separators and percent-encodes spaces as %20")
func obsidianURLSpacesAndSlashes() {
    let url = ObsidianURL.openURL(vaultName: "My Vault", relativePath: "Daily Notes/2026 06 20.md")
    #expect(url?.absoluteString == "obsidian://open?vault=My%20Vault&file=Daily%20Notes/2026%2006%2020")
}

@Test("openURL preserves non-.md extensions (.canvas/.base)")
func obsidianURLNonMarkdown() {
    let canvas = ObsidianURL.openURL(vaultName: "V", relativePath: "boards/plan.canvas")
    #expect(canvas?.absoluteString == "obsidian://open?vault=V&file=boards/plan.canvas")
}

@Test("openURL encodes query sub-delimiters that would break parsing")
func obsidianURLSubDelimiters() {
    // A note whose name contains & = ? # + ; must be encoded, or the query splits.
    let url = ObsidianURL.openURL(vaultName: "V", relativePath: "notes/a&b=c?d#e+f;g.md")
    let s = url?.absoluteString ?? ""
    #expect(s.hasPrefix("obsidian://open?vault=V&file=notes/"))
    #expect(!s.contains("a&b"))      // the literal & must be encoded
    #expect(s.contains("%26"))        // &
    #expect(s.contains("%3D"))        // =
    #expect(s.contains("%3F"))        // ?
    #expect(s.contains("%23"))        // #
    #expect(s.contains("%2B"))        // +  (would otherwise be read as space)
    #expect(s.contains("%3B"))        // ;
}

@Test("openURL strips a trailing .md case-insensitively")
func obsidianURLUppercaseMD() {
    #expect(ObsidianURL.openURL(vaultName: "V", relativePath: "Notes/PLAN.MD")?.absoluteString
        == "obsidian://open?vault=V&file=Notes/PLAN")
}

@Test("openURL is nil for empty vault or empty/.md-only path")
func obsidianURLEmpty() {
    #expect(ObsidianURL.openURL(vaultName: "", relativePath: "a.md") == nil)
    #expect(ObsidianURL.openURL(vaultName: "  ", relativePath: "a.md") == nil)
    #expect(ObsidianURL.openURL(vaultName: "V", relativePath: "") == nil)
    #expect(ObsidianURL.openURL(vaultName: "V", relativePath: ".md") == nil)
}

@Test("openURL handles unicode in the path")
func obsidianURLUnicode() {
    let url = ObsidianURL.openURL(vaultName: "V", relativePath: "café/notes.md")
    // é is percent-encoded; the result must round-trip back to the original.
    let comps = url.flatMap { URLComponents(url: $0, resolvingAgainstBaseURL: false) }
    let file = comps?.queryItems?.first(where: { $0.name == "file" })?.value
    #expect(file == "café/notes")
}
