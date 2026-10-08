import SwiftUI
#if os(iOS)
import UIKit
#endif

/// "Reading room": near-black paper, warm off-white type, serif headings, one coral accent.
enum Theme {
    static let background = Color(red: 0.067, green: 0.067, blue: 0.067)
    static let surface = Color(red: 0.118, green: 0.114, blue: 0.110)
    static let line = Color.white.opacity(0.09)
    static let text = Color(red: 0.95, green: 0.93, blue: 0.90)
    static let secondary = Color(red: 0.60, green: 0.58, blue: 0.55)
    static let tertiary = Color(red: 0.42, green: 0.41, blue: 0.39)
    static let accent = Color(red: 1.0, green: 0.478, blue: 0.392)

    static func serif(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .ui(size: size, weight: weight, design: .serif)
    }
}

extension View {
    /// The standard screen chrome: dark paper behind everything.
    func readingRoom() -> some View {
        self
            .scrollContentBackground(.hidden)
            .background(Theme.background.ignoresSafeArea())
            #if os(iOS)
            .toolbarBackground(Theme.background, for: .navigationBar)
            #else
            .toolbarBackground(Theme.background, for: .windowToolbar)
            #endif
    }
}

struct Hairline: View {
    var body: some View {
        Rectangle().fill(Theme.line).frame(height: 1)
    }
}

struct SectionTitle: View {
    let text: String
    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text)
            .font(.ui(size: 13))
            .foregroundStyle(Theme.secondary)
    }
}

/// Full-width coral call to action.
struct PrimaryButton: View {
    let title: String
    var busy = false
    var enabled = true
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(busy ? "Sending…" : title)
                .font(.ui(size: 15, weight: .semibold))
                .foregroundStyle(enabled ? Theme.background : Theme.tertiary)
                .frame(maxWidth: .infinity, minHeight: 48)
                .background(enabled ? Theme.accent : Theme.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(.plain)
        .disabled(!enabled || busy)
    }
}

/// Bordered text input on the surface color.
struct InputBox: View {
    let placeholder: String
    @Binding var text: String
    var minLines = 3
    /// What Return does; nil keeps Return as a newline.
    var onSubmit: (() -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top, spacing: 6) {
                TextField("", text: $text, prompt: Text(placeholder).foregroundStyle(Theme.tertiary), axis: .vertical)
                    .lineLimit(minLines...10)
                    .font(.ui(size: 15))
                    .foregroundStyle(Theme.text)
                    .tint(Theme.accent)
                    .padding(.vertical, 14)
                    .padding(.leading, 14)
                    .modifier(SubmitOnReturn(text: $text, onSubmit: onSubmit))
                MicButton(text: $text)
                    .padding(.top, 8)
                    .padding(.trailing, 8)
            }
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Theme.line))
            DictationNote()
        }
    }
}

/// A document row, used for artifacts.
struct ArtifactRow: View {
    let title: String
    let subtitle: String

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "doc.text")
                .font(.ui(size: 20, weight: .light))
                .foregroundStyle(Theme.text)
                .frame(width: 36, height: 40)
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Theme.line))
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.ui(size: 15)).foregroundStyle(Theme.text)
                Text(subtitle).font(.ui(size: 12)).foregroundStyle(Theme.secondary)
            }
            Spacer()
            Image(systemName: "chevron.right")
                .font(.ui(size: 13, weight: .medium))
                .foregroundStyle(Theme.tertiary)
        }
        .contentShape(Rectangle())
    }
}

extension Date {
    var ago: String {
        let s = Int(-timeIntervalSinceNow)
        switch s {
        case ..<60: return "just now"
        case ..<3600: return "\(s / 60)m ago"
        case ..<86400: return "\(s / 3600)h ago"
        default: return "\(s / 86400)d ago"
        }
    }
}

/// An outlined link. GitHub PR links are tinted by the PR's state and CI, and
/// open in the GitHub app when it's installed.
struct LinkPill: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openURL) private var openURL
    let url: URL

    var body: some View {
        let look = PRLook(model.prStatus(url), isPR: linkLabel(url).hasPrefix("PR"))
        Button { openURL(url) } label: {
            HStack(spacing: 6) {
                Image(systemName: look.icon)
                Text(linkLabel(url)).lineLimit(1)
                if let detail = look.detail {
                    Text(detail).foregroundStyle(look.color.opacity(0.75))
                }
            }
            .font(.ui(size: 13, weight: .medium))
            .foregroundStyle(look.color)
            .padding(.horizontal, 11)
            .padding(.vertical, 6)
            .background(look.color.opacity(0.08), in: Capsule())
            .overlay(Capsule().strokeBorder(look.color.opacity(0.35)))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel([linkLabel(url), look.detail].compactMap { $0 }.joined(separator: ", "))
    }
}

/// How a PR link looks for its state: merged, closed, draft, or open with CI
/// passing, running or failing.
struct PRLook {
    let color: Color
    let icon: String
    let detail: String?

    static let merged = Color(red: 0.70, green: 0.56, blue: 0.98)
    static let passing = Color(red: 0.42, green: 0.80, blue: 0.55)
    static let running = Color(red: 0.93, green: 0.74, blue: 0.36)
    static let failing = Color(red: 1.0, green: 0.42, blue: 0.40)

    init(_ status: PRStatus?, isPR: Bool) {
        guard isPR else { (color, icon, detail) = (Theme.accent, "link", nil); return }
        guard let status else { (color, icon, detail) = (Theme.accent, "arrow.triangle.pull", nil); return }
        switch (status.state, status.checks) {
        case ("merged", _): (color, icon, detail) = (Self.merged, "arrow.triangle.merge", "merged")
        case ("closed", _): (color, icon, detail) = (Theme.tertiary, "xmark", "closed")
        case ("draft", _): (color, icon, detail) = (Theme.secondary, "pencil", "draft")
        case (_, "failing"): (color, icon, detail) = (Self.failing, "xmark.circle", "CI failing")
        case (_, "pending"): (color, icon, detail) = (Self.running, "clock", "CI running")
        case (_, "passing"): (color, icon, detail) = (Self.passing, "checkmark.circle", "CI green")
        default: (color, icon, detail) = (Theme.accent, "arrow.triangle.pull", "open")
        }
    }
}

private struct SubmitOnReturn: ViewModifier {
    @Binding var text: String
    let onSubmit: (() -> Void)?

    func body(content: Content) -> some View {
        if let onSubmit {
            content.sendsOnReturn($text, send: onSubmit)
        } else {
            content
        }
    }
}

extension Font {
    /// The app's fonts are sized for the default text size and scale with the user's
    /// Dynamic Type setting (iPhone), like the system text styles do.
    static func ui(size: CGFloat, weight: Font.Weight = .regular, design: Font.Design = .default) -> Font {
        .system(size: scaled(size), weight: weight, design: design)
    }

    static func scaled(_ size: CGFloat) -> CGFloat {
        #if os(iOS)
        UIFontMetrics.default.scaledValue(for: size)
        #else
        size
        #endif
    }
}
