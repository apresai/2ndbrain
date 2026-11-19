import Foundation

/// Safety validation outcome for a test vault path under test mode (`2NB_TEST=1`).
enum TestVaultSafetyResult: Equatable, Sendable {
    case safe(resolvedPath: String)
    case empty
    case matchesProtectedObsidian(raw: String, resolved: String)
    case outsideTempDirectory(raw: String, resolved: String)
}

/// Parsed launch flags and environment overrides used for isolated GUI testing
/// and deterministic action-log routing.
struct LaunchOptions: Equatable, Sendable {
    var vaultPath: String?
    var skipOnboarding: Bool
    var noStartupSync: Bool
    var guiLogDir: URL?
    var guiControlDir: URL?

    static let current = LaunchOptions.parse()

    /// Validates that a test vault path is non-empty, strictly resides within /tmp or /private/tmp,
    /// and never points to or contains /dev/obsidian, dereferencing all symbolic links.
    static func validateTestVaultSafety(_ rawPath: String) -> TestVaultSafetyResult {
        let trimmed = rawPath.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            return .empty
        }

        var current = trimmed
        var visited = Set<String>()
        var hopCount = 0
        var allPaths: [String] = [trimmed]

        while hopCount < 40 {
            guard let dest = try? FileManager.default.destinationOfSymbolicLink(atPath: current) else {
                break
            }
            let next: String
            if dest.hasPrefix("/") {
                next = dest
            } else {
                next = URL(fileURLWithPath: current).deletingLastPathComponent().appendingPathComponent(dest).standardizedFileURL.path
            }
            allPaths.append(next)
            if visited.contains(next) || next == current {
                // Circular symlink loop detected
                return .outsideTempDirectory(raw: trimmed, resolved: next)
            }
            visited.insert(current)
            current = next
            hopCount += 1
        }
        if hopCount >= 40 {
            return .outsideTempDirectory(raw: trimmed, resolved: current)
        }

        let resolvedURL = URL(fileURLWithPath: current).resolvingSymlinksInPath().standardizedFileURL
        let resolved = resolvedURL.path
        allPaths.append(resolved)

        let isProtected = allPaths.contains { p in
            p.contains("/dev/obsidian") ||
            (p.contains("obsidian") && (p.contains("/dev/") || p.contains("Application Support")))
        }

        if isProtected {
            return .matchesProtectedObsidian(raw: trimmed, resolved: resolved)
        }
        for p in allPaths {
            if !p.hasPrefix("/tmp/") && !p.hasPrefix("/private/tmp/") {
                return .outsideTempDirectory(raw: trimmed, resolved: p)
            }
        }
        return .safe(resolvedPath: resolved)
    }

    /// Asserts that a vault path is safe for testing, terminating via `fatalError` if violated.
    static func assertSafeTestVaultPath(_ rawPath: String) {
        switch validateTestVaultSafety(rawPath) {
        case .safe:
            break
        case .empty:
            fatalError("FATAL GUARDRAIL VIOLATION: Test vault path is empty")
        case .matchesProtectedObsidian(let raw, let resolved):
            fatalError("FATAL GUARDRAIL VIOLATION: Test vault cannot target personal Obsidian repository: \(raw) (resolved: \(resolved))")
        case .outsideTempDirectory(let raw, let resolved):
            fatalError("FATAL GUARDRAIL VIOLATION: Test vault must reside in /tmp or /private/tmp: \(raw) (resolved: \(resolved))")
        }
    }

    static func parse(
        arguments: [String] = CommandLine.arguments,
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> LaunchOptions {
        var vaultPath = trimmedNonEmpty(environment["2NB_GUI_VAULT"])
        var skipOnboarding = environment["2NB_GUI_SKIP_ONBOARDING"] == "1"
        var noStartupSync = environment["2NB_GUI_NO_STARTUP_SYNC"] == "1"
        var guiLogDirPath = trimmedNonEmpty(environment["2NB_GUI_LOG_DIR"])
        var guiControlDirPath = trimmedNonEmpty(environment["2NB_GUI_CONTROL_DIR"])

        var i = 1
        while i < arguments.count {
            let arg = arguments[i]
            switch arg {
            case "--vault":
                if i + 1 < arguments.count {
                    vaultPath = trimmedNonEmpty(arguments[i + 1])
                    i += 1
                }
            case "--gui-log-dir":
                if i + 1 < arguments.count {
                    guiLogDirPath = trimmedNonEmpty(arguments[i + 1])
                    i += 1
                }
            case "--gui-control-dir":
                if i + 1 < arguments.count {
                    guiControlDirPath = trimmedNonEmpty(arguments[i + 1])
                    i += 1
                }
            case "--skip-onboarding":
                skipOnboarding = true
            case "--no-startup-sync":
                noStartupSync = true
            default:
                if let value = valueAfterPrefix("--vault=", in: arg) {
                    vaultPath = trimmedNonEmpty(value)
                } else if let value = valueAfterPrefix("--gui-log-dir=", in: arg) {
                    guiLogDirPath = trimmedNonEmpty(value)
                } else if let value = valueAfterPrefix("--gui-control-dir=", in: arg) {
                    guiControlDirPath = trimmedNonEmpty(value)
                }
            }
            i += 1
        }

        if environment["2NB_TEST"] == "1", let path = vaultPath {
            assertSafeTestVaultPath(path)
        }

        return LaunchOptions(
            vaultPath: vaultPath,
            skipOnboarding: skipOnboarding,
            noStartupSync: noStartupSync,
            guiLogDir: guiLogDirPath.map { URL(fileURLWithPath: $0, isDirectory: true) },
            guiControlDir: guiControlDirPath.map { URL(fileURLWithPath: $0, isDirectory: true) }
        )
    }

    private static func trimmedNonEmpty(_ raw: String?) -> String? {
        guard let raw else { return nil }
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    private static func valueAfterPrefix(_ prefix: String, in arg: String) -> String? {
        guard arg.hasPrefix(prefix) else { return nil }
        return String(arg.dropFirst(prefix.count))
    }
}
