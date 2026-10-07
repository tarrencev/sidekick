import SwiftUI

@main
struct SidekickApp: App {
    #if os(iOS)
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    #else
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    #endif
    @State private var model = AppModel.shared
    @State private var dictation = Dictation()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        mainWindow
        #if os(macOS)
        Settings {
            SettingsView()
                .environment(model)
                .environment(dictation)
                .preferredColorScheme(.dark)
                .frame(width: 460, height: 320)
        }

        MenuBarExtra {
            MenuBarContent().environment(model)
        } label: {
            MenuBarLabel().environment(model)
        }
        #endif
    }

    private var mainWindow: some Scene {
        let window = WindowGroup(id: "main") {
            Group {
                #if os(iOS)
                RootView()
                #else
                MacRootView()
                #endif
            }
            .environment(model)
            .environment(dictation)
            .preferredColorScheme(.dark)
            .tint(Theme.accent)
            .task {
                model.requestNotifications()
                dictation.warmUpIfCached()
                #if os(macOS)
                model.startLive() // the Mac app stays live, including from the menu bar
                #endif
                #if DEBUG
                // `-debugTranscribe <wav path>` checks the on-device engine end to end.
                if let path = UserDefaults.standard.string(forKey: "debugTranscribe") {
                    await ParakeetEngine.debugTranscribe(path)
                }
                #endif
            }
        }
        .onChange(of: scenePhase) { _, phase in
            model.isForeground = phase == .active
            #if os(iOS)
            switch phase {
            case .active: model.startLive()
            case .background: model.stopLive()
            default: break
            }
            #endif
        }
        #if os(macOS)
        return window
            .defaultSize(width: 1120, height: 780)
            .commands {
                CommandGroup(after: .toolbar) {
                    Button("Refresh") {
                        Task {
                            await model.refresh()
                            await model.refreshInbox()
                            for p in model.projects { await model.refresh(p.slug) }
                        }
                    }
                    .keyboardShortcut("r")
                }
            }
        #else
        return window
        #endif
    }
}

/// Where a navigation push can go.
enum Route: Hashable {
    case project(String)
    case item(String)
    case thread(project: String, id: String)
    case file(title: String, url: URL)
}

#if os(iOS)
/// The iPhone layout: Projects and Inbox tabs.
struct RootView: View {
    @Environment(AppModel.self) private var model
    @State private var tab = 0
    @State private var projectsPath: [Route] = []
    @State private var inboxPath: [Route] = []

    var body: some View {
        TabView(selection: $tab) {
            Tab("Projects", systemImage: "folder", value: 0) {
                NavigationStack(path: $projectsPath) { ProjectsView().routes() }
            }
            Tab("Inbox", systemImage: "tray", value: 1) {
                NavigationStack(path: $inboxPath) { InboxView().routes() }
            }
            .badge(model.totalPending)
        }
        // A tapped notification: inbox items open in the Inbox, replies on their page.
        .onChange(of: model.deepLink) { _, link in
            guard let link else { return }
            if link.isReply {
                tab = 0
                projectsPath = link.routes
            } else {
                tab = 1
                inboxPath = link.routes
            }
            model.deepLink = nil
        }
        #if DEBUG
        // e.g. `-debugRoute project:demo,item:<id>` opens straight to a screen.
        .onAppear {
            let spec = UserDefaults.standard.string(forKey: "debugRoute") ?? ""
            projectsPath = spec.split(separator: ",").compactMap { part in
                let kv = part.split(separator: ":", maxSplits: 1).map(String.init)
                guard kv.count == 2 else { return nil }
                switch kv[0] {
                case "project": return .project(kv[1])
                case "thread":
                    let parts = kv[1].split(separator: "/").map(String.init)
                    return parts.count == 2 ? .thread(project: parts[0], id: parts[1]) : nil
                default: return .item(kv[1])
                }
            }
        }
        #endif
    }
}

#endif

extension View {
    func routes() -> some View {
        navigationDestination(for: Route.self) { route in
            switch route {
            case .project(let slug): ProjectView(slug: slug)
            case .item(let id): ItemView(id: id)
            case .thread(let slug, let id): ThreadView(slug: slug, tid: id)
            case .file(let title, let url): FileView(title: title, url: url)
            }
        }
    }
}

/// Opens the right screen for an item, using its freshest copy.
struct ItemView: View {
    @Environment(AppModel.self) private var model
    let id: String

    var body: some View {
        if let item = model.item(id) {
            switch item.kind {
            case .question: QuestionView(item: item)
            case .review, .proposal: ApprovalView(item: item)
            case .message: ThreadOrProject(item: item)
            }
        } else {
            ContentUnavailableView("Gone", systemImage: "tray", description: Text("This item no longer exists."))
                .readingRoom()
        }
    }
}

/// Messages live in the composer; opening one shows where it was sent.
private struct ThreadOrProject: View {
    let item: Item

    var body: some View {
        if let tid = item.thread {
            ThreadView(slug: item.project, tid: tid)
        } else {
            ProjectView(slug: item.project)
        }
    }
}
