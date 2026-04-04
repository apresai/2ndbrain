import SwiftUI
import SecondBrainCore

struct MentionListView: View {
    @Bindable var state: MentionAutocompleteState

    var body: some View {
        VStack(spacing: 0) {
            if state.results.isEmpty {
                Text("No matching documents")
                    .foregroundStyle(.secondary)
                    .font(.subheadline)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    List(Array(state.results.enumerated()), id: \.element.id) { index, doc in
                        row(for: doc, at: index)
                            .id(index)
                    }
                    .listStyle(.plain)
                    .onChange(of: state.selectedIndex) { _, newIndex in
                        proxy.scrollTo(newIndex, anchor: .center)
                    }
                }
            }
        }
        .frame(width: 300, height: 250)
    }

    private func row(for doc: DocumentRecord, at index: Int) -> some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(doc.title.isEmpty ? doc.path : doc.title)
                    .fontWeight(index == state.selectedIndex ? .semibold : .regular)
                    .lineLimit(1)

                Text(doc.path)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
            }

            Spacer()

            if !doc.docType.isEmpty {
                Text(doc.docType)
                    .font(.caption2)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.accentColor.opacity(0.15))
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            }
        }
        .padding(.vertical, 2)
        .listRowBackground(index == state.selectedIndex ? Color.accentColor.opacity(0.1) : Color.clear)
    }
}
