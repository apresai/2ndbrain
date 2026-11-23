import SwiftUI

/// A simple line-based unified diff viewer.
/// Reusable by PolishView and (later) the merge-conflict dialog and git diff viewer.
struct DiffView: View {
    let original: String
    let modified: String

    /// Computed off the main actor, once per input pair. It used to be a
    /// computed property read by `body`, so every state change in the
    /// polish view (a toggle, a keystroke in the goal field) re-ran a full
    /// O(n x m) table on the main thread: tens of millions of steps and
    /// hundreds of megabytes for a long note.
    @State private var lines: [DiffLine]?
    @State private var linesKey: DiffInput?

    var body: some View {
        let key = DiffInput(original: original, modified: modified)
        ScrollView {
            if let lines {
                // Lazy: a long note has thousands of rows, and only the
                // visible ones need views. While a new diff computes, the
                // previous one stays (dimmed) rather than collapsing to a
                // spinner, which reset the scroll position.
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        row(line)
                    }
                }
                .padding(.vertical, 4)
                .opacity(linesKey == key ? 1 : 0.5)
            } else {
                ProgressView()
                    .controlSize(.small)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(12)
            }
        }
        .task(id: key) {
            // A detached task does not inherit cancellation, so a superseded
            // diff is cancelled explicitly; myersDiff checks once per step.
            let job = Task.detached(priority: .userInitiated) {
                lineDiff(key.original.components(separatedBy: "\n"),
                         key.modified.components(separatedBy: "\n"))
            }
            let computed = await withTaskCancellationHandler {
                await job.value
            } onCancel: {
                job.cancel()
            }
            guard !Task.isCancelled else { return }
            lines = computed
            linesKey = key
        }
    }

    private func row(_ line: DiffLine) -> some View {
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

private struct DiffInput: Hashable, Sendable {
    let original: String
    let modified: String
}

struct DiffLine: Equatable, Sendable {
    enum Kind: Equatable, Sendable {
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

/// Line diff in forward order. The common prefix and suffix are matched
/// first (a polish edit usually touches a few lines of a long note), and the
/// middle goes through Myers' O((N+M)D) algorithm, which replaced an O(N x M)
/// LCS table. Past `maxEdits` differences the middle is shown as removed then
/// added rather than aligned: still a correct diff, with memory bounded at
/// about maxEdits squared integers.
func lineDiff(_ a: [String], _ b: [String], maxEdits: Int = 1000) -> [DiffLine] {
    var prefix = 0
    while prefix < a.count, prefix < b.count, a[prefix] == b[prefix] { prefix += 1 }
    var suffix = 0
    while suffix < a.count - prefix, suffix < b.count - prefix,
          a[a.count - 1 - suffix] == b[b.count - 1 - suffix] { suffix += 1 }

    let midA = Array(a[prefix..<(a.count - suffix)])
    let midB = Array(b[prefix..<(b.count - suffix)])
    let middle = myersDiff(midA, midB, maxEdits: maxEdits)
        ?? midA.map { DiffLine(kind: .removed, text: $0) } + midB.map { DiffLine(kind: .added, text: $0) }

    return a[..<prefix].map { DiffLine(kind: .unchanged, text: $0) }
        + middle
        + a[(a.count - suffix)...].map { DiffLine(kind: .unchanged, text: $0) }
}

/// Myers' greedy shortest-edit-script diff. Returns nil when the inputs
/// differ by more than `maxEdits` lines.
private func myersDiff(_ a: [String], _ b: [String], maxEdits: Int) -> [DiffLine]? {
    let n = a.count
    let m = b.count
    if n == 0 { return b.map { DiffLine(kind: .added, text: $0) } }
    if m == 0 { return a.map { DiffLine(kind: .removed, text: $0) } }

    let maxD = min(n + m, maxEdits)
    let offset = n + m
    var v = [Int](repeating: 0, count: 2 * (n + m) + 2)
    // trace[d] holds v[-d...d] as it stood when step d began.
    var trace: [[Int]] = []

    for d in 0...maxD {
        if Task.isCancelled { return nil }
        trace.append(Array(v[(offset - d)...(offset + d)]))
        for k in stride(from: -d, through: d, by: 2) {
            var x: Int
            if k == -d || (k != d && v[offset + k - 1] < v[offset + k + 1]) {
                x = v[offset + k + 1]
            } else {
                x = v[offset + k - 1] + 1
            }
            var y = x - k
            while x < n, y < m, a[x] == b[y] {
                x += 1
                y += 1
            }
            v[offset + k] = x
            if x >= n, y >= m {
                return backtrack(a, b, trace: trace, finalD: d)
            }
        }
    }
    return nil
}

private func backtrack(_ a: [String], _ b: [String], trace: [[Int]], finalD: Int) -> [DiffLine] {
    var result: [DiffLine] = []
    var x = a.count
    var y = b.count
    for d in stride(from: finalD, through: 0, by: -1) {
        if d == 0 {
            // Step 0 is the leading snake on diagonal 0, where x == y.
            while x > 0 {
                result.append(DiffLine(kind: .unchanged, text: a[x - 1]))
                x -= 1
                y -= 1
            }
            break
        }
        let v = trace[d]
        func at(_ k: Int) -> Int { v[k + d] }
        let k = x - y
        let prevK = (k == -d || (k != d && at(k - 1) < at(k + 1))) ? k + 1 : k - 1
        let prevX = at(prevK)
        let prevY = prevX - prevK
        while x > prevX, y > prevY {
            result.append(DiffLine(kind: .unchanged, text: a[x - 1]))
            x -= 1
            y -= 1
        }
        if x == prevX {
            result.append(DiffLine(kind: .added, text: b[prevY]))
        } else {
            result.append(DiffLine(kind: .removed, text: a[prevX]))
        }
        x = prevX
        y = prevY
    }
    return result.reversed()
}
