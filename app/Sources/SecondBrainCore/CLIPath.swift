import Foundation

public enum CLIPath {
    public static func resolve() -> String {
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
