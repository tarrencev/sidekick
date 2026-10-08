import SwiftUI

/// Small shims so the same views build for iOS and macOS.
extension View {
    /// Keeps reading content to a comfortable width, centered, on iPad and the Mac.
    func readableWidth(_ width: CGFloat = 760) -> some View {
        frame(maxWidth: width).frame(maxWidth: .infinity)
    }

    /// A compact navigation title (iOS); the Mac window toolbar is already compact.
    @ViewBuilder func inlineTitle() -> some View {
        #if os(iOS)
        navigationBarTitleDisplayMode(.inline)
        #else
        self
        #endif
    }

    /// Detail screens hide the tab bar on iPhone; the Mac has a sidebar instead.
    @ViewBuilder func hidesTabBar() -> some View {
        #if os(iOS)
        toolbar(.hidden, for: .tabBar)
        #else
        self
        #endif
    }

    /// Return submits: it sends a message, a review's feedback or an answer. On the Mac,
    /// Shift-Return (or Option-Return) starts a new line, as in Messages and Slack. On
    /// iPhone the keyboard's Return key reads "send" and sends.
    @ViewBuilder func sendsOnReturn(_ text: Binding<String>, send: @escaping () -> Void) -> some View {
        #if os(macOS)
        onKeyPress(keys: [.return], phases: .down) { press in
            if press.modifiers.contains(.shift) || press.modifiers.contains(.option) {
                text.wrappedValue += "\n"
                return .handled
            }
            send()
            return .handled
        }
        #else
        self
            .submitLabel(.send)
            // A multi-line field inserts a newline instead of submitting: treat a typed
            // Return (one new trailing newline) as send.
            .onChange(of: text.wrappedValue) { old, new in
                // Fast typing or a suggestion can add the Return together with other
                // characters, so look for any growth that ends in a new newline.
                guard new.count > old.count, new.hasSuffix("\n"), !old.hasSuffix("\n") else { return }
                text.wrappedValue = String(new.dropLast())
                send()
            }
        #endif
    }

    /// Root screens draw their own large serif title on iPhone. On the Mac the window
    /// toolbar stays, for the sidebar toggle.
    @ViewBuilder func hidesNavigationBar() -> some View {
        #if os(iOS)
        toolbar(.hidden, for: .navigationBar)
        #else
        self
        #endif
    }
}
