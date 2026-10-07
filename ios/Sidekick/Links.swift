import Foundation
import SwiftUI

/// Turns agent-written text into tappable text: markdown links work, and bare URLs
/// become links too. Links open in the browser (or the GitHub app for github.com).
enum Links {
    private static let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue)

    static func attributed(_ text: String, markdown: Bool = true) -> AttributedString {
        var out = (markdown
            ? try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))
            : nil) ?? AttributedString(text)
        let plain = String(out.characters)
        guard let detector else { return out }
        for match in detector.matches(in: plain, range: NSRange(plain.startIndex..., in: plain)) {
            guard let url = match.url, let r = Range(match.range, in: plain) else { continue }
            let lower = out.characters.index(out.startIndex, offsetBy: plain.distance(from: plain.startIndex, to: r.lowerBound))
            let upper = out.characters.index(lower, offsetBy: plain.distance(from: r.lowerBound, to: r.upperBound))
            if out[lower..<upper].link == nil { out[lower..<upper].link = url }
        }
        return out
    }

    /// The URLs in a piece of text, in order.
    static func urls(in text: String) -> [URL] {
        guard let detector else { return [] }
        return detector.matches(in: text, range: NSRange(text.startIndex..., in: text)).compactMap(\.url)
    }
}

/// Text with working links, selectable so it can be copied.
struct LinkedText: View {
    let text: String

    init(_ text: String) { self.text = text }

    var body: some View {
        Text(Links.attributed(text))
            .tint(Theme.accent)
            .textSelection(.enabled)
    }
}
