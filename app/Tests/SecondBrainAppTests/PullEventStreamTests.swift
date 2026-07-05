import Foundation
import Testing
@testable import SecondBrain

/// The `ai engine pull --json` stream arrives as arbitrary byte chunks, so the
/// line buffer must hold a partial JSONL line across chunk boundaries and yield
/// multiple events from one chunk. This is the load-bearing logic behind the
/// AI Hub download progress bar.
@Test("PullEventStream reassembles a JSONL line split across chunks")
func pullEventStreamSplitLine() {
    let s = PullEventStream()
    // First chunk ends mid-object → no complete line yet.
    #expect(s.ingest(Data(#"{"model":"m","status":"progr"#.utf8)).isEmpty)
    // Second chunk completes the line.
    let ev = s.ingest(Data("ess\",\"done\":5,\"total\":10}\n".utf8))
    #expect(ev.count == 1)
    #expect(ev.first?.status == "progress")
    #expect(ev.first?.done == 5)
    #expect(ev.first?.total == 10)
}

@Test("PullEventStream yields multiple events from one chunk")
func pullEventStreamMultiLine() {
    let s = PullEventStream()
    let two = s.ingest(Data(
        "{\"model\":\"m\",\"status\":\"done\",\"path\":\"/p\"}\n{\"model\":\"m2\",\"status\":\"progress\",\"done\":1,\"total\":2}\n".utf8))
    #expect(two.count == 2)
    #expect(two[0].status == "done")
    #expect(two[0].path == "/p")
    #expect(two[1].model == "m2")
    #expect(two[1].done == 1)
}

@Test("PullEventStream skips malformed lines without dropping valid ones")
func pullEventStreamSkipsGarbage() {
    let s = PullEventStream()
    let evs = s.ingest(Data("not json\n{\"model\":\"m\",\"status\":\"done\"}\n".utf8))
    #expect(evs.count == 1)
    #expect(evs.first?.status == "done")
}
