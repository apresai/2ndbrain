import Foundation
import Testing
@testable import SecondBrain

// MARK: - ai status model_access decode

@Test("AIStatusInfo decodes the model_access summary")
func aiStatusDecodesModelAccess() throws {
    let json = """
    {
      "provider":"bedrock",
      "embedding_model":"amazon.nova-2-multimodal-embeddings-v1:0",
      "generation_model":"us.anthropic.claude-haiku-4-5-20251001-v1:0",
      "dimensions":1024,
      "embed_available":true,
      "gen_available":true,
      "embedding_count":10,
      "document_count":12,
      "model_access":{
        "verified":7,
        "access_denied":3,
        "other_failures":1,
        "last_verified_at":"2026-07-01T10:00:00Z"
      }
    }
    """
    let s = try JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
    let ma = try #require(s.modelAccess)
    #expect(ma.verified == 7)
    #expect(ma.accessDenied == 3)
    #expect(ma.otherFailures == 1)
    #expect(ma.lastVerifiedAt == "2026-07-01T10:00:00Z")
}

@Test("AIStatusInfo without model_access decodes it as nil (never validated / older CLI)")
func aiStatusModelAccessAbsent() throws {
    let json = """
    {
      "provider":"bedrock",
      "embedding_model":"amazon.nova-2",
      "generation_model":"claude-haiku",
      "dimensions":1024,
      "embed_available":true,
      "gen_available":true,
      "embedding_count":0,
      "document_count":0
    }
    """
    let s = try JSONDecoder().decode(AIStatusInfo.self, from: Data(json.utf8))
    #expect(s.modelAccess == nil)
}

// MARK: - models verify --events NDJSON decode

@Test("VerifyEvent decodes the start header with total and estimate")
func verifyEventDecodesStart() throws {
    let json = #"{"event":"start","total":11,"estimated_usd":0.0012}"#
    let e = try JSONDecoder().decode(VerifyEvent.self, from: Data(json.utf8))
    #expect(e.event == "start")
    #expect(e.total == 11)
    #expect(e.estimatedUsd == 0.0012)
    #expect(e.result == nil)
    #expect(e.summary == nil)
}

@Test("VerifyEvent decodes a per-model result event")
func verifyEventDecodesResult() throws {
    let json = #"""
    {"event":"result","n":3,"total":11,"result":{"model_id":"us.anthropic.claude-haiku-4-5-20251001-v1:0","provider":"bedrock","type":"generation","ok":true,"latency":"320ms"}}
    """#
    let e = try JSONDecoder().decode(VerifyEvent.self, from: Data(json.utf8))
    #expect(e.event == "result")
    #expect(e.n == 3)
    #expect(e.total == 11)
    let r = try #require(e.result)
    #expect(r.modelID == "us.anthropic.claude-haiku-4-5-20251001-v1:0")
    #expect(r.provider == "bedrock")
    #expect(r.modelType == "generation")
    #expect(r.ok == true)
    #expect(r.latency == "320ms")
    #expect(r.code == nil)
}

@Test("VerifyEvent decodes a failing result carrying the classified code")
func verifyEventDecodesFailingResult() throws {
    let json = #"""
    {"event":"result","n":4,"total":11,"result":{"model_id":"us.anthropic.claude-opus-4-8","provider":"bedrock","type":"generation","ok":false,"detail":"403","code":"access_denied","remediation":"request access in the AWS console"}}
    """#
    let e = try JSONDecoder().decode(VerifyEvent.self, from: Data(json.utf8))
    let r = try #require(e.result)
    #expect(r.ok == false)
    #expect(r.code == "access_denied")
    #expect(r.remediation == "request access in the AWS console")
}

@Test("VerifyEvent decodes the done event with a summary and saved scope")
func verifyEventDecodesDone() throws {
    let json = #"{"event":"done","total":11,"summary":{"ok":7,"access_denied":3,"throttled":1},"saved_scope":"vault"}"#
    let e = try JSONDecoder().decode(VerifyEvent.self, from: Data(json.utf8))
    #expect(e.event == "done")
    #expect(e.total == 11)
    #expect(e.savedScope == "vault")
    let summary = try #require(e.summary)
    #expect(summary["ok"] == 7)
    #expect(summary["access_denied"] == 3)
    #expect(summary["throttled"] == 1)
}

@Test("VerifyEvent decodes the zero-candidate done event that omits summary")
func verifyEventDecodesDoneNoSummary() throws {
    // Stream-contract note 2: the zero-candidate done event omits `summary`
    // entirely (Go omitempty on an empty map), so it must decode to nil.
    let json = #"{"event":"done","total":0,"saved_scope":"vault"}"#
    let e = try JSONDecoder().decode(VerifyEvent.self, from: Data(json.utf8))
    #expect(e.event == "done")
    #expect(e.total == 0)
    #expect(e.summary == nil)
}

// MARK: - VerifyFlow.summaryText (verify vocabulary → display, ok→verified)

@Test("summaryText maps ok to verified and keeps the failure vocabulary")
func summaryTextMapsVocabulary() {
    let text = VerifyFlow.summaryText(["ok": 7, "access_denied": 3, "throttled": 1])
    #expect(text == "7 verified, 3 no access, 1 throttled")
}

@Test("summaryText drops zero counts and preserves the canonical order")
func summaryTextDropsZeros() {
    let text = VerifyFlow.summaryText(["ok": 5, "access_denied": 0, "timeout": 2])
    #expect(text == "5 verified, 2 timeout")
}

@Test("summaryText folds an unknown code under its raw slug so nothing is lost")
func summaryTextUnknownCode() {
    let text = VerifyFlow.summaryText(["ok": 1, "some_new_code": 2])
    #expect(text == "1 verified, 2 some_new_code")
}

@Test("summaryText treats an absent summary as an empty result set")
func summaryTextAbsent() {
    #expect(VerifyFlow.summaryText(nil) == "No models validated")
    #expect(VerifyFlow.summaryText([:]) == "No models validated")
}

// MARK: - VerifyFlow.costCap

@Test("costCap doubles the confirmed estimate plus a cent of headroom")
func costCapFromPreview() throws {
    let json = #"{"estimates":[],"total_usd":0.03}"#
    let preview = try JSONDecoder().decode(CostPreviewResponse.self, from: Data(json.utf8))
    let cap = VerifyFlow.costCap(preview: preview)
    #expect(abs(cap - 0.07) < 1e-9)
}

@Test("costCap falls back to the CLI default guard when no estimate is available")
func costCapNilPreview() {
    #expect(VerifyFlow.costCap(preview: nil) == 0.05)
}

// MARK: - VerifyFlow.classify (empty-stream + nonzero exit is a failure)

@Test("classify treats a clean exit as success")
func classifySuccess() {
    #expect(VerifyFlow.classify(exitStatus: 0, stderr: "") == .success)
}

@Test("classify surfaces stderr on a non-zero exit")
func classifyFailureWithStderr() {
    let outcome = VerifyFlow.classify(exitStatus: 1, stderr: "refusing to spend: estimated cost exceeds cap\n")
    #expect(outcome == .failure("refusing to spend: estimated cost exceeds cap"))
}

@Test("classify still fails on a non-zero exit with an empty stream (cost-cap trip)")
func classifyFailureEmptyStream() {
    // Stream-contract note 1: a cost-cap-exceeded --events run exits non-zero
    // with EMPTY stdout and no error event; that must be a failure, not a
    // silent no-op, even when stderr is also empty.
    let outcome = VerifyFlow.classify(exitStatus: 2, stderr: "")
    #expect(outcome == .failure("models verify exited with status 2"))
}
