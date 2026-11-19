import Foundation
import Testing
@testable import SecondBrain

@Suite("Local Model Plumbing Tests")
struct LocalModelPlumbingTests {

    @Test("PullEventStream reassembles a JSONL line split across chunks")
    func pullEventStreamSplitLine() {
        let s = PullEventStream()
        // First chunk ends mid-object -> no complete line yet.
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

    @Test("LocalGemmaCatalog defaults to EmbeddingGemma 2 Q8_0")
    func localGemmaCatalogDefaults() throws {
        #expect(LocalGemmaCatalog.defaultEmbedModel == "embeddinggemma-2")
        let embed = try #require(LocalGemmaCatalog.models.first { $0.id == LocalGemmaCatalog.defaultEmbedModel })
        #expect(embed.role == "embed")
        #expect(embed.title == "EmbeddingGemma 2")
        #expect(embed.fileName == "embeddinggemma-2-Q8_0.gguf")
        #expect(embed.sizeBytes == 309_855_456)
        #expect(embed.sizeDisplay == "310 MB")
    }

    @Test("LocalModelDownload fraction computes clamped ratio and protects against zero total")
    func localModelDownloadFraction() {
        var dl = LocalModelDownload(models: ["embeddinggemma-2", "gemma4-e2b"])
        #expect(dl.fraction == 0.0)

        dl.total = 1000
        dl.done = 250
        #expect(dl.fraction == 0.25)

        dl.done = 1500
        #expect(dl.fraction == 1.0)

        // Negative done must be clamped to 0.0
        dl.done = -500
        #expect(dl.fraction == 0.0)

        // Negative total must produce 0.0
        dl.total = -100
        dl.done = 50
        #expect(dl.fraction == 0.0)
    }

    @Test("EngineStatus decodes role status and reports running and healthy states")
    func engineStatusDecoding() throws {
        let json = """
        {
            "engine_path": "/opt/homebrew/bin/llama-server",
            "agent_loaded": true,
            "roles": [
                {
                    "role": "gen",
                    "pid": 4210,
                    "port": 18090,
                    "model": "gemma4-e2b",
                    "started_at": "2026-11-15T12:00:00Z",
                    "healthy": true
                },
                {
                    "role": "embed",
                    "pid": 4211,
                    "port": 18091,
                    "model": "embeddinggemma-2",
                    "started_at": "2026-11-15T12:00:01Z",
                    "healthy": true
                }
            ]
        }
        """.data(using: .utf8)!

        let status = try JSONDecoder().decode(EngineStatus.self, from: json)
        #expect(status.enginePath == "/opt/homebrew/bin/llama-server")
        #expect(status.agentLoaded == true)
        #expect(status.isRunning == true)
        #expect(status.isHealthy == true)
        #expect(status.primaryPid == 4210)
        #expect(status.roles?.count == 2)
    }
}
