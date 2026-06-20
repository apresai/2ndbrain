import Foundation
import Testing
@testable import SecondBrain

@Test("StartupSync.shouldSync runs only on a count delta when not already indexing")
func startupSyncShouldSync() {
    // Counts differ (notes added or removed while closed) and idle → sync.
    #expect(StartupSync.shouldSync(onDiskCount: 12, indexedCount: 10, isIndexing: false))
    #expect(StartupSync.shouldSync(onDiskCount: 8, indexedCount: 10, isIndexing: false))

    // Counts match → nothing to catch up, skip (a stable vault pays nothing).
    #expect(!StartupSync.shouldSync(onDiskCount: 10, indexedCount: 10, isIndexing: false))
    #expect(!StartupSync.shouldSync(onDiskCount: 0, indexedCount: 0, isIndexing: false))

    // Already indexing → never start a second, racing index, even on a delta.
    #expect(!StartupSync.shouldSync(onDiskCount: 12, indexedCount: 10, isIndexing: true))
}
