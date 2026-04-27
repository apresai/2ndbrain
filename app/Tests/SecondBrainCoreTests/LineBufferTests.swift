import Foundation
import SecondBrainCore
import Testing

@Test("LineBuffer preserves UTF-8 split across chunks")
func lineBufferPreservesSplitUTF8() {
    let buffer = LineBuffer()
    let bytes = Array("prefix café\n".utf8)
    let split = bytes.firstIndex(of: 0xC3) ?? 0

    #expect(buffer.append(Data(bytes[..<split])).isEmpty)
    let lines = buffer.append(Data(bytes[split...]))
    #expect(lines == ["prefix café"])
}

@Test("LineBuffer returns complete lines and trailing finish")
func lineBufferCompleteLinesAndFinish() {
    let buffer = LineBuffer()

    #expect(buffer.append(Data("{\"event\":\"one\"}\n{\"event\"".utf8)) == ["{\"event\":\"one\"}"])
    #expect(buffer.append(Data(":\"two\"}\ntrailing".utf8)) == ["{\"event\":\"two\"}"])
    #expect(buffer.finish() == ["trailing"])
}
