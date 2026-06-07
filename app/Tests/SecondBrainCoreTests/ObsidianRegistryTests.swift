import Testing
import Foundation
import SecondBrainCore

@Suite("ObsidianRegistry")
struct ObsidianRegistryTests {
    // The exact shape Obsidian writes to ~/Library/Application Support/obsidian/obsidian.json.
    @Test("decodes the open vault from a real obsidian.json shape")
    func decodesOpenVault() throws {
        let json = """
        {
          "vaults": {
            "0b772aad89d5cf96": { "path": "/Users/chad/dev/obsidian", "ts": 1780434058390, "open": true },
            "aaaa1111bbbb2222": { "path": "/Users/chad/notes-archive", "ts": 1700000000000, "open": false }
          },
          "cli": true
        }
        """
        let reg = try ObsidianRegistry(data: Data(json.utf8))
        #expect(reg.vaults.count == 2)
        #expect(reg.openVault?.path == "/Users/chad/dev/obsidian")
        #expect(reg.openVault?.name == "obsidian")
        #expect(reg.openVault?.open == true)
    }

    @Test("falls back to the most recently opened vault when none is flagged open")
    func fallsBackToMostRecent() throws {
        let json = """
        { "vaults": {
            "a": { "path": "/v/old", "ts": 100, "open": false },
            "b": { "path": "/v/new", "ts": 200, "open": false }
        } }
        """
        let reg = try ObsidianRegistry(data: Data(json.utf8))
        #expect(reg.openVault?.path == "/v/new")
    }

    @Test("tolerates entries missing ts/open and an empty registry")
    func tolerantDecoding() throws {
        let reg = try ObsidianRegistry(data: Data(#"{ "vaults": { "a": { "path": "/v/only" } } }"#.utf8))
        #expect(reg.vaults.count == 1)
        #expect(reg.openVault?.path == "/v/only")
        #expect(reg.openVault?.ts == 0)
        #expect(reg.openVault?.open == false)

        let empty = try ObsidianRegistry(data: Data("{}".utf8))
        #expect(empty.vaults.isEmpty)
        #expect(empty.openVault == nil)
    }

    @Test("matches a vault by path, ignoring a trailing slash")
    func matchesByPath() throws {
        let json = #"{ "vaults": { "a": { "path": "/Users/chad/dev/obsidian", "ts": 1, "open": true } } }"#
        let reg = try ObsidianRegistry(data: Data(json.utf8))
        #expect(reg.vault(at: URL(fileURLWithPath: "/Users/chad/dev/obsidian/"))?.name == "obsidian")
        #expect(reg.vault(at: URL(fileURLWithPath: "/Users/chad/other")) == nil)
    }

    @Test("init throws on malformed JSON")
    func malformedJsonThrows() {
        #expect(throws: (any Error).self) {
            _ = try ObsidianRegistry(data: Data("{ not json".utf8))
        }
    }

    // load() swallows both an absent file and an unparseable one to nil — the
    // documented fallback contract callers depend on (manual "Open Vault").
    @Test("load returns nil for a missing registry file")
    func loadMissingFileIsNil() {
        let missing = FileManager.default.temporaryDirectory
            .appendingPathComponent("no-such-obsidian-registry-\(UUID().uuidString).json")
        #expect(ObsidianRegistry.load(at: missing) == nil)
    }
}
