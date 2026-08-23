import Foundation
import Testing
@testable import SecondBrain

// Pins the shared formatter behavior extracted from MetricsView, so the
// Testing tab's benchmark feed and the performance observatory can never
// drift apart on how a latency, size, or timestamp renders.

@Test("duration renders ms, seconds, and minutes with the historical shapes")
func durationShapes() {
    #expect(Formatters.duration(0) == "0ms")
    #expect(Formatters.duration(412) == "412ms")
    #expect(Formatters.duration(999) == "999ms")
    #expect(Formatters.duration(1000) == "1.0s")
    #expect(Formatters.duration(1234) == "1.2s")
    #expect(Formatters.duration(59_949) == "59.9s")
    #expect(Formatters.duration(60_000) == "1m00s")
    #expect(Formatters.duration(125_000) == "2m05s")
}

@Test("durationD rounds sub-second values and matches the Int variant above 1s")
func durationDoubleVariant() {
    #expect(Formatters.durationD(411.6) == "412ms")
    #expect(Formatters.durationD(1500.0) == "1.5s")
    #expect(Formatters.durationD(60_000.0) == Formatters.duration(60_000))
}

@Test("bytes keeps sub-KB exact and scales KB/MB/GB with one decimal")
func byteShapes() {
    #expect(Formatters.bytes(0) == "0 B")
    #expect(Formatters.bytes(512) == "512 B")
    #expect(Formatters.bytes(1536) == "1.5 KB")
    #expect(Formatters.bytes(3 * 1024 * 1024) == "3.0 MB")
    #expect(Formatters.bytes(2 * 1024 * 1024 * 1024) == "2.0 GB")
}

@Test("relativeDateIfParseable accepts RFC3339 with and without fractional seconds")
func relativeDateParses() {
    #expect(Formatters.relativeDateIfParseable("2026-01-01T10:00:00Z") != nil)
    #expect(Formatters.relativeDateIfParseable("2026-01-01T10:00:00.123Z") != nil)
    #expect(Formatters.relativeDateIfParseable("not a date") == nil)
    #expect(Formatters.relativeDateIfParseable("") == nil)
}

@Test("relativeDate falls back to the raw string so a cell never renders empty")
func relativeDateFallback() {
    #expect(Formatters.relativeDate("garbage") == "garbage")
    #expect(Formatters.relativeDate("2026-01-01T10:00:00Z") != "2026-01-01T10:00:00Z")
}
