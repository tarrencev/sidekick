import SwiftUI

extension View {
    /// A toolbar button that clears a pending ask from the inbox, for when it no
    /// longer needs the user. The agent is told, so it doesn't wait on it.
    func dismissable(_ item: Item) -> some View { modifier(DismissButton(item: item)) }
}

private struct DismissButton: ViewModifier {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var close
    let item: Item

    @State private var asking = false
    @State private var error: String?

    func body(content: Content) -> some View {
        content
            .toolbar {
                if item.isPending {
                    ToolbarItem(placement: .primaryAction) {
                        Button { asking = true } label: {
                            Label("Dismiss", systemImage: "tray.and.arrow.down")
                        }
                        .accessibilityIdentifier("item.dismiss")
                    }
                }
            }
            .confirmationDialog("Clear this from your inbox?", isPresented: $asking, titleVisibility: .visible) {
                Button("Already handled") { send("Already handled") }
                Button("No longer needed") { send("No longer needed") }
            } message: {
                Text(error ?? "The agent is told, so it won't wait on it or guess. If it still matters, it asks again.")
            }
    }

    private func send(_ reason: String) {
        Task {
            do {
                try await model.dismiss(item, reason: reason)
                close()
            } catch {
                self.error = error.localizedDescription
                asking = true
            }
        }
    }
}
