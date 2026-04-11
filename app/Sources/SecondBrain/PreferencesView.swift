import SwiftUI

struct PreferencesView: View {
    @Environment(AppState.self) var appState

    private let fontFamilies = [
        "System Mono",
        "SF Mono",
        "Menlo",
        "Monaco",
        "Courier New",
        "Andale Mono",
    ]

    var body: some View {
        @Bindable var state = appState

        Form {
            Section("Editor Font") {
                Picker("Font Family", selection: Binding(
                    get: { appState.editorFontFamily },
                    set: { appState.setFontFamily($0) }
                )) {
                    ForEach(fontFamilies, id: \.self) { family in
                        Text(family)
                            .font(.custom(family == "System Mono" ? "Menlo" : family, size: 13))
                            .tag(family)
                    }
                }

                HStack {
                    Text("Font Size")
                    Spacer()
                    Button {
                        appState.decreaseFontSize()
                    } label: {
                        Image(systemName: "minus")
                    }
                    .buttonStyle(.borderless)

                    Text("\(Int(appState.editorFontSize)) pt")
                        .monospacedDigit()
                        .frame(width: 44, alignment: .center)

                    Button {
                        appState.increaseFontSize()
                    } label: {
                        Image(systemName: "plus")
                    }
                    .buttonStyle(.borderless)

                    Button("Reset") {
                        appState.resetFontSize()
                    }
                    .buttonStyle(.borderless)
                    .foregroundStyle(.secondary)
                    .font(.caption)
                }

                // Live preview
                Text("The quick brown fox jumps over the lazy dog.")
                    .font(.custom(
                        appState.editorFontFamily == "System Mono" ? "Menlo" : appState.editorFontFamily,
                        size: appState.editorFontSize
                    ))
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 4)
            }
        }
        .formStyle(.grouped)
        .frame(width: 400, height: 220)
    }
}
