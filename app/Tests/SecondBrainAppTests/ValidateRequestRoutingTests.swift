import Testing
@testable import SecondBrain

// AppState.pendingValidateRequest is a one-shot cross-window request
// (Settings sets it after a key save, the main window's Models tab runs it),
// but SimpleModelsView has TWO hosts observing the flag: the Models tab
// (validateOnly false) and the Testing tab's Validate pane (validateOnly
// true). Exactly one may consume it. These tests pin the routing so a
// mounted Testing pane can never steal the request and then be torn down by
// the navigation to the Models tab, evaporating the run after the flag was
// already cleared.

@Test("Only the Models-tab host consumes a pending validate request")
func modelsTabHostConsumes() {
    #expect(ValidateRequestRouting.consumes(requested: true, validateOnly: false),
            "the Models-tab host is where the post-key-save routing lands; it must run the request")
    #expect(!ValidateRequestRouting.consumes(requested: true, validateOnly: true),
            "the Testing tab's validate-only host must NO-OP, not consume: it can be mounted when the flag is set, and consuming there both races the Models-tab navigation tearing it down and hides the run from the surface the user was sent to")
}

@Test("No host consumes when nothing is requested")
func noRequestNoConsume() {
    #expect(!ValidateRequestRouting.consumes(requested: false, validateOnly: false))
    #expect(!ValidateRequestRouting.consumes(requested: false, validateOnly: true))
}
