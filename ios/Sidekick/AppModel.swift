import Foundation
#if os(iOS)
import UIKit
#else
import AppKit
#endif
import Observation
import UserNotifications

@MainActor
@Observable
final class AppModel {
    static let shared = AppModel()

    static let defaultServer = "https://dl.yattle-moth.ts.net:7443"

    var server: String {
        didSet {
            UserDefaults.standard.set(server, forKey: "server")
            restartLive()
        }
    }
    var projects: [ProjectSummary] = []
    var details: [String: ProjectDetail] = [:]
    var inbox: [InboxItem] = []
    var threads: [String: ThreadDetail] = [:] // "slug/tid"
    var connected = false
    var lastError: String?

    private var liveTask: Task<Void, Never>?
    private var knownPending: Set<String>?
    var isForeground = true

    init() {
        server = UserDefaults.standard.string(forKey: "server") ?? Self.defaultServer
    }

    var api: API? { URL(string: server).map(API.init) }

    var totalPending: Int { inbox.filter(\.item.needsYou).count }

    /// A conversation the user asked to open (from the Inbox or a notification); the
    /// matching page's composer opens it.
    var requestedConversation: String?

    /// The freshest copy of an item, wherever it was last loaded.
    func item(_ id: String) -> Item? {
        if let hit = inbox.first(where: { $0.id == id }) { return hit.item }
        for list in messages.values { if let hit = list.first(where: { $0.id == id }) { return hit } }
        for d in details.values { if let hit = d.items.first(where: { $0.id == id }) { return hit } }
        return nil
    }

    /// "Coordinator", or the title of the thread that pushed the item.
    func source(of item: Item) -> String {
        guard let tid = item.thread else { return "Coordinator" }
        return details[item.project]?.threadsList.first { $0.id == tid }?.title
            ?? inbox.first { $0.id == item.id }?.threadTitle
            ?? tid
    }

    func refreshThread(_ slug: String, _ tid: String) async {
        guard let api else { return }
        do {
            let fresh = try await api.thread(slug, tid)
            if threads["\(slug)/\(tid)"] != fresh { threads["\(slug)/\(tid)"] = fresh }
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// The oldest pending item a thread is waiting on, if any.
    func pendingItem(_ slug: String, thread tid: String) -> Item? {
        details[slug]?.items.filter { $0.isPending && $0.thread == tid }.min { $0.created < $1.created }
    }

    /// The latest known status of a GitHub PR link, from any loaded project or thread.
    func prStatus(_ url: URL) -> PRStatus? {
        let key = url.absoluteString
        for d in details.values { if let s = d.prs?[key] { return s } }
        for t in threads.values { if let s = t.prs?[key] { return s } }
        return nil
    }

    func refreshInbox() async {
        guard let api else { return }
        if let items = try? await api.inbox() {
            if inbox != items { inbox = items }
            updateBadge()
        }
    }

    func prompt(_ slug: String, _ text: String) async throws {
        try await api?.prompt(slug, text: text)
    }

    func refresh() async {
        guard let api else { return }
        do {
            let fresh = try await api.projects()
            if projects != fresh { projects = fresh }
            lastError = nil
        } catch {
            lastError = error.localizedDescription
        }
    }

    func refresh(_ slug: String) async {
        guard let api else { return }
        do {
            let detail = try await api.project(slug)
            if details[slug] != detail { details[slug] = detail }
            notifyNew(in: detail)
        } catch {
            lastError = error.localizedDescription
        }
    }

    func answer(_ item: Item, _ answers: [String: String]) async throws {
        try await api?.answer(item, answers: answers)
        await refreshAfterAction(item.project)
    }

    func review(_ item: Item, _ verdict: API.Verdict, comment: String, attachments: [String] = []) async throws {
        try await api?.review(item, verdict, comment: comment, attachments: attachments)
        await refreshAfterAction(item.project)
    }

    // MARK: Messages

    var messages: [String: [Item]] = [:] // ComposerTarget.key
    /// Composer targets with a reply the user hasn't seen yet.
    var unreadReplies: Set<String> = []
    /// The conversation currently on screen, so its replies aren't counted as unread.
    var openConversation: String?
    /// The last project picked in the composer on pages without a project.
    var lastProject: String?

    func refreshMessages(_ target: ComposerTarget) async {
        guard let api, let fresh = try? await api.messages(target) else { return }
        let before = messages[target.key] ?? []
        if before != fresh { messages[target.key] = fresh }
        // A reply is new if we last saw its message still waiting.
        let waiting = Set(before.filter(\.isPending).map(\.id))
        let newReplies = fresh.filter { $0.state == "replied" && waiting.contains($0.id) }
        // A reply that arrives while its conversation is on screen has been read.
        if openConversation == target.key, isForeground, fresh.contains(where: \.isUnreadReply) {
            markRead(target)
        }
        if !newReplies.isEmpty, openConversation != target.key || !isForeground {
            unreadReplies.insert(target.key)
            if !pushEnabled { for reply in newReplies { notifyReply(reply, target: target) } }
        }
    }

    func send(_ target: ComposerTarget, _ text: String, attachments: [String] = []) async throws {
        guard let api else { return }
        let item = try await api.send(target, text: text, attachments: attachments)
        // The server's change event may already have reloaded the conversation with
        // this message in it; add it only if it isn't there yet.
        if messages[target.key]?.contains(where: { $0.id == item.id }) != true {
            messages[target.key, default: []].append(item)
        }
    }

    func markRead(_ target: ComposerTarget) {
        unreadReplies.remove(target.key)
        // Read on one device, read everywhere: drops the replies from the Inbox.
        let unread = inbox.contains { $0.item.isUnreadReply && ComposerTarget($0.item) == target }
            || (messages[target.key] ?? []).contains(where: \.isUnreadReply)
        guard unread, let api else { return }
        Task {
            try? await api.markSeen(target)
            await refreshInbox()
        }
    }

    private func notifyReply(_ item: Item, target: ComposerTarget) {
        let content = UNMutableNotificationContent()
        content.title = projects.first { $0.slug == target.project }?.name ?? target.project
        content.body = item.reply ?? "Replied"
        content.sound = .default
        content.userInfo = ["project": target.project, "item": item.id, "thread": target.thread ?? "", "kind": "reply"]
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: "reply-" + item.id, content: content, trigger: nil))
    }

    private func refreshAfterAction(_ slug: String) async {
        await refresh(slug)
        await refresh()
        await refreshInbox()
    }

    // MARK: Live updates

    func startLive() {
        guard liveTask == nil else { return }
        liveTask = Task { [weak self] in
            var delay: UInt64 = 1
            while !Task.isCancelled {
                guard let self, let api = self.api else { return }
                await self.refresh()
                await self.refreshInbox()
                for p in self.projects { await self.refresh(p.slug) }
                do {
                    self.connected = true
                    delay = 1
                    // Events come in bursts (a message, its delivery, a status, a PR
                    // lookup): collect them for a moment, then reload once per project.
                    var changed: Set<String> = []
                    var flush: Task<Void, Never>?
                    for try await slug in api.events() {
                        changed.insert(slug)
                        flush?.cancel()
                        flush = Task { @MainActor in
                            try? await Task.sleep(for: .milliseconds(350))
                            guard !Task.isCancelled else { return }
                            let slugs = changed
                            changed = []
                            await self.reload(slugs)
                        }
                    }
                } catch {}
                self.connected = false
                try? await Task.sleep(nanoseconds: delay * 1_000_000_000)
                delay = min(delay * 2, 30)
            }
        }
    }

    /// Reloads what changed: the project list, the inbox, and each changed project's
    /// page, open threads and conversations.
    private func reload(_ slugs: Set<String>) async {
        await refresh()
        await refreshInbox()
        for slug in slugs where !slug.isEmpty {
            if details[slug] != nil { await refresh(slug) }
            for key in threads.keys where key.hasPrefix(slug + "/") {
                let parts = key.split(separator: "/").map(String.init)
                if parts.count == 2 { await refreshThread(parts[0], parts[1]) }
            }
            for key in messages.keys where key == slug || key.hasPrefix(slug + "/") {
                let parts = key.split(separator: "/").map(String.init)
                await refreshMessages(parts.count == 2 ? .thread(parts[0], parts[1]) : .project(parts[0]))
            }
        }
    }

    func stopLive() {
        liveTask?.cancel()
        liveTask = nil
        connected = false
    }

    private func restartLive() {
        stopLive()
        projects = []
        details = [:]
        inbox = []
        knownPending = nil
        startLive()
    }

    // MARK: Notifications

    // MARK: Push

    /// Set once dl confirms it can send native push to this device.
    var pushEnabled = false
    var pushError: String?
    var deepLink: DeepLink?

    /// Whether iOS lets Sidekick show notifications (the user can turn this off in Settings).
    var notificationsAllowed = true

    func requestNotifications() {
        // The device token doesn't depend on permission, so register either way.
        #if os(iOS)
        UIApplication.shared.registerForRemoteNotifications()
        #else
        NSApplication.shared.registerForRemoteNotifications()
        #endif
        Task {
            let granted = (try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge])) ?? false
            notificationsAllowed = granted
        }
    }

    func registerDevice(_ token: String) async {
        guard let api else { return }
        do {
            pushEnabled = try await api.registerDevice(token: token, env: PushEnvironment.current)
            pushError = pushEnabled ? nil : "dl has no APNs key yet"
        } catch {
            pushError = error.localizedDescription
        }
    }

    private func updateBadge() {
        UNUserNotificationCenter.current().setBadgeCount(totalPending)
    }

    /// Posts a local notification for pending items we haven't seen before.
    private func notifyNew(in detail: ProjectDetail) {
        let pending = detail.items.filter(\.isPending)
        let ids = Set(pending.map(\.id))
        defer { knownPending = (knownPending ?? []).union(ids) }
        guard let known = knownPending, !isForeground, !pushEnabled else { return }
        for item in pending where !known.contains(item.id) {
            let content = UNMutableNotificationContent()
            content.title = detail.name
            content.body = item.kind == .question
                ? (item.questions?.first?.question ?? "A question needs you")
                : "Review: \(item.title ?? "artifact")"
            content.sound = .default
            // Same fields as a push, so tapping it opens the item (see DeepLink).
            content.userInfo = ["project": detail.slug, "item": item.id, "thread": item.thread ?? "", "kind": item.kind.rawValue]
            UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: item.id, content: content, trigger: nil))
        }
    }
}
