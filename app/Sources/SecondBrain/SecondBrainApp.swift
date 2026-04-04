import SwiftUI
import SecondBrainCore

@main
struct SecondBrainApp: App {
    @State private var appState = AppState()
    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(appState)
                .frame(minWidth: 800, minHeight: 500)
                .onAppear {
                    if let lastVault = UserDefaults.standard.string(forKey: "lastVaultPath") {
                        appState.openVault(at: URL(fileURLWithPath: lastVault))
                    }
                }
        }
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Vault...") {
                    newVaultPanel()
                }

                Button("Open Vault...") {
                    openVaultPanel()
                }
                .keyboardShortcut("o", modifiers: [.command, .shift])

                Divider()

                Button("New Document...") {
                    newDocumentPanel()
                }
                .keyboardShortcut("n", modifiers: .command)
                .disabled(appState.vault == nil)

                Divider()

                Button("Import Obsidian Vault...") {
                    importObsidianPanel()
                }

                Button("Export to Obsidian...") {
                    exportObsidianPanel()
                }
                .disabled(appState.vault == nil)
            }

            CommandGroup(replacing: .saveItem) {
                Button("Save") {
                    appState.saveCurrentDocument()
                }
                .keyboardShortcut("s", modifiers: .command)
                .disabled(appState.currentDocument == nil)
            }

            CommandMenu("View") {
                Button("Search Vault") {
                    appState.showSearch.toggle()
                }
                .keyboardShortcut("f", modifiers: [.command, .shift])

                Button("Quick Open") {
                    appState.showQuickOpen.toggle()
                }
                .keyboardShortcut("p", modifiers: .command)

                Button("Command Palette") {
                    appState.showCommandPalette.toggle()
                }
                .keyboardShortcut("p", modifiers: [.command, .shift])

                Divider()

                Button("Focus Mode") {
                    appState.focusModeActive.toggle()
                }
                .keyboardShortcut("e", modifiers: [.command, .shift])

                Button("Toggle Sidebar") {
                    appState.sidebarVisible.toggle()
                }
                .keyboardShortcut("\\", modifiers: .command)
            }
        }
    }

    private func newDocumentPanel() {
        let alert = NSAlert()
        alert.messageText = "New Document"
        alert.informativeText = "Enter a title and select a type."
        alert.addButton(withTitle: "Create")
        alert.addButton(withTitle: "Cancel")

        let container = NSView(frame: NSRect(x: 0, y: 0, width: 300, height: 54))

        let titleField = NSTextField(frame: NSRect(x: 0, y: 30, width: 300, height: 24))
        titleField.placeholderString = "Title"
        titleField.stringValue = ""
        container.addSubview(titleField)

        let typePopup = NSPopUpButton(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        typePopup.addItems(withTitles: ["Note", "Architecture Decision Record", "Runbook", "Postmortem"])
        container.addSubview(typePopup)

        alert.accessoryView = container
        alert.window.initialFirstResponder = titleField

        let response = alert.runModal()
        guard response == .alertFirstButtonReturn else { return }
        let types = ["note", "adr", "runbook", "postmortem"]
        let selectedType = types[typePopup.indexOfSelectedItem]
        let title = titleField.stringValue.isEmpty ? "Untitled" : titleField.stringValue
        appState.createNewDocument(type: selectedType, title: title)
    }

    private func newVaultPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        panel.message = "Select or create a directory for the new vault"
        panel.prompt = "Create Vault"

        if panel.runModal() == .OK, let url = panel.url {
            appState.createVault(at: url)
        }
    }

    private func openVaultPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select a 2ndbrain vault directory"
        panel.prompt = "Open Vault"

        if panel.runModal() == .OK, let url = panel.url {
            appState.openVault(at: url)
            UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
        }
    }

    private func importObsidianPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select an Obsidian vault to import"
        panel.prompt = "Import"

        if panel.runModal() == .OK, let url = panel.url {
            runCLICommand(["import-obsidian", url.path]) { success in
                if success {
                    appState.openVault(at: url)
                    UserDefaults.standard.set(url.path, forKey: "lastVaultPath")
                }
            }
        }
    }

    private func exportObsidianPanel() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select a directory to export the vault as Obsidian format"
        panel.prompt = "Export"

        if panel.runModal() == .OK, let url = panel.url {
            guard let vault = appState.vault else { return }
            runCLICommand(["export-obsidian", url.path], cwd: vault.rootURL) { _ in }
        }
    }

    @MainActor
    private func runCLICommand(_ args: [String], cwd: URL? = nil, completion: @escaping @MainActor (Bool) -> Void) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/local/bin/2nb")
        process.arguments = args
        if let cwd { process.currentDirectoryURL = cwd }

        process.terminationHandler = { proc in
            let success = proc.terminationStatus == 0
            Task { @MainActor in
                completion(success)
            }
        }

        do {
            try process.run()
        } catch {
            print("CLI command failed: \(error)")
            completion(false)
        }
    }
}

