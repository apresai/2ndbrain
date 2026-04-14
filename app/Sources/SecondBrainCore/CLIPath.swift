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
}
