import AppKit
import Carbon.HIToolbox
import Observation
import SwiftUI

/// A key combination such as `"ctrl+shift+space"`, persisted in `UserDefaults`
/// and registered with Carbon's `RegisterEventHotKey`.
struct KeyCombo: Equatable, Sendable {
    static let defaultSpec = "ctrl+shift+space"

    let keyCode: UInt32
    let carbonModifiers: UInt32
    let display: String
    let spec: String
    let caps: [String]

    private static let keys: [String: (code: Int, name: String)] = {
        var keys: [String: (Int, String)] = [
            "space": (kVK_Space, "Space"), "return": (kVK_Return, "↩"), "tab": (kVK_Tab, "⇥"),
            "escape": (kVK_Escape, "⎋"), "esc": (kVK_Escape, "⎋"), "slash": (kVK_ANSI_Slash, "/"),
            "period": (kVK_ANSI_Period, "."), "comma": (kVK_ANSI_Comma, ","), "semicolon": (kVK_ANSI_Semicolon, ";"),
            "quote": (kVK_ANSI_Quote, "'"), "backslash": (kVK_ANSI_Backslash, "\\"), "grave": (kVK_ANSI_Grave, "`"),
            "minus": (kVK_ANSI_Minus, "-"), "equal": (kVK_ANSI_Equal, "="),
        ]
        let letters: [(String, Int)] = [
            ("a", kVK_ANSI_A), ("b", kVK_ANSI_B), ("c", kVK_ANSI_C), ("d", kVK_ANSI_D), ("e", kVK_ANSI_E),
            ("f", kVK_ANSI_F), ("g", kVK_ANSI_G), ("h", kVK_ANSI_H), ("i", kVK_ANSI_I), ("j", kVK_ANSI_J),
            ("k", kVK_ANSI_K), ("l", kVK_ANSI_L), ("m", kVK_ANSI_M), ("n", kVK_ANSI_N), ("o", kVK_ANSI_O),
            ("p", kVK_ANSI_P), ("q", kVK_ANSI_Q), ("r", kVK_ANSI_R), ("s", kVK_ANSI_S), ("t", kVK_ANSI_T),
            ("u", kVK_ANSI_U), ("v", kVK_ANSI_V), ("w", kVK_ANSI_W), ("x", kVK_ANSI_X), ("y", kVK_ANSI_Y),
            ("z", kVK_ANSI_Z), ("0", kVK_ANSI_0), ("1", kVK_ANSI_1), ("2", kVK_ANSI_2), ("3", kVK_ANSI_3),
            ("4", kVK_ANSI_4), ("5", kVK_ANSI_5), ("6", kVK_ANSI_6), ("7", kVK_ANSI_7), ("8", kVK_ANSI_8),
            ("9", kVK_ANSI_9),
        ]
        for (letter, code) in letters { keys[letter] = (code, letter.uppercased()) }
        let functionKeys = [
            kVK_F1, kVK_F2, kVK_F3, kVK_F4, kVK_F5, kVK_F6,
            kVK_F7, kVK_F8, kVK_F9, kVK_F10, kVK_F11, kVK_F12,
        ]
        for (index, code) in functionKeys.enumerated() { keys["f\(index + 1)"] = (code, "F\(index + 1)") }
        return keys
    }()

    private static let names: [Int: (spec: String, display: String)] = {
        var names: [Int: (String, String)] = [:]
        for (spec, key) in keys.sorted(by: { $0.key.count > $1.key.count }) {
            if names[key.code] == nil { names[key.code] = (spec, key.name) }
        }
        return names
    }()

    /// Parses `"ctrl+shift+space"`, `"opt+space"`, `"cmd+shift+k"`, etc.
    init?(_ text: String) {
        var modifiers: UInt32 = 0
        var key: (code: Int, name: String)?
        for part in text.lowercased().split(whereSeparator: { $0 == "+" || $0 == "-" || $0 == " " }) {
            switch part {
            case "cmd", "command", "⌘": modifiers |= UInt32(cmdKey)
            case "shift", "⇧": modifiers |= UInt32(shiftKey)
            case "opt", "option", "alt", "⌥": modifiers |= UInt32(optionKey)
            case "ctrl", "control", "⌃": modifiers |= UInt32(controlKey)
            default:
                guard key == nil, let found = Self.keys[String(part)] else { return nil }
                key = found
            }
        }
        guard let key else { return nil }
        self.init(code: key.code, modifiers: modifiers)
    }

    /// Parses an `NSEvent` key press (used by `ShortcutRecorder`). Requires at
    /// least one of ⌘, ⌥, or ⌃ unless the key is an F-key.
    init?(keyCode: UInt16, flags: NSEvent.ModifierFlags) {
        var modifiers: UInt32 = 0
        if flags.contains(.command) { modifiers |= UInt32(cmdKey) }
        if flags.contains(.shift) { modifiers |= UInt32(shiftKey) }
        if flags.contains(.option) { modifiers |= UInt32(optionKey) }
        if flags.contains(.control) { modifiers |= UInt32(controlKey) }
        self.init(code: Int(keyCode), modifiers: modifiers)
    }

    private init?(code: Int, modifiers: UInt32) {
        guard let name = Self.names[code] else { return nil }
        let isFunctionKey = name.display.count > 1 && name.display.hasPrefix("F")
        guard modifiers & UInt32(cmdKey | optionKey | controlKey) != 0 || isFunctionKey else { return nil }
        keyCode = UInt32(code)
        carbonModifiers = modifiers
        var caps: [String] = []
        var parts: [String] = []
        for (mask, symbol, word) in [
            (controlKey, "⌃", "ctrl"),
            (optionKey, "⌥", "opt"),
            (shiftKey, "⇧", "shift"),
            (cmdKey, "⌘", "cmd"),
        ] where modifiers & UInt32(mask) != 0 {
            caps.append(symbol)
            parts.append(word)
        }
        self.caps = caps + [name.display]
        display = caps.joined() + name.display
        spec = (parts + [name.spec]).joined(separator: "+")
    }

    /// Equivalent `NSMenuItem` key + modifier mask for status/app menu rendering.
    var menuEquivalent: (String, NSEvent.ModifierFlags)? {
        var flags: NSEvent.ModifierFlags = []
        if carbonModifiers & UInt32(cmdKey) != 0 { flags.insert(.command) }
        if carbonModifiers & UInt32(shiftKey) != 0 { flags.insert(.shift) }
        if carbonModifiers & UInt32(optionKey) != 0 { flags.insert(.option) }
        if carbonModifiers & UInt32(controlKey) != 0 { flags.insert(.control) }
        let name = display.trimmingCharacters(in: CharacterSet(charactersIn: "⌃⌥⇧⌘"))
        switch name {
        case "Space": return (" ", flags)
        case _ where name.count == 1: return (name.lowercased(), flags)
        default: return nil
        }
    }

    /// Checks whether macOS already reserves this combo for a system shortcut
    /// (Spotlight, Input Source Switching, Emoji Picker, Screenshots).
    var systemConflict: String? {
        var list: Unmanaged<CFArray>?
        guard CopySymbolicHotKeys(&list) == noErr,
              let entries = list?.takeRetainedValue() as? [[String: Any]] else { return nil }
        let taken = entries.contains { entry in
            (entry[kHISymbolicHotKeyEnabled as String] as? Bool) == true
                && (entry[kHISymbolicHotKeyCode as String] as? Int) == Int(keyCode)
                && (entry[kHISymbolicHotKeyModifiers as String] as? Int).map { UInt32($0) & 0xFF00 } == carbonModifiers
        }
        guard taken else { return nil }
        switch (Int(keyCode), Int(carbonModifiers)) {
        case (kVK_Space, cmdKey): return "Spotlight uses \(display)"
        case (kVK_Space, cmdKey | optionKey): return "Finder's search window uses \(display)"
        case (kVK_Space, controlKey), (kVK_Space, controlKey | optionKey): return "Input source switching uses \(display)"
        case (kVK_Space, controlKey | cmdKey): return "The emoji picker uses \(display)"
        case (kVK_ANSI_3, cmdKey | shiftKey), (kVK_ANSI_4, cmdKey | shiftKey), (kVK_ANSI_5, cmdKey | shiftKey):
            return "Screenshots use \(display)"
        default: return "macOS reserves \(display) for a system shortcut"
        }
    }
}

/// Carbon `RegisterEventHotKey` wrapper requiring no Accessibility permission.
@MainActor
final class GlobalHotkey {
    let combo: KeyCombo
    private let action: () -> Void
    nonisolated(unsafe) private var hotKey: EventHotKeyRef?
    nonisolated(unsafe) private var handler: EventHandlerRef?

    init?(_ combo: KeyCombo, action: @escaping () -> Void) {
        self.combo = combo
        self.action = action
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        let me = Unmanaged.passUnretained(self).toOpaque()
        guard InstallEventHandler(GetApplicationEventTarget(), hotkeyPressed, 1, &spec, me, &handler) == noErr else {
            return nil
        }
        let id = EventHotKeyID(signature: OSType(0x324E_4252), id: 1) // "2NBR"
        let status = RegisterEventHotKey(
            combo.keyCode,
            combo.carbonModifiers,
            id,
            GetApplicationEventTarget(),
            0,
            &hotKey
        )
        guard status == noErr else {
            if let handler { RemoveEventHandler(handler) }
            handler = nil
            return nil
        }
    }

    deinit {
        if let hotKey { UnregisterEventHotKey(hotKey) }
        if let handler { RemoveEventHandler(handler) }
    }

    fileprivate func fire() { action() }
}

private nonisolated func hotkeyPressed(
    _ next: EventHandlerCallRef?,
    _ event: EventRef?,
    _ userData: UnsafeMutableRawPointer?
) -> OSStatus {
    guard let userData else { return OSStatus(eventNotHandledErr) }
    let address = UInt(bitPattern: userData)
    MainActor.assumeIsolated {
        let hotkey = Unmanaged<GlobalHotkey>.fromOpaque(UnsafeRawPointer(bitPattern: address)!).takeUnretainedValue()
        hotkey.fire()
    }
    return noErr
}

/// Manages a single global hotkey registration with suspend/resume while the
/// shortcut recorder is actively listening.
@MainActor
final class HotkeyCenter {
    private var hotkey: GlobalHotkey?
    private(set) var combo: KeyCombo?
    private var isSuspended = false
    var onPress: () -> Void = {}

    @discardableResult
    func register(_ combo: KeyCombo) -> Bool {
        if combo == self.combo, hotkey != nil { return true }
        let previous = hotkey
        hotkey = nil
        guard let new = GlobalHotkey(combo, action: { [weak self] in self?.onPress() }) else {
            hotkey = previous
            return false
        }
        hotkey = new
        self.combo = combo
        isSuspended = false
        return true
    }

    func suspend() {
        guard hotkey != nil else { return }
        isSuspended = true
        hotkey = nil
    }

    func resume() {
        guard isSuspended, let combo else { return }
        isSuspended = false
        hotkey = GlobalHotkey(combo, action: { [weak self] in self?.onPress() })
    }

    func unregister() {
        isSuspended = false
        hotkey = nil
        combo = nil
    }
}

/// Shared coordinator for the Quick Ask & Search global hotkey. Persists the
/// user's chosen combination in `UserDefaults` and stays inert under `2NB_TEST=1`.
@Observable @MainActor
final class HotkeyManager {
    static let shared = HotkeyManager()
    static let defaultsKey = "quickSearchHotkey"

    private(set) var combo: KeyCombo?
    @ObservationIgnored private let center = HotkeyCenter()
    @ObservationIgnored var onTrigger: (() -> Void)?
    /// Set by `OnboardingView` on step 3 so pressing the hotkey flashes
    /// "Hotkey works!" inside the wizard instead of opening the HUD over it.
    @ObservationIgnored var onboardingTestHook: (() -> Void)?

    private var isTestEnvironment: Bool {
        ProcessInfo.processInfo.environment["2NB_TEST"] == "1"
    }

    private init() {
        let saved = UserDefaults.standard.string(forKey: Self.defaultsKey) ?? KeyCombo.defaultSpec
        self.combo = KeyCombo(saved) ?? KeyCombo(KeyCombo.defaultSpec)
        center.onPress = { [weak self] in
            self?.handlePress()
        }
    }

    /// Registers the active/default shortcut with macOS (skipped under `2NB_TEST=1`).
    func registerDefaultIfNeeded() {
        guard !isTestEnvironment else { return }
        let target = combo ?? KeyCombo(KeyCombo.defaultSpec)!
        if !center.register(target), let fallback = KeyCombo(KeyCombo.defaultSpec) {
            if center.register(fallback) {
                combo = fallback
            }
        }
    }

    /// Applies a new shortcut from `ShortcutRecorder`. Returns nil on success,
    /// or a human-readable conflict reason if macOS or another app owns it.
    func apply(_ newCombo: KeyCombo) -> String? {
        if let conflict = newCombo.systemConflict {
            return conflict
        }
        if !isTestEnvironment {
            guard center.register(newCombo) else {
                return "\(newCombo.display) is in use by another application"
            }
        }
        combo = newCombo
        UserDefaults.standard.set(newCombo.spec, forKey: Self.defaultsKey)
        return nil
    }

    func resetToDefault() {
        guard let fallback = KeyCombo(KeyCombo.defaultSpec) else { return }
        _ = apply(fallback)
    }

    func setListening(_ active: Bool) {
        guard !isTestEnvironment else { return }
        if active {
            center.suspend()
        } else {
            center.resume()
        }
    }

    private func handlePress() {
        if let hook = onboardingTestHook {
            hook()
            return
        }
        onTrigger?()
    }
}

/// Click-to-record global shortcut control (`Esc` cancels).
struct ShortcutRecorder: View {
    let combo: KeyCombo?
    let apply: (KeyCombo) -> String?
    let listening: (Bool) -> Void
    var capSize: CGFloat = 14

    @State private var recording = false
    @State private var problem: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Button {
                setRecording(!recording)
            } label: {
                Group {
                    if recording {
                        Text("Press the new shortcut…")
                            .foregroundStyle(.secondary)
                    } else if let combo {
                        Keycaps(caps: combo.caps, size: capSize)
                    } else {
                        Text("Click to set shortcut")
                            .foregroundStyle(.secondary)
                    }
                }
                .font(.system(size: 13))
                .frame(minWidth: 180, minHeight: capSize * 2.2)
                .padding(.horizontal, 12)
                .contentShape(Rectangle())
                .background(
                    RoundedRectangle(cornerRadius: 9, style: .continuous)
                        .fill(Color.primary.opacity(recording ? 0.03 : 0.05))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 9, style: .continuous)
                        .strokeBorder(
                            recording ? Color.accentColor : Color.primary.opacity(0.15),
                            lineWidth: recording ? 2 : 1
                        )
                )
            }
            .buttonStyle(.plain)
            .background(KeyCatcher(active: recording, onKey: handle, onResign: { setRecording(false) }))
            .accessibilityLabel(
                recording
                    ? "Recording a new shortcut"
                    : "Shortcut \(combo?.display ?? "none"), click to change"
            )

            Text(problem ?? (recording ? "Esc cancels · include ⌘, ⌥, or ⌃" : "Click to change"))
                .font(.system(size: 11))
                .foregroundStyle(problem == nil ? AnyShapeStyle(.tertiary) : AnyShapeStyle(Color.red))
        }
        .onDisappear {
            if recording { listening(false) }
        }
    }

    private func setRecording(_ on: Bool) {
        guard on != recording else { return }
        recording = on
        if !on { problem = nil }
        listening(on)
    }

    private func handle(_ event: NSEvent) {
        let flags = event.modifierFlags
            .intersection(.deviceIndependentFlagsMask)
            .subtracting([.capsLock, .function, .numericPad])
        if event.keyCode == 53, flags.isEmpty {
            setRecording(false)
            return
        }
        guard let next = KeyCombo(keyCode: event.keyCode, flags: flags) else {
            problem = "Include ⌘, ⌥, or ⌃ with a key"
            return
        }
        if let err = apply(next) {
            problem = err
        } else {
            setRecording(false)
        }
    }
}

private struct KeyCatcher: NSViewRepresentable {
    let active: Bool
    let onKey: (NSEvent) -> Void
    let onResign: () -> Void

    func makeNSView(context: Context) -> KeyCatcherView { KeyCatcherView() }

    func updateNSView(_ view: KeyCatcherView, context: Context) {
        view.onKey = onKey
        view.onResign = onResign
        guard view.active != active else { return }
        view.active = active
        DispatchQueue.main.async {
            if active {
                view.window?.makeFirstResponder(view)
            } else if view.window?.firstResponder === view {
                view.window?.makeFirstResponder(nil)
            }
        }
    }
}

@MainActor
private final class KeyCatcherView: NSView {
    var active = false
    var onKey: (NSEvent) -> Void = { _ in }
    var onResign: () -> Void = {}
    private var resignObserver: NSObjectProtocol?

    override var acceptsFirstResponder: Bool { active }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if let resignObserver { NotificationCenter.default.removeObserver(resignObserver) }
        resignObserver = nil
        guard let window else { return }
        resignObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.didResignKeyNotification,
            object: window,
            queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self, self.active else { return }
                self.onResign()
            }
        }
    }

    override func resignFirstResponder() -> Bool {
        if active { DispatchQueue.main.async { [weak self] in self?.onResign() } }
        return super.resignFirstResponder()
    }

    override func keyDown(with event: NSEvent) {
        if active { onKey(event) } else { super.keyDown(with: event) }
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard active, window?.firstResponder === self else { return super.performKeyEquivalent(with: event) }
        onKey(event)
        return true
    }
}
