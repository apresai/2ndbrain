import Foundation

public final class FSEventsWatcher: @unchecked Sendable {
    private var stream: FSEventStreamRef?
    private let path: String
    private let filter: @Sendable (String) -> Bool
    private let callback: @Sendable ([String]) -> Void
    private let queue = DispatchQueue(label: "dev.apresai.2ndbrain.fswatcher")

    /// Default filter keeps the legacy "markdown-only" behavior so existing
    /// callers don't change. Pass a custom `filter` to observe additional
    /// files (e.g. .yaml catalogs).
    public init(
        path: String,
        filter: @escaping @Sendable (String) -> Bool = { $0.hasSuffix(".md") },
        callback: @escaping @Sendable ([String]) -> Void
    ) {
        self.path = path
        self.filter = filter
        self.callback = callback
    }

    public func start() {
        let pathsToWatch = [path] as CFArray

        var context = FSEventStreamContext()
        context.info = Unmanaged.passUnretained(self).toOpaque()

        let flags = UInt32(
            kFSEventStreamCreateFlagUseCFTypes |
            kFSEventStreamCreateFlagFileEvents |
            kFSEventStreamCreateFlagNoDefer
        )

        guard let stream = FSEventStreamCreate(
            nil,
            { _, info, numEvents, eventPaths, _, _ in
                guard let info else { return }
                let watcher = Unmanaged<FSEventsWatcher>.fromOpaque(info).takeUnretainedValue()
                guard let paths = unsafeBitCast(eventPaths, to: NSArray.self) as? [String] else { return }
                let matched = paths.filter { watcher.filter($0) }
                if !matched.isEmpty {
                    watcher.callback(matched)
                }
            },
            &context,
            pathsToWatch,
            FSEventStreamEventId(kFSEventStreamEventIdSinceNow),
            1.0,
            FSEventStreamCreateFlags(flags)
        ) else { return }

        self.stream = stream
        FSEventStreamSetDispatchQueue(stream, queue)
        FSEventStreamStart(stream)
    }

    public func stop() {
        guard let stream else { return }
        FSEventStreamStop(stream)
        FSEventStreamInvalidate(stream)
        FSEventStreamRelease(stream)
        self.stream = nil
    }

    deinit {
        stop()
    }
}
