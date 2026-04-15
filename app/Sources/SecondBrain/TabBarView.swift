import SwiftUI
import AppKit

struct TabBarView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 0) {
                ForEach(Array(appState.openDocuments.enumerated()), id: \.element.id) { index, tab in
                    TabButton(
                        title: tab.title,
                        isDirty: tab.isDirty,
                        isActive: index == appState.activeTabIndex,
                        onSelect: { appState.activeTabIndex = index },
                        onClose: { appState.closeTab(id: tab.id) },
                        onCloseOthers: {
                            // Close every tab except this one. Iterate from
                            // the end to keep indices stable during removal.
                            for i in stride(from: appState.openDocuments.count - 1, through: 0, by: -1) {
                                if appState.openDocuments[i].id != tab.id {
                                    appState.closeTab(id: appState.openDocuments[i].id)
                                }
                            }
                        },
                        onCloseAll: {
                            for i in stride(from: appState.openDocuments.count - 1, through: 0, by: -1) {
                                appState.closeTab(id: appState.openDocuments[i].id)
                            }
                        },
                        onReveal: {
                            NSWorkspace.shared.activateFileViewerSelecting([tab.url])
                        }
                    )
                }
            }
        }
        .frame(height: 32)
        .background(Color(nsColor: .controlBackgroundColor))
        .overlay(alignment: .bottom) {
            Divider()
        }
    }
}

struct TabButton: View {
    let title: String
    let isDirty: Bool
    let isActive: Bool
    let onSelect: () -> Void
    let onClose: () -> Void
    let onCloseOthers: () -> Void
    let onCloseAll: () -> Void
    let onReveal: () -> Void

    @State private var isHovering = false

    var body: some View {
        HStack(spacing: 4) {
            if isDirty {
                Circle()
                    .fill(.primary.opacity(0.5))
                    .frame(width: 6, height: 6)
            }

            Text(title)
                .font(.callout)
                .lineLimit(1)

            Button(action: onClose) {
                Image(systemName: "xmark")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .opacity(isHovering ? 1 : 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(isActive ? Color(nsColor: .controlBackgroundColor) : Color.clear)
        .overlay(alignment: .bottom) {
            if isActive {
                Rectangle()
                    .fill(Color.accentColor)
                    .frame(height: 2)
            }
        }
        .contentShape(Rectangle())
        .onTapGesture(perform: onSelect)
        .contextMenu {
            Button("Close") { onClose() }
            Button("Close Others") { onCloseOthers() }
                .disabled(false)
            Button("Close All") { onCloseAll() }
            Divider()
            Button("Reveal in Finder") { onReveal() }
        }
        .onHover { isHovering = $0 }
    }
}
