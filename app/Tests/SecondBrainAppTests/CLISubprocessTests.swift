import Foundation
import Testing
@testable import SecondBrain

// Process plumbing only: these run real child processes (small shell scripts
// standing in for the executable), never a provider, so nothing is mocked.

private func openFileDescriptorCount() -> Int {
    (try? FileManager.default.contentsOfDirectory(atPath: "/dev/fd").count) ?? -1
}

private func makeScript(_ body: String) throws -> URL {
    let dir = URL(fileURLWithPath: NSTemporaryDirectory())
        .appendingPathComponent("SecondBrainCLISubprocessTests-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let script = dir.appendingPathComponent("fake-cli.sh")
    try ("#!/bin/sh\n" + body + "\n").write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    return script
}

@Test("A failed launch releases its pipes: repeated failures do not leak file descriptors")
@MainActor
func failedLaunchDoesNotLeakFileDescriptors() async throws {
    setenv("2NB_TEST", "1", 1)
    let state = AppState()
    state.cliExecutablePathOverride = "/nonexistent/2nb-\(UUID().uuidString)"
    let cwd = URL(fileURLWithPath: NSTemporaryDirectory())

    // Warm up once so one-time allocations are not counted.
    _ = try? await state.runCLI(["mcp", "status", "--json"], cwd: cwd)
    let before = openFileDescriptorCount()
    for _ in 0..<40 {
        _ = try? await state.runCLI(["mcp", "status", "--json"], cwd: cwd)
        _ = try? await state.runCLIGlobalRaw(["doctor", "--versions"])
    }
    let after = openFileDescriptorCount()
    // Before the fix each failure leaked 6 descriptors: 80 failures is 480,
    // past the 256 soft limit. Allow a little slack for unrelated activity.
    #expect(after - before < 12, "leaked \(after - before) file descriptors over 80 failed launches")
}

@Test("A stdin payload larger than the pipe buffer is delivered without blocking")
@MainActor
func largeStdinPayloadIsDelivered() async throws {
    setenv("2NB_TEST", "1", 1)
    let state = AppState()
    state.cliExecutablePathOverride = try makeScript("wc -c").path
    let payload = Data(repeating: UInt8(ascii: "x"), count: 300_000)

    // 300 KB is well past the 64 KB pipe buffer. Written before launch on the
    // main actor, this call never returned.
    let out = try await state.runCLI(["ask"], cwd: URL(fileURLWithPath: NSTemporaryDirectory()), stdin: payload)
    let count = String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    #expect(count == "300000")
}

@Test("Cancelling a cancellable CLI call terminates the child and throws CancellationError")
@MainActor
func cancellableCallTerminatesChild() async throws {
    setenv("2NB_TEST", "1", 1)
    let state = AppState()
    state.cliExecutablePathOverride = try makeScript("exec sleep 30").path
    let cwd = URL(fileURLWithPath: NSTemporaryDirectory())

    let started = ContinuousClock.now
    let task = Task { @MainActor in
        try await state.runCLI(["search", "q"], cwd: cwd, cancellable: true)
    }
    try await Task.sleep(nanoseconds: 300_000_000)
    task.cancel()
    do {
        _ = try await task.value
        Issue.record("expected CancellationError")
    } catch is CancellationError {
        // expected
    } catch {
        Issue.record("expected CancellationError, got \(error)")
    }
    #expect(ContinuousClock.now - started < .seconds(10), "the child was not terminated on cancel")
}

@Test("ProcessCancelBox terminates a process that is attached after the cancel arrived")
func cancelBoxHandlesCancelBeforeAttach() throws {
    let box = ProcessCancelBox()
    box.cancel()
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/bin/sleep")
    process.arguments = ["30"]
    try process.run()
    box.attach(process)
    process.waitUntilExit()
    #expect(box.isCancelled)
    #expect(process.terminationReason == .uncaughtSignal)
}
