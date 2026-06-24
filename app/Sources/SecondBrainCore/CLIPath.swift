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

    /// Best-effort removal of the `com.apple.quarantine` xattr from one file.
    /// Returns true when the xattr is gone afterwards (removed now, or never
    /// present — `ENOATTR` is success for our purposes). Factored out so it is
    /// unit-testable against a temp file (the app bundle isn't present in tests).
    @discardableResult
    public static func clearQuarantine(at path: String) -> Bool {
        path.withCString { cPath in
            removexattr(cPath, "com.apple.quarantine", 0) == 0 || errno == ENOATTR
        }
    }

    /// Strip `com.apple.quarantine` from the bundled `2nb` so the app can spawn
    /// it without Gatekeeper independently blocking it. Call once at launch,
    /// before any CLI spawn.
    ///
    /// A user's download/install (browser, or `brew install --cask`, which copies
    /// via `ditto`) applies the quarantine xattr recursively to every file in the
    /// bundle — including `Contents/Resources/2nb`. The app bundle has a stapled
    /// notarization ticket and launches fine, but a standalone Mach-O binary
    /// *cannot* carry a stapled ticket (Apple limitation), so a quarantined `2nb`
    /// is verified ONLINE at spawn time. When that check fails (offline, or the
    /// notarization daemon errors) macOS denies it — "Apple could not verify
    /// '2nb' is free of malware … Move to Trash" — which breaks the whole app,
    /// since it shells out to `2nb` for everything. Clearing the quarantine xattr
    /// resolves it and is safe for the code signature, which excludes
    /// `com.apple.*` system xattrs from the sealed resources. Mirrors what the
    /// Obsidian plugin already does for its downloaded `2nb`.
    ///
    /// No-op for non-bundled runs (dev/tests) and best-effort otherwise: a
    /// translocated run or a non-writable bundle just leaves the xattr in place
    /// (the user can clear it with `xattr -dr com.apple.quarantine <app>`).
    ///
    /// Returns `nil` when there is no bundled CLI (dev/test run), otherwise
    /// whether the quarantine xattr is now absent (cleared or never present).
    /// SecondBrainCore stays logger-free; the caller logs the outcome so a
    /// support report can answer "did the quarantine strip succeed?".
    @discardableResult
    public static func prepareBundledCLI() -> Bool? {
        guard let bundled = bundledPath else { return nil }
        return clearQuarantine(at: bundled)
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
    /// the highest-priority vault source (overrides 2NB_VAULT env, the open
    /// Obsidian vault registry, and cwd). Use this for every CLI call from the
    /// app so the command always operates on the vault the dashboard is bound to.
    public static func args(_ args: [String], vault: URL) -> [String] {
        ["--vault", vault.path] + args
    }
}
