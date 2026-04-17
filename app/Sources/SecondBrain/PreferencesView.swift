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

            Section("Autosave") {
                Toggle("Enable autosave", isOn: Binding(
                    get: { appState.autosaveIntervalSeconds > 0 },
                    set: { isOn in
                        appState.setAutosaveInterval(isOn ? 30 : 0)
                    }
                ))
                if appState.autosaveIntervalSeconds > 0 {
                    Picker("Save every", selection: Binding(
                        get: { appState.autosaveIntervalSeconds },
                        set: { appState.setAutosaveInterval($0) }
                    )) {
                        Text("15 seconds").tag(15)
                        Text("30 seconds").tag(30)
                        Text("60 seconds").tag(60)
                    }
                    .pickerStyle(.menu)
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 400, height: 300)
    }
}
