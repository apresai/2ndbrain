import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Soft material design tokens and shared UI helpers for
/// 2ndbrain's macOS workspace.
enum AppTheme {
    static let cardRadius: CGFloat = 12
    static let panelRadius: CGFloat = 18
    static let compactRadius: CGFloat = 9
    static let hairlineOpacity: Double = 0.09
    static let prominentBorderOpacity: Double = 0.16
    static let shadowOpacity: Double = 0.08
    static let shadowRadius: CGFloat = 16
    static let shadowY: CGFloat = 4

    static let geminiAccent = LinearGradient(
        colors: [Color.accentColor, Color.indigo],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )
}

/// Soft material surface container with subtle border and elevation.
struct ThemedCard<Content: View>: View {
    enum Style {
        case standard
        case prominent
        case subtle
    }

    let style: Style
    let padding: CGFloat
    let hoverEffect: Bool
    @ViewBuilder let content: () -> Content

    @State private var isHovered = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var colorScheme

    init(
        style: Style = .standard,
        padding: CGFloat = 16,
        hoverEffect: Bool = false,
        @ViewBuilder content: @escaping () -> Content
    ) {
        self.style = style
        self.padding = padding
        self.hoverEffect = hoverEffect
        self.content = content
    }

    var body: some View {
        content()
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(backgroundView)
            .overlay(borderView)
            .clipShape(RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .shadow(
                color: Color.black.opacity(shadowOpacity),
                radius: style == .prominent ? AppTheme.shadowRadius : 10,
                x: 0,
                y: AppTheme.shadowY
            )
            .scaleEffect(hoverEffect && isHovered && !reduceMotion ? 1.008 : 1.0)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.15), value: isHovered)
            .onHover { hovering in
                guard hoverEffect else { return }
                isHovered = hovering
            }
    }

    private var cornerRadius: CGFloat {
        style == .subtle ? AppTheme.compactRadius : AppTheme.cardRadius
    }

    @ViewBuilder
    private var backgroundView: some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        switch style {
        case .standard:
            shape.fill(.regularMaterial)
        case .prominent:
            shape.fill(.thinMaterial)
                .overlay(shape.fill(Color.accentColor.opacity(colorScheme == .dark ? 0.08 : 0.05)))
        case .subtle:
            shape.fill(Color.primary.opacity(colorScheme == .dark ? 0.045 : 0.035))
        }
    }

    private var borderView: some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        let opacity: Double = switch style {
        case .prominent: AppTheme.prominentBorderOpacity
        case .standard: AppTheme.hairlineOpacity
        case .subtle: 0.06
        }
        return shape.strokeBorder(
            style == .prominent
                ? Color.accentColor.opacity(0.28)
                : Color.primary.opacity(isHovered && hoverEffect ? 0.16 : opacity),
            lineWidth: 1
        )
    }

    private var shadowOpacity: Double {
        switch style {
        case .subtle: 0
        case .standard: colorScheme == .dark ? 0.18 : AppTheme.shadowOpacity
        case .prominent: colorScheme == .dark ? 0.24 : 0.11
        }
    }
}

/// Keyboard shortcut keycap pills (`⌃`, `⇧`, `Space`).
struct Keycaps: View {
    let caps: [String]
    var size: CGFloat = 11

    var body: some View {
        HStack(spacing: size * 0.28) {
            ForEach(Array(caps.enumerated()), id: \.offset) { _, cap in
                Text(cap)
                    .font(.system(size: size, weight: .medium, design: .rounded))
                    .frame(minWidth: size * 1.55)
                    .padding(.horizontal, size * 0.42)
                    .padding(.vertical, size * 0.24)
                    .background(
                        RoundedRectangle(cornerRadius: size * 0.42, style: .continuous)
                            .fill(Color.primary.opacity(0.07))
                    )
                    .overlay(
                        RoundedRectangle(cornerRadius: size * 0.42, style: .continuous)
                            .strokeBorder(Color.primary.opacity(0.14), lineWidth: 0.5)
                    )
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(caps.joined(separator: " "))
    }
}

/// Pure citation and markdown-preview helpers (shared by Ask & Search, the
/// floating HUD, and tests).
enum NoteCitation {
    /// Builds an Obsidian `[[wikilink]]` citation from a search hit.
    static func wikilink(path: String, title: String) -> String {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            return "[[\(trimmed)]]"
        }
        let filename = (path as NSString).lastPathComponent
        let base = (filename as NSString).deletingPathExtension
        return "[[\(base.isEmpty ? path : base)]]"
    }

    /// Safely strips a leading YAML frontmatter block by scanning fence lines
    /// rather than invoking `Yams.load` (which can crash on template placeholders),
    /// and caps output length so large notes stay fast in previews.
    static func previewBody(fromMarkdown raw: String, maxChars: Int = 24_000) -> String {
        let stripped = stripLeadingFrontmatter(raw)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard stripped.count > maxChars else { return stripped }
        let idx = stripped.index(stripped.startIndex, offsetBy: maxChars)
        return String(stripped[..<idx]) + "\n\n…"
    }

    private static func stripLeadingFrontmatter(_ raw: String) -> String {
        let lines = raw.split(separator: "\n", omittingEmptySubsequences: false)
        guard let first = lines.first, isFenceLine(first) else { return raw }
        for index in 1..<lines.count {
            if isFenceLine(lines[index]) {
                let remaining = lines[(index + 1)...].joined(separator: "\n")
                return remaining
            }
        }
        return raw
    }

    private static func isFenceLine(_ line: Substring) -> Bool {
        let trimmed = line.replacingOccurrences(of: "\r", with: "")
            .trimmingCharacters(in: .whitespaces)
        return trimmed == "---"
    }
}

/// Live Markdown note preview pane used by Hybrid Search and Quick Search HUD.
struct NotePreviewPane: View {
    let vaultRoot: URL?
    let result: CLISearchResult?
    let onOpenObsidian: (String) -> Void
    let onRevealFinder: (URL) -> Void
    let onCopyCitation: (CLISearchResult) -> Void

    @State private var noteText: String = ""
    @State private var loadError: String?

    var body: some View {
        if let result {
            VStack(alignment: .leading, spacing: 0) {
                header(for: result)
                Divider()
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        if let excerpt = result.content?.trimmingCharacters(in: .whitespacesAndNewlines),
                           !excerpt.isEmpty {
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Matched Passage")
                                    .font(.caption2.weight(.semibold))
                                    .foregroundStyle(.secondary)
                                    .textCase(.uppercase)
                                Text(excerpt)
                                    .font(.callout)
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                            }
                            .padding(10)
                            .background(Color.accentColor.opacity(0.08))
                            .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                        }

                        if let loadError {
                            Text(loadError)
                                .font(.callout)
                                .foregroundStyle(.secondary)
                        } else if noteText.isEmpty {
                            Text("Empty note body.")
                                .font(.callout)
                                .foregroundStyle(.secondary)
                        } else {
                            Text(noteText)
                                .font(.system(size: 13))
                                .lineSpacing(3)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                    .padding(16)
                }
            }
            .task(id: "\(result.id)|\(vaultRoot?.path ?? "")") {
                loadPreview(for: result)
            }
        } else {
            ContentUnavailableView(
                "Select a Note",
                systemImage: "doc.text.magnifyingglass",
                description: Text("Arrow through results to inspect note contents, open in Obsidian, or copy a [[wikilink]].")
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func header(for result: CLISearchResult) -> some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text(result.title.isEmpty ? result.path : result.title)
                    .font(.headline)
                    .lineLimit(1)
                HStack(spacing: 6) {
                    Text(result.path)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if let heading = result.headingPath, !heading.isEmpty {
                        Text("› \(heading)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
            }

            Spacer(minLength: 8)

            HStack(spacing: 6) {
                Button {
                    onCopyCitation(result)
                } label: {
                    Label("Copy [[Link]]", systemImage: "link")
                }
                .controlSize(.small)
                .help("Copy \(NoteCitation.wikilink(path: result.path, title: result.title))")

                if let fileURL = fileURL(for: result) {
                    Button {
                        onRevealFinder(fileURL)
                    } label: {
                        Image(systemName: "folder")
                    }
                    .controlSize(.small)
                    .help("Reveal in Finder (⌘↩)")
                }

                Button {
                    onOpenObsidian(result.path)
                } label: {
                    Label("Open", systemImage: "arrow.up.forward.app")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .help("Open in Obsidian (↩)")
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private func fileURL(for result: CLISearchResult) -> URL? {
        vaultRoot?.appendingPathComponent(result.path)
    }

    private func loadPreview(for result: CLISearchResult) {
        loadError = nil
        guard let url = fileURL(for: result) else {
            noteText = ""
            loadError = "No vault bound."
            return
        }
        do {
            let raw = try String(contentsOf: url, encoding: .utf8)
            noteText = NoteCitation.previewBody(fromMarkdown: raw)
        } catch {
            noteText = ""
            loadError = "Unable to read note from disk (\(error.localizedDescription))."
        }
    }
}
