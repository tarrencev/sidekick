import SwiftUI

struct SettingsView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var server = ""

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Server", text: $server)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        .keyboardType(.URL)
                        #endif
                } header: {
                    Text("Server")
                } footer: {
                    Text("The sidekick daemon on your tailnet. Tailscale must be connected on this device.")
                }
                Section {
                    LabeledContent("Connection", value: model.connected ? "Live" : "Offline")
                    LabeledContent("Push notifications", value: model.pushEnabled && model.notificationsAllowed ? "On" : "Off")
                    if !model.notificationsAllowed {
                        Text("Notifications are off for Sidekick. Turn them on in iOS Settings → Notifications → Sidekick.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                    if !model.pushEnabled, let why = model.pushError {
                        Text(why).font(.footnote).foregroundStyle(.secondary)
                    }
                    if let error = model.lastError {
                        Text(error).font(.footnote).foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle("Settings")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        let trimmed = server.trimmingCharacters(in: .whitespaces)
                        if trimmed != model.server, URL(string: trimmed) != nil { model.server = trimmed }
                        dismiss()
                    }
                }
            }
            .onAppear { server = model.server }
        }
    }
}
