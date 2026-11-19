import Foundation
import Testing
@testable import SecondBrain

@Suite("LaunchOptions Safety Guardrail Tests")
struct LaunchOptionsSafetyTests {

    @Test("validateTestVaultSafety accepts safe directories in /tmp and /private/tmp")
    func safeDirectoriesAccepted() {
        let tmpDir = "/tmp/sb-test-\(UUID().uuidString)"
        let privTmpDir = "/private/tmp/sb-test-\(UUID().uuidString)"

        let res1 = LaunchOptions.validateTestVaultSafety(tmpDir)
        switch res1 {
        case .safe(let resolved):
            #expect(!resolved.isEmpty)
            #expect(resolved.hasPrefix("/tmp/") || resolved.hasPrefix("/private/tmp/"))
        default:
            Issue.record("Expected .safe for \(tmpDir), got \(res1)")
        }

        let res2 = LaunchOptions.validateTestVaultSafety(privTmpDir)
        switch res2 {
        case .safe(let resolved):
            #expect(!resolved.isEmpty)
            #expect(resolved.hasPrefix("/tmp/") || resolved.hasPrefix("/private/tmp/"))
        default:
            Issue.record("Expected .safe for \(privTmpDir), got \(res2)")
        }
    }

    @Test("validateTestVaultSafety rejects empty path")
    func emptyPathRejected() {
        #expect(LaunchOptions.validateTestVaultSafety("") == .empty)
        #expect(LaunchOptions.validateTestVaultSafety("   ") == .empty)
    }

    @Test("validateTestVaultSafety strictly rejects paths matching /dev/obsidian")
    func personalObsidianVaultRejected() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let badPaths = [
            "\(home)/dev/obsidian",
            "\(home)/dev/obsidian/vault",
            "/dev/obsidian",
            "/tmp/fake/../..\(home)/dev/obsidian"
        ]
        for path in badPaths {
            let result = LaunchOptions.validateTestVaultSafety(path)
            switch result {
            case .matchesProtectedObsidian:
                #expect(true)
            default:
                Issue.record("Expected matchesProtectedObsidian for \(path), got \(result)")
            }
        }
    }

    @Test("validateTestVaultSafety rejects paths outside /tmp or /private/tmp")
    func pathsOutsideTempRejected() {
        let outsidePaths = [
            "/Users/testuser/Documents/vault",
            "/var/folders/xx/yy/T/vault",
            "/Applications/SecondBrain.app"
        ]
        for path in outsidePaths {
            let result = LaunchOptions.validateTestVaultSafety(path)
            switch result {
            case .outsideTempDirectory, .matchesProtectedObsidian:
                #expect(true)
            default:
                Issue.record("Expected outsideTempDirectory or matchesProtectedObsidian for \(path), got \(result)")
            }
        }
    }

    @Test("validateTestVaultSafety dereferences real filesystem symlinks to protected or outside paths")
    func symlinkDereferencingSafety() throws {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let testDir = URL(fileURLWithPath: "/tmp/sb-symlink-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: testDir, withIntermediateDirectories: true)
        defer {
            try? FileManager.default.removeItem(at: testDir)
        }

        // 1. Symlink pointing to ~/dev/obsidian
        let symObsidian = testDir.appendingPathComponent("symlink_obsidian").path
        try FileManager.default.createSymbolicLink(atPath: symObsidian, withDestinationPath: "\(home)/dev/obsidian")
        let resObsidian = LaunchOptions.validateTestVaultSafety(symObsidian)
        switch resObsidian {
        case .matchesProtectedObsidian(let raw, let resolved):
            #expect(raw == symObsidian)
            #expect(resolved.contains("/dev/obsidian"))
        default:
            Issue.record("Expected .matchesProtectedObsidian for symlink to obsidian, got \(resObsidian)")
        }

        // 2. Broken symlink pointing to ~/dev/obsidian/nonexistent
        let brokenObsidian = testDir.appendingPathComponent("broken_obsidian").path
        try FileManager.default.createSymbolicLink(atPath: brokenObsidian, withDestinationPath: "\(home)/dev/obsidian/nonexistent")
        let resBroken = LaunchOptions.validateTestVaultSafety(brokenObsidian)
        switch resBroken {
        case .matchesProtectedObsidian(let raw, _):
            #expect(raw == brokenObsidian)
        default:
            Issue.record("Expected .matchesProtectedObsidian for broken symlink to obsidian, got \(resBroken)")
        }

        // 3. Symlink in /tmp pointing outside /tmp (Desktop)
        let symDesktop = testDir.appendingPathComponent("symlink_desktop").path
        try FileManager.default.createSymbolicLink(atPath: symDesktop, withDestinationPath: "/Users/testuser/Desktop")
        let resDesktop = LaunchOptions.validateTestVaultSafety(symDesktop)
        switch resDesktop {
        case .outsideTempDirectory(let raw, let resolved):
            #expect(raw == symDesktop)
            #expect(resolved == "/Users/testuser/Desktop")
        default:
            Issue.record("Expected .outsideTempDirectory for symlink to Desktop, got \(resDesktop)")
        }

        // 4. Symlink in /tmp pointing to bare /tmp
        let symBareTmp = testDir.appendingPathComponent("symlink_bare_tmp").path
        try FileManager.default.createSymbolicLink(atPath: symBareTmp, withDestinationPath: "/tmp")
        let resBareTmp = LaunchOptions.validateTestVaultSafety(symBareTmp)
        switch resBareTmp {
        case .outsideTempDirectory:
            #expect(true)
        default:
            Issue.record("Expected .outsideTempDirectory for symlink to bare /tmp, got \(resBareTmp)")
        }

        // 5. Symlink in /tmp pointing to safe directory in /tmp
        let safeTarget = testDir.appendingPathComponent("safe_vault").path
        try FileManager.default.createDirectory(atPath: safeTarget, withIntermediateDirectories: true)
        let symSafe = testDir.appendingPathComponent("symlink_safe").path
        try FileManager.default.createSymbolicLink(atPath: symSafe, withDestinationPath: safeTarget)
        let resSafe = LaunchOptions.validateTestVaultSafety(symSafe)
        switch resSafe {
        case .safe(let resolved):
            #expect(resolved.hasPrefix("/tmp/") || resolved.hasPrefix("/private/tmp/"))
        default:
            Issue.record("Expected .safe for symlink to safe vault, got \(resSafe)")
        }
    }

    @Test("validateTestVaultSafety detects multi-hop broken symlinks and circular loops")
    func multiHopAndCircularSymlinksSafety() throws {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let testDir = URL(fileURLWithPath: "/tmp/sb-multihop-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: testDir, withIntermediateDirectories: true)
        defer {
            try? FileManager.default.removeItem(at: testDir)
        }

        // 1. Multi-hop symlink to non-existent obsidian path (chain_a -> chain_b -> ~/dev/obsidian/ghost)
        let chainObsidianB = testDir.appendingPathComponent("chain_obs_b").path
        let chainObsidianA = testDir.appendingPathComponent("chain_obs_a").path
        try FileManager.default.createSymbolicLink(atPath: chainObsidianB, withDestinationPath: "\(home)/dev/obsidian/ghost")
        try FileManager.default.createSymbolicLink(atPath: chainObsidianA, withDestinationPath: chainObsidianB)
        let resObsidianChain = LaunchOptions.validateTestVaultSafety(chainObsidianA)
        switch resObsidianChain {
        case .matchesProtectedObsidian(let raw, _):
            #expect(raw == chainObsidianA)
        default:
            Issue.record("Expected .matchesProtectedObsidian for multi-hop to obsidian ghost, got \(resObsidianChain)")
        }

        // 2. Multi-hop symlink to non-existent outside path (chain_a -> chain_b -> /Users/testuser/Desktop/nonexist)
        let chainOutsideB = testDir.appendingPathComponent("chain_out_b").path
        let chainOutsideA = testDir.appendingPathComponent("chain_out_a").path
        try FileManager.default.createSymbolicLink(atPath: chainOutsideB, withDestinationPath: "/Users/testuser/Desktop/nonexist")
        try FileManager.default.createSymbolicLink(atPath: chainOutsideA, withDestinationPath: chainOutsideB)
        let resOutsideChain = LaunchOptions.validateTestVaultSafety(chainOutsideA)
        switch resOutsideChain {
        case .outsideTempDirectory(let raw, let resolved):
            #expect(raw == chainOutsideA)
            #expect(resolved == "/Users/testuser/Desktop/nonexist")
        default:
            Issue.record("Expected .outsideTempDirectory for multi-hop to outside nonexistent path, got \(resOutsideChain)")
        }

        // 3. Circular self-loop (loop -> loop)
        let selfLoop = testDir.appendingPathComponent("self_loop").path
        try FileManager.default.createSymbolicLink(atPath: selfLoop, withDestinationPath: selfLoop)
        let resSelfLoop = LaunchOptions.validateTestVaultSafety(selfLoop)
        switch resSelfLoop {
        case .outsideTempDirectory(let raw, let resolved):
            #expect(raw == selfLoop)
            #expect(resolved == selfLoop)
        default:
            Issue.record("Expected .outsideTempDirectory for circular self-loop, got \(resSelfLoop)")
        }

        // 4. Circular mutual 2-node loop (pair_a <-> pair_b)
        let pairA = testDir.appendingPathComponent("pair_a").path
        let pairB = testDir.appendingPathComponent("pair_b").path
        try FileManager.default.createSymbolicLink(atPath: pairA, withDestinationPath: pairB)
        try FileManager.default.createSymbolicLink(atPath: pairB, withDestinationPath: pairA)
        let resPairLoop = LaunchOptions.validateTestVaultSafety(pairA)
        switch resPairLoop {
        case .outsideTempDirectory(let raw, _):
            #expect(raw == pairA)
        default:
            Issue.record("Expected .outsideTempDirectory for circular mutual loop, got \(resPairLoop)")
        }
    }
}
