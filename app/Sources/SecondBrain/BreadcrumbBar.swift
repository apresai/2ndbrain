import SwiftUI

struct BreadcrumbBar: View {
    @Environment(AppState.self) var appState

    var body: some View {
        if let tab = appState.currentDocument, let vault = appState.vault {
            HStack(spacing: 4) {
                let segments = pathSegments(url: tab.url, root: vault.rootURL)

                ForEach(Array(segments.enumerated()), id: \.offset) { index, segment in
                    if index > 0 {
                        Image(systemName: "chevron.right")
                            .font(.system(size: 8))
                            .foregroundStyle(.quaternary)
                    }

                    Text(segment)
                        .font(.caption)
                        .foregroundStyle(index == segments.count - 1 ? .secondary : .tertiary)
                }

                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 3)
            .frame(height: 22)
            .background(Color(nsColor: .controlBackgroundColor))
            .overlay(alignment: .bottom) { Divider() }
        }
    }

    private func pathSegments(url: URL, root: URL) -> [String] {
        let rootPath = root.standardizedFileURL.path
        let filePath = url.standardizedFileURL.path

        let filename = url.deletingPathExtension().lastPathComponent

        guard filePath.hasPrefix(rootPath) else {
            return [filename]
        }

        var relative = String(filePath.dropFirst(rootPath.count))
        if relative.hasPrefix("/") { relative = String(relative.dropFirst()) }

        let vaultName = root.lastPathComponent
        var segments = [vaultName]
        let parts = relative.split(separator: "/").map(String.init)
        // Replace last segment (filename with extension) with extension-less version
        segments += parts.dropLast()
        segments.append(filename)
        return segments
    }
}
