import Foundation

/// Finds the Homebrew binary so Home's drift banner can offer a real
/// "Update CLI" button instead of only a copyable command. Checks the two
/// standard install prefixes (Apple Silicon, then Intel); PATH lookup is
/// useless here because a launchd-launched GUI app has no shell PATH.
enum BrewLocator {
    static let candidates = ["/opt/homebrew/bin/brew", "/usr/local/bin/brew"]

    /// The first existing brew path, or nil when Homebrew isn't installed.
    /// The file-exists check is injectable for tests.
    static func resolve(
        fileExists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }
    ) -> String? {
        candidates.first(where: fileExists)
    }
}
