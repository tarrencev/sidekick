import SwiftUI

/// A small block-level markdown renderer for agent reports: headings, lists,
/// quotes, code fences and paragraphs, with inline markdown (bold, code, links)
/// inside each block. Links open through openURL, so GitHub links reach the app.
struct MarkdownView: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ForEach(Array(Self.blocks(text).enumerated()), id: \.offset) { _, block in
                view(for: block)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .tint(Theme.accent)
    }

    enum Block {
        case heading(Int, String)
        case paragraph(String)
        case bullet(String, indent: Int)
        case numbered(String, String)
        case quote(String)
        case code(String)
        case rule
    }

    @ViewBuilder private func view(for block: Block) -> some View {
        switch block {
        case .heading(let level, let s):
            Text(inline(s))
                .font(Theme.serif(level == 1 ? 26 : level == 2 ? 21 : 17))
                .foregroundStyle(Theme.text)
                .padding(.top, level <= 2 ? 10 : 4)
        case .paragraph(let s):
            Text(inline(s)).font(.system(size: 15)).foregroundStyle(Theme.text.opacity(0.88)).lineSpacing(3)
        case .bullet(let s, let indent):
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text("•").foregroundStyle(Theme.secondary)
                Text(inline(s)).font(.system(size: 15)).foregroundStyle(Theme.text.opacity(0.88)).lineSpacing(3)
            }
            .padding(.leading, CGFloat(indent) * 16)
        case .numbered(let n, let s):
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(n).font(.system(size: 15).monospacedDigit()).foregroundStyle(Theme.secondary)
                Text(inline(s)).font(.system(size: 15)).foregroundStyle(Theme.text.opacity(0.88)).lineSpacing(3)
            }
        case .quote(let s):
            Text(inline(s))
                .font(.system(size: 15))
                .foregroundStyle(Theme.secondary)
                .padding(.leading, 12)
                .overlay(alignment: .leading) { Rectangle().fill(Theme.accent.opacity(0.6)).frame(width: 2) }
        case .code(let s):
            ScrollView(.horizontal, showsIndicators: false) {
                Text(s).font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.text.opacity(0.85))
                    .padding(12)
            }
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        case .rule:
            Hairline()
        }
    }

    private func inline(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(s)
    }

    static func blocks(_ text: String) -> [Block] {
        var out: [Block] = []
        var para: [String] = []
        var code: [String]?
        func flush() {
            // Keep the author's line breaks: replies hold poems, addresses, lists without markers.
            if !para.isEmpty { out.append(.paragraph(para.joined(separator: "\n"))); para = [] }
        }
        for raw in text.components(separatedBy: "\n") {
            if code != nil {
                if raw.trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                    out.append(.code(code!.joined(separator: "\n"))); code = nil
                } else {
                    code!.append(raw)
                }
                continue
            }
            let line = raw.trimmingCharacters(in: .whitespaces)
            let indent = (raw.prefix { $0 == " " }.count) / 2
            if line.hasPrefix("```") { flush(); code = []; continue }
            if line.isEmpty { flush(); continue }
            if line == "---" || line == "***" { flush(); out.append(.rule); continue }
            if let h = line.firstIndex(where: { $0 != "#" }), line.hasPrefix("#"), line[h] == " " {
                flush(); out.append(.heading(line.distance(from: line.startIndex, to: h), String(line[h...]).trimmingCharacters(in: .whitespaces))); continue
            }
            if line.hasPrefix("- ") || line.hasPrefix("* ") {
                flush(); out.append(.bullet(String(line.dropFirst(2)), indent: indent)); continue
            }
            if let dot = line.firstIndex(of: "."), line[..<dot].allSatisfy(\.isNumber), !line[..<dot].isEmpty,
               line[line.index(after: dot)...].hasPrefix(" ") {
                flush(); out.append(.numbered(String(line[...dot]), String(line[line.index(dot, offsetBy: 2)...]))); continue
            }
            if line.hasPrefix(">") {
                flush(); out.append(.quote(String(line.dropFirst()).trimmingCharacters(in: .whitespaces))); continue
            }
            para.append(line)
        }
        if let code { out.append(.code(code.joined(separator: "\n"))) }
        flush()
        return out
    }
}
