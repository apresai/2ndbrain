import AppKit
import Foundation
import Observation

struct GUITextCommand: Equatable, Sendable {
    let id: UUID
    let targetIdentifier: String
    let text: String
    let submit: Bool
}

struct GUIButtonCommand: Equatable, Sendable {
    let id: UUID
    let targetIdentifier: String
}

enum GUIButtonAck: Equatable, Sendable {
    case clicked(UUID, String)
    case disabled(UUID, String)
}

@Observable @MainActor
final class GUIInputBridge {
    var pendingTextCommand: GUITextCommand?
    var acknowledgedCommandID: UUID?
    var pendingButtonCommand: GUIButtonCommand?
    var acknowledgedButton: GUIButtonAck?
    var requestedPolishNotePath: String?

    @discardableResult
    func submitTextCommand(target: String, text: String, submit: Bool) -> UUID {
        let cmd = GUITextCommand(
            id: UUID(),
            targetIdentifier: target,
            text: text,
            submit: submit
        )
        acknowledgedCommandID = nil
        pendingTextCommand = cmd
        return cmd.id
    }

    func acknowledge(_ id: UUID) {
        acknowledgedCommandID = id
    }

    @discardableResult
    func submitButtonCommand(target: String) -> UUID {
        let cmd = GUIButtonCommand(id: UUID(), targetIdentifier: target)
        acknowledgedButton = nil
        pendingButtonCommand = cmd
        return cmd.id
    }

    func acknowledgeButtonClicked(_ id: UUID, label: String) {
        acknowledgedButton = .clicked(id, label)
        if pendingButtonCommand?.id == id {
            pendingButtonCommand = nil
        }
    }

    func acknowledgeButtonDisabled(_ id: UUID, label: String) {
        acknowledgedButton = .disabled(id, label)
        if pendingButtonCommand?.id == id {
            pendingButtonCommand = nil
        }
    }
}

struct GUIAutomationRequest: Codable, Sendable {
    let op: String
    let menu: String?
    let item: String?
    let target: String?
    let text: String?
    let submit: Bool?
    let key: String?
    let modifiers: [String]?
    let path: String?
}

struct GUIAutomationResponse: Codable, Sendable {
    let ok: Bool
    let value: String?
    let error: String?

    static func success(_ value: String? = nil) -> GUIAutomationResponse {
        GUIAutomationResponse(ok: true, value: value, error: nil)
    }

    static func failure(_ error: String) -> GUIAutomationResponse {
        GUIAutomationResponse(ok: false, value: nil, error: error)
    }
}

private struct GUIReadyMarker: Codable, Sendable {
    let pid: Int32
    let ready: Bool
}

private struct GUIMenuSnapshot: Codable, Sendable {
    let top: [String]
    let menus: [String: [String]]
}

/// First-party opt-in file-IPC controller active only when `--gui-control-dir`
/// (or `2NB_GUI_CONTROL_DIR`) is passed. Drives `SecondBrain`'s own AppKit
/// menus, windows, sheets, floating HUD, and mounted SwiftUI views on `@MainActor`.
@MainActor
final class GUIAutomationController {
    private let controlDir: URL
    private let appState: AppState
    private weak var appDelegate: AppDelegate?
    private var pollTask: Task<Void, Never>?

    init(controlDir: URL, appState: AppState, appDelegate: AppDelegate) {
        self.controlDir = controlDir.standardizedFileURL
        self.appState = appState
        self.appDelegate = appDelegate
    }

    func start() {
        guard pollTask == nil else { return }
        try? FileManager.default.createDirectory(at: controlDir, withIntermediateDirectories: true)
        writeReadyMarker()

        pollTask = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                await self?.processPendingRequests()
                try? await Task.sleep(for: .milliseconds(50))
            }
        }
    }

    func stop() {
        pollTask?.cancel()
        pollTask = nil
    }

    private func writeReadyMarker() {
        let marker = GUIReadyMarker(
            pid: ProcessInfo.processInfo.processIdentifier,
            ready: true
        )
        let readyURL = controlDir.appendingPathComponent("ready.json")
        writeJSONAtomically(marker, to: readyURL)
    }

    private func processPendingRequests() async {
        guard let urls = try? FileManager.default.contentsOfDirectory(
            at: controlDir,
            includingPropertiesForKeys: nil
        ) else {
            return
        }

        let requests = urls
            .filter { $0.lastPathComponent.hasPrefix("req-") && $0.pathExtension == "json" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }

        for reqURL in requests {
            let filename = reqURL.deletingPathExtension().lastPathComponent
            let reqID = String(filename.dropFirst("req-".count))
            let respURL = controlDir.appendingPathComponent("resp-\(reqID).json")

            let rawData: Data
            do {
                rawData = try Data(contentsOf: reqURL)
                try? FileManager.default.removeItem(at: reqURL)
            } catch {
                try? FileManager.default.removeItem(at: reqURL)
                writeJSONAtomically(
                    GUIAutomationResponse.failure("Failed to read request: \(error.localizedDescription)"),
                    to: respURL
                )
                continue
            }

            let response: GUIAutomationResponse
            do {
                let req = try JSONDecoder().decode(GUIAutomationRequest.self, from: rawData)
                response = await handle(req)
            } catch {
                response = .failure("Invalid automation request JSON: \(error.localizedDescription)")
            }
            writeJSONAtomically(response, to: respURL)
        }
    }

    private func handle(_ req: GUIAutomationRequest) async -> GUIAutomationResponse {
        switch req.op {
        case "inspect_menus":
            return inspectMenus()
        case "click_menu":
            return clickMenu(menu: req.menu, item: req.item)
        case "shortcut":
            return triggerShortcut(key: req.key, modifiers: req.modifiers ?? [])
        case "click_button":
            return await clickButton(target: req.target)
        case "set_text":
            return await setText(target: req.target, text: req.text, submit: req.submit ?? false)
        case "select_polish_note":
            return await selectPolishNote(path: req.path)
        case "escape":
            return await handleEscape()
        case "return_key":
            return await handleReturnKey()
        case "screenshot":
            return captureScreenshot(to: req.path)
        default:
            return .failure("Unknown automation op: \(req.op)")
        }
    }

    // MARK: - Menus & Shortcuts

    private func inspectMenus() -> GUIAutomationResponse {
        appDelegate?.prepareMenusForInteraction()
        guard let mainMenu = NSApp.mainMenu else {
            return .failure("Main menu unavailable")
        }

        var top: [String] = []
        var menus: [String: [String]] = [:]
        for item in mainMenu.items {
            top.append(item.title)
            if let sub = item.submenu {
                sub.update()
                menus[item.title] = sub.items
                    .filter { !$0.isSeparatorItem }
                    .map(\.title)
            }
        }

        let snapshot = GUIMenuSnapshot(top: top, menus: menus)
        guard let data = try? JSONEncoder().encode(snapshot),
              let json = String(data: data, encoding: .utf8) else {
            return .failure("Failed to encode menu snapshot")
        }
        return .success(json)
    }

    private func clickMenu(menu menuName: String?, item itemName: String?) -> GUIAutomationResponse {
        guard let menuName, let itemName else {
            return .failure("click_menu requires menu and item")
        }
        appDelegate?.prepareMenusForInteraction()
        guard let mainMenu = NSApp.mainMenu else {
            return .failure("Main menu unavailable")
        }

        guard let topItem = mainMenu.items.first(where: { $0.title == menuName }),
              let submenu = topItem.submenu else {
            return .failure("Top-level menu not found: \(menuName)")
        }
        submenu.update()

        guard let matchIndex = findMenuItemIndex(named: itemName, in: submenu) else {
            let available = submenu.items.filter { !$0.isSeparatorItem }.map(\.title)
            return .failure("Menu item '\(itemName)' not found in '\(menuName)' (available: \(available))")
        }

        let targetItem = submenu.items[matchIndex]
        guard targetItem.isEnabled else {
            return .failure("Menu item disabled: \(menuName) -> \(targetItem.title)")
        }

        submenu.performActionForItem(at: matchIndex)
        return .success(targetItem.title)
    }

    private func findMenuItemIndex(named target: String, in submenu: NSMenu) -> Int? {
        if let exact = submenu.items.firstIndex(where: { $0.title == target }) {
            return exact
        }
        var stripped = target
        if stripped.hasSuffix("…") {
            stripped = String(stripped.dropLast())
        } else if stripped.hasSuffix("...") {
            stripped = String(stripped.dropLast(3))
        }
        return submenu.items.firstIndex(where: { !$0.isSeparatorItem && $0.title.hasPrefix(stripped) })
    }

    private func triggerShortcut(key: String?, modifiers: [String]) -> GUIAutomationResponse {
        guard let key, !key.isEmpty else {
            return .failure("shortcut requires key")
        }
        let expectedMask = parseModifiers(modifiers)
        appDelegate?.prepareMenusForInteraction()

        if key.lowercased() == "w" && expectedMask == [.command] {
            if let secondary = findSecondaryClosableWindow() {
                secondary.close()
                return .success("closed_secondary_window")
            }
            return .failure("No secondary closable window found for Cmd+W")
        }

        if let mainMenu = NSApp.mainMenu,
           let (submenu, index, item) = findMenuItemByShortcut(key: key, modifiers: expectedMask, in: mainMenu) {
            guard item.isEnabled else {
                return .failure("Shortcut menu item disabled: \(item.title)")
            }
            submenu.performActionForItem(at: index)
            return .success(item.title)
        }

        let keyCode = virtualKeyCode(for: key.lowercased())
        if let event = NSEvent.keyEvent(
            with: .keyDown,
            location: .zero,
            modifierFlags: expectedMask,
            timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: NSApp.keyWindow?.windowNumber ?? NSApp.mainWindow?.windowNumber ?? 0,
            context: nil,
            characters: key.lowercased(),
            charactersIgnoringModifiers: key.lowercased(),
            isARepeat: false,
            keyCode: keyCode
        ),
           NSApp.mainMenu?.performKeyEquivalent(with: event) == true {
            return .success("performKeyEquivalent:\(key)")
        }

        return .failure("No enabled menu item handles shortcut \(modifiers.joined(separator: "+"))+\(key)")
    }

    private func virtualKeyCode(for key: String) -> UInt16 {
        let map: [String: UInt16] = [
            "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
            "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
            "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "=": 24, "9": 25, "7": 26,
            "-": 27, "8": 28, "0": 29, "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35,
            "l": 37, "j": 38, "'": 39, "k": 40, ";": 41, "\\": 42, ",": 43, "/": 44, "n": 45,
            "m": 46, ".": 47,
        ]
        return map[key] ?? 0
    }

    private func parseModifiers(_ modifiers: [String]) -> NSEvent.ModifierFlags {
        var mask: NSEvent.ModifierFlags = []
        for raw in modifiers {
            switch raw.lowercased() {
            case "command", "cmd": mask.insert(.command)
            case "shift": mask.insert(.shift)
            case "option", "alt": mask.insert(.option)
            case "control", "ctrl": mask.insert(.control)
            default: break
            }
        }
        return mask
    }

    private func findMenuItemByShortcut(
        key: String,
        modifiers: NSEvent.ModifierFlags,
        in menu: NSMenu
    ) -> (NSMenu, Int, NSMenuItem)? {
        menu.update()
        let relevantMask: NSEvent.ModifierFlags = [.command, .shift, .option, .control]
        for (idx, item) in menu.items.enumerated() {
            if item.keyEquivalent.lowercased() == key.lowercased(),
               item.keyEquivalentModifierMask.intersection(relevantMask) == modifiers {
                return (menu, idx, item)
            }
            if let sub = item.submenu,
               let match = findMenuItemByShortcut(key: key, modifiers: modifiers, in: sub) {
                return match
            }
        }
        return nil
    }

    private func findSecondaryClosableWindow() -> NSWindow? {
        let nonPanelClosables = NSApp.orderedWindows.filter { window in
            window.isVisible &&
            window.styleMask.contains(.closable) &&
            !(window is NSPanel)
        }
        if let settingsWindow = nonPanelClosables.first(where: { win in
            let ident = win.identifier?.rawValue ?? ""
            if ident.localizedCaseInsensitiveContains("settings") {
                return true
            }
            return ["AI & Models", "Gemini", "Integrations", "General", "Settings"].contains(win.title)
        }) {
            return settingsWindow
        }
        if nonPanelClosables.count > 1 {
            return nonPanelClosables.first
        }
        return nil
    }

    // MARK: - Buttons & Accessibility Hierarchy

    private func orderedCandidateWindows() -> [NSWindow] {
        var candidates: [NSWindow] = []
        for window in NSApp.orderedWindows where window.isVisible {
            for sheet in window.sheets.reversed() where sheet.isVisible {
                candidates.append(sheet)
            }
        }
        if let hud = appDelegate?.quickSearchPanelController?.panelWindow, hud.isVisible {
            candidates.append(hud)
        }
        for window in NSApp.orderedWindows where window.isVisible && !candidates.contains(window) {
            candidates.append(window)
        }
        for window in NSApp.windows where window.isVisible && !candidates.contains(window) {
            candidates.append(window)
        }
        return candidates
    }

    private enum ButtonSearchResult {
        case clicked(String)
        case disabled(String)
        case notFound
    }

    private func clickButton(target: String?) async -> GUIAutomationResponse {
        guard let target, !target.isEmpty else {
            return .failure("click_button requires target")
        }

        let windows = orderedCandidateWindows()
        guard !windows.isEmpty else {
            return .failure("No visible window for click_button(\(target))")
        }

        var debugButtons: [String] = []
        for window in windows {
            window.layoutIfNeeded()
            window.contentView?.layoutSubtreeIfNeeded()

            if let rootView = window.contentView {
                switch clickMatchingNSButton(named: target, in: rootView) {
                case .clicked(let label):
                    return .success(label)
                case .disabled(let label):
                    return .failure("Button disabled: \(label)")
                case .notFound:
                    break
                }
            }

            var visited = Set<ObjectIdentifier>()
            switch clickMatchingAXButton(named: target, in: window, visited: &visited, debugList: &debugButtons) {
            case .clicked(let label):
                return .success(label)
            case .disabled(let label):
                return .failure("Button disabled: \(label)")
            case .notFound:
                break
            }
        }

        let cmdID = appState.guiInputBridge.submitButtonCommand(target: target)
        for _ in 0..<40 {
            if let ack = appState.guiInputBridge.acknowledgedButton {
                switch ack {
                case .clicked(let ackID, let label) where ackID == cmdID:
                    try? await Task.sleep(for: .milliseconds(30))
                    return .success(label)
                case .disabled(let ackID, let label) where ackID == cmdID:
                    return .failure("Button disabled: \(label)")
                default:
                    break
                }
            }
            try? await Task.sleep(for: .milliseconds(25))
        }
        if appState.guiInputBridge.pendingButtonCommand?.id == cmdID {
            appState.guiInputBridge.pendingButtonCommand = nil
        }

        return .failure("Button not found: \(target)")
    }

    private func clickMatchingNSButton(named target: String, in view: NSView) -> ButtonSearchResult {
        if let button = view as? NSButton {
            let ident = button.accessibilityIdentifier()
            let title = button.title
            let axTitle = button.accessibilityTitle() ?? ""
            let axLabel = button.accessibilityLabel() ?? ""
            if ident == target || title == target || axTitle == target || axLabel == target {
                guard button.isEnabled else {
                    return .disabled(ident.isEmpty ? title : ident)
                }
                button.performClick(nil)
                return .clicked(ident.isEmpty ? title : ident)
            }
        }
        for subview in view.subviews {
            let res = clickMatchingNSButton(named: target, in: subview)
            switch res {
            case .clicked, .disabled:
                return res
            case .notFound:
                continue
            }
        }
        return .notFound
    }

    private func clickMatchingAXButton(
        named target: String,
        in node: AnyObject,
        visited: inout Set<ObjectIdentifier>,
        debugList: inout [String]
    ) -> ButtonSearchResult {
        let objID = ObjectIdentifier(node)
        guard !visited.contains(objID) else { return .notFound }
        visited.insert(objID)

        if let ax = node as? NSAccessibilityProtocol {
            let role = ax.accessibilityRole()
            let ident = ax.accessibilityIdentifier() ?? ""
            let title = ax.accessibilityTitle() ?? ""
            let label = ax.accessibilityLabel() ?? ""
            if role == .button || role == .radioButton || role == .checkBox || !ident.isEmpty {
                debugList.append("\(role?.rawValue ?? "nil")(\(ident)|\(title)|\(label))")
            }
            if role == .button || role == .radioButton || role == .checkBox {
                if ident == target || title == target || label == target {
                    guard ax.isAccessibilityEnabled() else {
                        return .disabled(ident.isEmpty ? title : ident)
                    }
                    if ax.accessibilityPerformPress() {
                        return .clicked(ident.isEmpty ? title : ident)
                    }
                }
            }

            if let children = ax.accessibilityChildren() {
                for child in children {
                    let childObj = child as AnyObject
                    let res = clickMatchingAXButton(named: target, in: childObj, visited: &visited, debugList: &debugList)
                    switch res {
                    case .clicked, .disabled:
                        return res
                    case .notFound:
                        continue
                    }
                }
            }
        }

        if let view = node as? NSView {
            for subview in view.subviews {
                let res = clickMatchingAXButton(named: target, in: subview, visited: &visited, debugList: &debugList)
                switch res {
                case .clicked, .disabled:
                    return res
                case .notFound:
                    continue
                }
            }
        } else if let win = node as? NSWindow {
            if let children = win.accessibilityChildren() {
                for child in children {
                    let res = clickMatchingAXButton(named: target, in: child as AnyObject, visited: &visited, debugList: &debugList)
                    switch res {
                    case .clicked, .disabled:
                        return res
                    case .notFound:
                        continue
                    }
                }
            }
            if let contentView = win.contentView {
                let res = clickMatchingAXButton(named: target, in: contentView, visited: &visited, debugList: &debugList)
                switch res {
                case .clicked, .disabled:
                    return res
                case .notFound:
                    break
                }
            }
        }

        return .notFound
    }

    // MARK: - Text Input, Polish Picker, Sheet/HUD Keys

    private func setText(target: String?, text: String?, submit: Bool) async -> GUIAutomationResponse {
        guard let target, let text else {
            return .failure("set_text requires target and text")
        }
        guard NSApp.windows.contains(where: \.isVisible) else {
            return .failure("No visible window for set_text(\(target))")
        }

        if target == "hud.inputField" {
            guard let controller = appDelegate?.quickSearchPanelController else {
                return .failure("QuickSearchPanelController unavailable")
            }
            let ok = await controller.setQueryFromAutomation(text, submit: submit)
            try? await Task.sleep(for: .milliseconds(50))
            return ok ? .success(text) : .failure("Quick Search HUD is not visible or no vault is bound")
        }

        guard target == "askSearch.searchField"
            || target == "askSearch.askField"
            || target == "polish.searchField"
            || target == "polish.goalField" else {
            return .failure("Unsupported text target: \(target)")
        }

        let cmdID = appState.guiInputBridge.submitTextCommand(target: target, text: text, submit: submit)
        for _ in 0..<60 {
            if appState.guiInputBridge.acknowledgedCommandID == cmdID {
                try? await Task.sleep(for: .milliseconds(50))
                return .success(text)
            }
            try? await Task.sleep(for: .milliseconds(25))
        }
        return .failure("Timed out waiting for mounted view '\(target)' to acknowledge text command")
    }

    private func selectPolishNote(path: String?) async -> GUIAutomationResponse {
        guard let path, !path.isEmpty else {
            return .failure("select_polish_note requires path")
        }
        appState.guiInputBridge.requestedPolishNotePath = path
        try? await Task.sleep(for: .milliseconds(100))
        return .success(path)
    }

    private func handleEscape() async -> GUIAutomationResponse {
        if appDelegate?.quickSearchPanelController?.isPanelVisible == true {
            appDelegate?.quickSearchPanelController?.hide()
            return .success("hud_hidden")
        }
        if let host = NSApp.orderedWindows.first(where: { $0.isVisible && !$0.sheets.isEmpty }),
           let sheet = host.sheets.first {
            let cancelResp = await clickButton(target: "indexSheet.cancelButton")
            if cancelResp.ok {
                return .success("sheet_cancelled")
            }
            host.endSheet(sheet)
            return .success("sheet_ended")
        }
        return .success("noop")
    }

    private func handleReturnKey() async -> GUIAutomationResponse {
        if NSApp.orderedWindows.contains(where: { $0.isVisible && !$0.sheets.isEmpty }) {
            for target in ["indexSheet.confirmButton", "indexSheet.doneButton"] {
                let resp = await clickButton(target: target)
                if resp.ok {
                    return .success("clicked:\(target)")
                }
            }
        }
        return .success("noop")
    }

    // MARK: - Window Screenshots

    private func captureScreenshot(to path: String?) -> GUIAutomationResponse {
        guard let path, !path.isEmpty else {
            return .failure("screenshot requires path")
        }

        let targetWindow = orderedCandidateWindows().first
        let destURL = URL(fileURLWithPath: path)
        try? FileManager.default.createDirectory(at: destURL.deletingLastPathComponent(), withIntermediateDirectories: true)

        if let window = targetWindow,
           let contentView = window.contentView {
            window.layoutIfNeeded()
            contentView.layoutSubtreeIfNeeded()
            let bounds = contentView.bounds
            if !bounds.isEmpty,
               let rep = contentView.bitmapImageRepForCachingDisplay(in: bounds) {
                contentView.cacheDisplay(in: bounds, to: rep)
                if let png = rep.representation(using: .png, properties: [:]) {
                    do {
                        try png.write(to: destURL, options: .atomic)
                        return .success(path)
                    } catch {
                        return .failure("Failed to write screenshot PNG: \(error.localizedDescription)")
                    }
                }
            }
        }

        // Fallback if window has empty contentView bounds.
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
        proc.arguments = ["-x", path]
        do {
            try proc.run()
            proc.waitUntilExit()
            return proc.terminationStatus == 0
                ? .success(path)
                : .failure("screencapture exited with \(proc.terminationStatus)")
        } catch {
            return .failure("screencapture fallback failed: \(error.localizedDescription)")
        }
    }

    private func writeJSONAtomically<T: Encodable>(_ value: T, to url: URL) {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        guard let data = try? encoder.encode(value) else { return }
        let tmpURL = url.deletingLastPathComponent().appendingPathComponent(".\(url.lastPathComponent).tmp-\(UUID().uuidString)")
        do {
            try data.write(to: tmpURL, options: .atomic)
            _ = try? FileManager.default.removeItem(at: url)
            try FileManager.default.moveItem(at: tmpURL, to: url)
        } catch {
            try? FileManager.default.removeItem(at: tmpURL)
        }
    }
}
