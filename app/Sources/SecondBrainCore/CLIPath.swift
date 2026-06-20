import Foundation

public enum CLIPath {
    /// The 2nb binary bundled inside the app at Contents/Resources/2nb, if the
    /// app is running from a bundle that contains it. This is the version-matched
    /// CLI shipped alongside the app (see the Makefile build-app* targets), so it
    /// can never drift behind the app the way a Homebrew-installed `2nb` can: a
    /// cask upgrade bumps the app but not the `twonb` formula (the "0.5.8
    /// re-embed bug"). Returns nil for non-bundled runs (e.g. `swift run`, tests).
    public static var bundledPath: String? {
        let candidate = Bundle.main.bundlePath + "/Contents/Resources/2nb"
        return FileManager.default.isExecutableFile(atPath: candidate) ? candidate : nil
    }

    public static func resolve() -> String {
        // Prefer the bundled, version-matched binary so app-internal CLI calls
        // always match the app's feature set. Fall back to a Homebrew install
        // for non-bundled runs (dev, tests) and shell parity.
        if let bundled = bundledPath {
            return bundled
        }
        for path in ["/opt/homebrew/bin/2nb", "/usr/local/bin/2nb"] {
            if FileManager.default.isExecutableFile(atPath: path) {
                return path
            }
        }
        return "/usr/local/bin/2nb"
    }

    /// Pin the CLI's vault resolution to `vault` by prepending `--vault <path>`,
    /// the highest-priority vault source (overrides ~/.2ndbrain-active-vault,
    /// 2NB_VAULT env, and cwd). Use this for every CLI call from the app so
    /// drift in the global active-vault file can't redirect commands.
    public static func args(_ args: [String], vault: URL) -> [String] {
        ["--vault", vault.path] + args
    }
}
