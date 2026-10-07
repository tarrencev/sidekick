#if os(macOS)
import SwiftUI

/// The Mac window: Inbox and projects in a sidebar, the selection on the right.
struct MacRootView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: SidebarItem? = .inbox
    @State private var path: [Route] = []
    @State private var nextPath: [Route]?

    enum SidebarItem: Hashable {
        case inbox
        case project(String)
    }

    var body: some View {
        NavigationSplitView {
            List(selection: $selection) {
                Label("Inbox", systemImage: "tray")
                    .badge(model.totalPending)
                    .tag(SidebarItem.inbox)
                Section("Projects") {
                    ForEach(model.projects) { project in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(project.name).font(Theme.serif(15))
                            if let headline = project.status?.headline {
                                Text(headline).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                            }
                        }
                        .padding(.vertical, 3)
                        .tag(SidebarItem.project(project.slug))
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 210, ideal: 250, max: 340)
        } detail: {
            NavigationStack(path: $path) {
                Group {
                    switch selection {
                    case .project(let slug): ProjectView(slug: slug).id(slug)
                    default: InboxView()
                    }
                }
                .routes()
            }
        }
        .frame(minWidth: 820, minHeight: 560)
        .background(Theme.background)
        .onChange(of: selection) { _, _ in
            path = nextPath ?? []
            nextPath = nil
        }
        // A clicked notification or menu bar item.
        .onChange(of: model.deepLink) { _, link in
            guard let link else { return }
            let target: SidebarItem = .inbox
            let routes = link.routes
            if selection == target {
                path = routes
            } else {
                nextPath = routes
                selection = target
            }
            model.deepLink = nil
        }
    }
}

/// The menu bar: what's waiting, one click from the relevant screen.
struct MenuBarContent: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        let pending = model.inbox.filter(\.item.isPending)
        if pending.isEmpty {
            Text("Nothing needs you")
        } else {
            ForEach(pending.prefix(10)) { entry in
                Button("\(entry.projectName): \(entry.item.headline)") {
                    open(DeepLink(item: entry.id, project: entry.item.project, thread: entry.item.thread, kind: entry.item.kind.rawValue))
                }
            }
        }
        Divider()
        Button("Open Sidekick") { open(nil) }.keyboardShortcut("o")
        Button("Quit Sidekick") { NSApplication.shared.terminate(nil) }.keyboardShortcut("q")
    }

    private func open(_ link: DeepLink?) {
        openWindow(id: "main")
        NSApplication.shared.activate()
        if let link { model.deepLink = link }
    }
}

struct MenuBarLabel: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let n = model.totalPending
        Image(systemName: n > 0 ? "tray.full.fill" : "tray")
        if n > 0 { Text("\(n)") }
    }
}
#endif
