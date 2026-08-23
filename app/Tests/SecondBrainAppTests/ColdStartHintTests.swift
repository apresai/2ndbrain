import Testing
import Foundation
@testable import SecondBrain

@Test("ColdStartHint constructs and its constants match the patient-probe design")
@MainActor
func coldStartHintConstants() {
    let hint = ColdStartHint()
    #expect(String(describing: type(of: hint)) == "ColdStartHint")
    // 15s: long enough that a warm probe (a few seconds) never shows it,
    // short enough to land before a user concludes the app hung. The copy
    // must set the expectation in minutes, because the CLI's probe deadlines
    // deliberately allow a cold model minutes of think time.
    #expect(ColdStartHint.delaySeconds == 15)
    #expect(ColdStartHint.message.contains("a few minutes"))
    #expect(ColdStartHint.message.hasPrefix("Still working"))
}
