import SwiftUI

/// A simple line-based unified diff viewer.
/// Reusable by PolishView and (later) the merge-conflict dialog and git diff viewer.
struct DiffView: View {
    let original: String
    let modified: String

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(diffLines.enumerated()), id: \.offset) { _, line in
                    HStack(alignment: .top, spacing: 0) {
                        Text(line.marker)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(line.markerColor)
                            .frame(width: 16, alignment: .center)
                        Text(line.text.isEmpty ? " " : line.text)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 1)
                    .background(line.background)
                }
            }
            .padding(.vertical, 4)
        }
    }

    private var diffLines: [DiffLine] {
        let originalLines = original.components(separatedBy: "\n")
        let modifiedLines = modified.components(separatedBy: "\n")
        return myersDiff(originalLines, modifiedLines)
    }
}

private struct DiffLine {
    enum Kind {
        case unchanged
        case added
        case removed
    }

    let kind: Kind
    let text: String

    var marker: String {
        switch kind {
        case .unchanged: return " "
        case .added: return "+"
        case .removed: return "-"
        }
    }

    var markerColor: Color {
        switch kind {
        case .unchanged: return .secondary
        case .added: return .green
        case .removed: return .red
        }
    }

    var background: Color {
        switch kind {
        case .unchanged: return .clear
        case .added: return Color.green.opacity(0.12)
        case .removed: return Color.red.opacity(0.12)
        }
    }
}

/// Myers diff on string arrays. For polish/git/merge-conflict use cases the
/// inputs are small (one document) so a straightforward LCS-based diff is fine.
private func myersDiff(_ a: [String], _ b: [String]) -> [DiffLine] {
    let n = a.count
    let m = b.count

    // Build LCS length table. O(n*m) time and space — acceptable for docs.
    var dp = Array(repeating: Array(repeating: 0, count: m + 1), count: n + 1)
    for i in 0..<n {
        for j in 0..<m {
            if a[i] == b[j] {
                dp[i + 1][j + 1] = dp[i][j] + 1
            } else {
                dp[i + 1][j + 1] = max(dp[i + 1][j], dp[i][j + 1])
            }
        }
    }

    // Backtrack to produce a unified diff in forward order.
    var result: [DiffLine] = []
    var i = n
    var j = m
    while i > 0 || j > 0 {
        if i > 0 && j > 0 && a[i - 1] == b[j - 1] {
            result.append(DiffLine(kind: .unchanged, text: a[i - 1]))
            i -= 1
            j -= 1
        } else if j > 0 && (i == 0 || dp[i][j - 1] >= dp[i - 1][j]) {
            result.append(DiffLine(kind: .added, text: b[j - 1]))
            j -= 1
        } else if i > 0 {
            result.append(DiffLine(kind: .removed, text: a[i - 1]))
            i -= 1
        }
    }
    return result.reversed()
}
