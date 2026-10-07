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
