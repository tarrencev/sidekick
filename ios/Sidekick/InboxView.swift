import SwiftUI

struct InboxView: View {
    @Environment(AppModel.self) private var model
    @State private var showResolved = false

    private var pending: [InboxItem] { model.inbox.filter(\.item.needsYou) }
    private var resolved: [InboxItem] {
        model.inbox.filter { !$0.item.needsYou }
            .sorted { ($0.item.closed ?? $0.item.created) > ($1.item.closed ?? $1.item.created) }
    }

    @State private var confirmReject: Item?
    @State private var actionError: String?

    var body: some View {
        // A List (not a ScrollView) so rows can be swiped: right to approve, left to reject.
        List {
            VStack(alignment: .leading, spacing: 20) {
                Text("Inbox")
                    .font(Theme.serif(34))
                    .foregroundStyle(Theme.text)
                segmented
                if let actionError {
                    Text(actionError).font(.system(size: 12)).foregroundStyle(Theme.accent)
                }
            }
            .padding(.top, 8)
            .padding(.bottom, 8)
            .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
            .listRowSeparator(.hidden)
            .listRowBackground(Theme.background)

            let rows = showResolved ? Array(resolved.prefix(50)) : pending
            if rows.isEmpty {
                Text(showResolved ? "Nothing resolved yet." : "Nothing needs you.")
                    .font(Theme.serif(18))
                    .foregroundStyle(Theme.secondary)
                    .padding(.top, 16)
                    .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.background)
            }
            ForEach(rows) { entry in
                InboxRow(entry: entry)
                    .background(NavigationLink(value: Route.item(entry.id)) { EmptyView() }.opacity(0))
                    .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
                    .listRowBackground(Theme.background)
                    .listRowSeparatorTint(Theme.line)
                    .swipeActions(edge: .leading, allowsFullSwipe: true) { approveAction(entry.item) }
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) { rejectAction(entry.item) }
            }
        }
        .listStyle(.plain)
        .environment(\.defaultMinListRowHeight, 0)
        .readingRoom()
        .hidesNavigationBar()
        .refreshable { await model.refreshInbox() }
        .composer(nil)
        .confirmationDialog(
            confirmReject?.kind == .proposal ? "Decline this thread?" : "Reject this?",
            isPresented: Binding(get: { confirmReject != nil }, set: { if !$0 { confirmReject = nil } }),
            titleVisibility: .visible,
            presenting: confirmReject
        ) { item in
            Button(item.kind == .proposal ? "Decline" : "Reject", role: .destructive) {
                act { try await model.review(item, .reject, comment: "") }
            }
        } message: { item in
            Text(item.kind == .proposal ? "The coordinator won't start it." : "The agent drops this direction and asks before trying another.")
        }
    }

    // MARK: Swipe actions

    /// Swipe right: approve a review or proposal, or answer a question with its first
    /// (recommended) option.
    @ViewBuilder private func approveAction(_ item: Item) -> some View {
        if item.isPending, item.kind == .review || item.kind == .proposal {
            Button {
                act { try await model.review(item, .approve, comment: "") }
            } label: {
                Label("Approve", systemImage: "checkmark")
            }
            .tint(Theme.accent)
        } else if item.isPending, item.kind == .question, let answers = recommendedAnswers(item) {
            Button {
                act { try await model.answer(item, answers) }
            } label: {
                Label(answers.count == 1 ? answers.values.first! : "Recommended", systemImage: "checkmark")
            }
            .tint(Theme.accent)
        }
    }

    /// Swipe left: reject a review or decline a proposal (confirmed), or mark a reply read.
    @ViewBuilder private func rejectAction(_ item: Item) -> some View {
        if item.isPending, item.kind == .review || item.kind == .proposal {
            Button {
                confirmReject = item
            } label: {
                Label(item.kind == .proposal ? "Decline" : "Reject", systemImage: "xmark")
            }
            .tint(Color(red: 0.55, green: 0.18, blue: 0.16))
        } else if item.isUnreadReply {
            Button {
                model.markRead(ComposerTarget(item))
            } label: {
                Label("Read", systemImage: "envelope.open")
            }
            .tint(Theme.surface)
        }
    }

    /// The first option of every question, if they all have options. Agents put their
    /// recommended option first.
    private func recommendedAnswers(_ item: Item) -> [String: String]? {
        guard let questions = item.questions, !questions.isEmpty else { return nil }
        var answers: [String: String] = [:]
        for q in questions {
            guard let first = q.options?.first?.label else { return nil }
            answers[q.question] = first
        }
        return answers
    }

    private func act(_ work: @escaping () async throws -> Void) {
        actionError = nil
        Task {
            do {
                try await work()
            } catch {
                actionError = error.localizedDescription
            }
        }
    }

    private var segmented: some View {
        HStack(spacing: 0) {
            segment("Pending", count: pending.count, on: !showResolved) { showResolved = false }
            segment("Resolved", count: nil, on: showResolved) { showResolved = true }
        }
        .padding(3)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }

    private func segment(_ title: String, count: Int?, on: Bool, action: @escaping () -> Void) -> some View {
        Button {
            withAnimation(.snappy(duration: 0.2)) { action() }
        } label: {
            HStack(spacing: 6) {
                Text(title).font(.system(size: 14))
                if let count, count > 0 {
                    Text("\(count)")
                        .font(.system(size: 11, weight: .bold).monospacedDigit())
                        .foregroundStyle(Theme.background)
                        .frame(minWidth: 18, minHeight: 18)
                        .background(Theme.accent, in: Circle())
                }
            }
            .foregroundStyle(on ? Theme.text : Theme.secondary)
            .frame(maxWidth: .infinity, minHeight: 34)
            .background(on ? Color.white.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
        .buttonStyle(.plain)
    }
}

private struct InboxRow: View {
    let entry: InboxItem

    private var item: Item { entry.item }

    /// "Question", "Approval"…; a conversation row says how many replies are new.
    private var label: String {
        guard item.needsYou else { return item.outcome }
        if item.kind == .message, entry.unread > 1 { return "Reply · \(entry.unread) new" }
        return item.kindLabel
    }

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            icon.padding(.top, 2)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(label)
                        .font(.system(size: 12))
                        .foregroundStyle(Theme.secondary)
                        .lineLimit(1)
                    Spacer()
                    Text((item.closed ?? item.created).ago)
                        .font(.system(size: 12))
                        .foregroundStyle(Theme.tertiary)
                }
                Text(item.headline)
                    .font(.system(size: 15))
                    .foregroundStyle(Theme.text)
                    .lineLimit(2)
                Text([entry.projectName, entry.threadTitle ?? (item.thread == nil ? "Coordinator" : item.thread!)].joined(separator: " · "))
                    .font(.system(size: 12))
                    .foregroundStyle(Theme.tertiary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 14)
        .contentShape(Rectangle())
    }

    @ViewBuilder private var icon: some View {
        let symbol = switch item.kind {
        case .question: "questionmark"
        case .proposal: "plus"
        case .message: "bubble.left"
        default: "checkmark"
        }
        if item.needsYou {
            Image(systemName: symbol)
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(Theme.background)
                .frame(width: 20, height: 20)
                .background(Theme.accent, in: Circle())
        } else {
            Image(systemName: symbol)
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(Theme.secondary)
                .frame(width: 20, height: 20)
                .overlay(Circle().strokeBorder(Theme.tertiary))
        }
    }
}
