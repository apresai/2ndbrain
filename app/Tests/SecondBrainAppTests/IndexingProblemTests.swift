import Foundation
import Testing
@testable import SecondBrain

// The watcher's two `index --doc` call sites used to catch a failure and
// log.debug it, so a note whose frontmatter broke on save silently dropped out
// of search until the next full index reported it. These pin the state
// transition, with no window and no CLI: set on failure, cleared only when THAT
// path indexes cleanly.

@MainActor
@Test("a failed index --doc is recorded with its path and the CLI's reason")
func indexingProblemIsRecorded() {
    let state = AppState()
    #expect(state.lastIndexingProblem == nil)

    state.noteIndexingProblem(path: "notes/broken.md", message: "  malformed YAML frontmatter  ")
    #expect(state.lastIndexingProblem?.path == "notes/broken.md")
    // Trimmed: the CLI's stderr arrives with surrounding whitespace.
    #expect(state.lastIndexingProblem?.message == "malformed YAML frontmatter")
}

@MainActor
@Test("the problem clears when the same note indexes cleanly")
func indexingProblemClearsForTheSamePath() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")
    state.clearIndexingProblem(for: "notes/broken.md")
    #expect(state.lastIndexingProblem == nil)
}

@MainActor
@Test("a different note succeeding does not hide an unfixed failure")
func indexingProblemSurvivesAnotherPathSucceeding() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/broken.md", message: "malformed YAML frontmatter")
    state.clearIndexingProblem(for: "notes/other.md")
    #expect(state.lastIndexingProblem?.path == "notes/broken.md")
}

@MainActor
@Test("a later failure replaces the earlier one")
func indexingProblemReplacedByALaterFailure() {
    let state = AppState()
    state.noteIndexingProblem(path: "notes/first.md", message: "first reason")
    state.noteIndexingProblem(path: "notes/second.md", message: "second reason")
    #expect(state.lastIndexingProblem == IndexingProblem(path: "notes/second.md", message: "second reason"))
}
