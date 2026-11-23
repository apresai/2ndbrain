import Foundation

public final class FSEventsWatcher: @unchecked Sendable {
    private var stream: FSEventStreamRef?
    private let path: String
    private let queue = DispatchQueue(label: "dev.apresai.2ndbrain.fswatcher")
    private let sink: Sink

    /// What the stream's C callback reaches through its context pointer. It
    /// is retained BY THE STREAM (the context's retain/release callbacks), so
    /// it lives exactly as long as the stream does. The pointer used to be
    /// the watcher itself, passed unretained: replacing a watcher on a vault
    /// switch freed it while a callback could still be running on its queue.
    /// `stopped` drops late events, which belong to the vault being closed.
    private final class Sink: @unchecked Sendable {
        let filter: @Sendable (String) -> Bool
        let callback: @Sendable ([String]) -> Void
        private let lock = NSLock()
        private var _stopped = false

        init(filter: @escaping @Sendable (String) -> Bool, callback: @escaping @Sendable ([String]) -> Void) {
            self.filter = filter
            self.callback = callback
        }

        var stopped: Bool {
            get { lock.lock(); defer { lock.unlock() }; return _stopped }
            set { lock.lock(); _stopped = newValue; lock.unlock() }
        }

        func deliver(_ paths: [String]) {
            guard !stopped else { return }
            let matched = paths.filter(filter)
            if !matched.isEmpty { callback(matched) }
        }
    }

    /// Default filter keeps the legacy "markdown-only" behavior so existing
    /// callers don't change. Pass a custom `filter` to observe additional
    /// files (e.g. .yaml catalogs).
    public init(
        path: String,
        filter: @escaping @Sendable (String) -> Bool = { $0.hasSuffix(".md") },
        callback: @escaping @Sendable ([String]) -> Void
    ) {
        self.path = path
        self.sink = Sink(filter: filter, callback: callback)
    }

    public func start() {
        guard stream == nil else { return }
        sink.stopped = false
        let pathsToWatch = [path] as CFArray

        var context = FSEventStreamContext(
            version: 0,
            info: Unmanaged.passUnretained(sink).toOpaque(),
            retain: { info in
                guard let info else { return nil }
                _ = Unmanaged<Sink>.fromOpaque(info).retain()
                return info
            },
            release: { info in
                guard let info else { return }
                Unmanaged<Sink>.fromOpaque(info).release()
            },
            copyDescription: nil
        )

        let flags = UInt32(
            kFSEventStreamCreateFlagUseCFTypes |
            kFSEventStreamCreateFlagFileEvents |
            kFSEventStreamCreateFlagNoDefer
        )

        guard let stream = FSEventStreamCreate(
            nil,
            { _, info, _, eventPaths, _, _ in
                guard let info else { return }
                let sink = Unmanaged<Sink>.fromOpaque(info).takeUnretainedValue()
                guard let paths = unsafeBitCast(eventPaths, to: NSArray.self) as? [String] else { return }
                sink.deliver(paths)
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
        sink.stopped = true
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
