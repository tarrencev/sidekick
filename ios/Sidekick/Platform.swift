import SwiftUI

/// Small shims so the same views build for iOS and macOS.
extension View {
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

    /// On the Mac, Return sends and Shift-Return starts a new line (as in Messages and
    /// Slack). iOS keeps the keyboard's own Return key and the send button.
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
