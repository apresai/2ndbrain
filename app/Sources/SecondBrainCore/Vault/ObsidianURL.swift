import Foundation
#if canImport(AppKit)
import AppKit
#endif

/// Builds `obsidian://` deep links and opens notes in Obsidian, with a
/// default-app fallback when Obsidian can't handle the URL (not installed, no
/// registered handler). 2ndbrain is not an editor — when a user wants to fix a
/// note, the right place to send them is Obsidian, not Finder.
public enum ObsidianURL {
    /// Characters allowed verbatim inside an `obsidian://` query value. Starts
    /// from the URL query-allowed set and removes the sub-delimiters that would
    /// otherwise be read as query structure (`&` `=` `+` `?` `#` `;`). `/` is
    /// intentionally kept so a vault-relative path stays human-readable, and
    /// space (absent from the base set) is percent-encoded to `%20`.
    private static let queryValueAllowed: CharacterSet = {
        var allowed = CharacterSet.urlQueryAllowed
        allowed.remove(charactersIn: "&=+?#;")
        return allowed
    }()

    /// Build `obsidian://open?vault=<name>&file=<path>` for a note.
    ///
    /// - Parameters:
    ///   - vaultName: the Obsidian vault name (its folder basename, or the name
    ///     Obsidian's registry reports for it).
    ///   - relativePath: the vault-relative path to the note (e.g.
    ///     `"projects/roadmap.md"`). A single trailing `.md` is stripped because
    ///     Obsidian addresses a note by name, not filename; other extensions
    ///     (`.canvas`, `.base`) and all `/` separators are preserved.
    /// - Returns: the deep-link URL, or `nil` if either component is empty or
    ///   can't be percent-encoded.
    public static func openURL(vaultName: String, relativePath: String) -> URL? {
        let trimmedVault = vaultName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedVault.isEmpty else { return nil }

        var file = relativePath
        if file.lowercased().hasSuffix(".md") {
            file = String(file.dropLast(3))
        }
        guard !file.isEmpty else { return nil }

        guard
            let vaultEnc = trimmedVault.addingPercentEncoding(withAllowedCharacters: queryValueAllowed),
            let fileEnc = file.addingPercentEncoding(withAllowedCharacters: queryValueAllowed)
        else { return nil }

        return URL(string: "obsidian://open?vault=\(vaultEnc)&file=\(fileEnc)")
    }

    #if canImport(AppKit)
    /// Open a note in Obsidian. Falls back to opening `absoluteFileURL` in the
    /// default handler (preserving the prior Finder/editor behavior) when the
    /// `obsidian://` URL can't be built or no app handles it.
    ///
    /// - Returns: `true` if Obsidian (or some `obsidian://` handler) accepted
    ///   the deep link; `false` if it fell back to the file URL.
    @discardableResult
    @MainActor
    public static func open(vaultName: String, relativePath: String, absoluteFileURL: URL) -> Bool {
        if let url = openURL(vaultName: vaultName, relativePath: relativePath),
           NSWorkspace.shared.open(url) {
            return true
        }
        NSWorkspace.shared.open(absoluteFileURL)
        return false
    }
    #endif
}
