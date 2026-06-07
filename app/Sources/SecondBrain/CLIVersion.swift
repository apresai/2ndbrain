import Foundation

/// Compares the installed `2nb` CLI version against this app's version so Home
/// can warn when the two have drifted apart.
///
/// A stale CLI is exactly the failure mode behind the 0.5.8 re-embed bug:
/// `brew upgrade --cask secondbrain` bumps the app but does NOT bump the
/// `twonb` formula (a cask's `depends_on formula` only guarantees the CLI is
/// installed, not current), so a user can run app 0.5.8 against CLI 0.5.4 —
/// which lacks the empty-note skip and fails the re-embed with no hint as to
/// why. The app and CLI share the same `VERSION` file, so a matched release
/// has identical `major.minor.build`; any drift means one is behind.
enum CLIVersion {
    /// Parse a `major.minor.build` triple out of a `2nb --version` line
    /// ("2nb version 0.5.8"), a tagged form ("v0.5.8"), or a bare "0.5.8".
    /// Returns nil if no `N.N.N` token is present.
    static func parse(_ raw: String?) -> (Int, Int, Int)? {
        guard let raw else { return nil }
        for token in raw.split(whereSeparator: { " \n\t\r".contains($0) }) {
            let t = token.hasPrefix("v") ? token.dropFirst() : token
            let parts = t.split(separator: ".")
            guard parts.count == 3,
                  let major = Int(parts[0]),
                  let minor = Int(parts[1]),
                  let build = Int(parts[2]) else { continue }
            return (major, minor, build)
        }
        return nil
    }

    /// True when the CLI version is strictly older than the app version.
    /// Returns false when either side can't be parsed — never cry wolf on an
    /// unreadable version string, and never warn when the CLI is newer (a
    /// dev/local build), which isn't the failure direction.
    static func isOlder(cli: String?, thanApp app: String) -> Bool {
        guard let c = parse(cli), let a = parse(app) else { return false }
        if c.0 != a.0 { return c.0 < a.0 }
        if c.1 != a.1 { return c.1 < a.1 }
        return c.2 < a.2
    }
}
