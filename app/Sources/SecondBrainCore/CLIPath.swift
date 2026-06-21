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

    /// The PATH a child process should see: `current` with the directories a
    /// Homebrew or Go install lives in prepended (deduped, order preserved).
    ///
    /// The app is launched by launchd (Finder / the dock) with a minimal PATH
    /// that lacks `/opt/homebrew/bin`, so a bundled `2nb` running
    /// `exec.LookPath("2nb")` — as `skills doctor` does to verify the terminal
    /// can find the CLI — would wrongly report "2nb is NOT on your shell PATH"
    /// even when it is. Pure and `home`-injectable so it can be unit-tested.
    public static func augmentedPATH(current: String, home: String) -> String {
        let preferred = ["/opt/homebrew/bin", "/usr/local/bin", "\(home)/go/bin"]
        let existing = current.split(separator: ":", omittingEmptySubsequences: true).map(String.init)
        let existingSet = Set(existing)
        let prepend = preferred.filter { !existingSet.contains($0) }
        return (prepend + existing).joined(separator: ":")
    }

    /// Prepend the Homebrew/Go bin directories to THIS process's PATH so every
    /// child process the app spawns (the `2nb` CLI, `brew`) inherits a
    /// login-shell-like PATH. Call once at app launch, before any CLI call.
    /// Idempotent — re-running never duplicates an entry. Returns the PATH it
    /// applied so the caller can log it for support (a "still says not on PATH"
    /// report is diagnosed by seeing exactly what the children inherit).
    @discardableResult
    public static func ensureToolPATH() -> String {
        let current = ProcessInfo.processInfo.environment["PATH"] ?? ""
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let augmented = augmentedPATH(current: current, home: home)
        setenv("PATH", augmented, 1)
        return augmented
    }

    /// Pin the CLI's vault resolution to `vault` by prepending `--vault <path>`,
    /// the highest-priority vault source (overrides ~/.2ndbrain-active-vault,
    /// 2NB_VAULT env, and cwd). Use this for every CLI call from the app so
    /// drift in the global active-vault file can't redirect commands.
    public static func args(_ args: [String], vault: URL) -> [String] {
        ["--vault", vault.path] + args
    }
}
