import SwiftUI

struct InboxView: View {
    @Environment(AppModel.self) private var model
    @State private var showResolved = false

    private var pending: [InboxItem] { model.inbox.filter(\.item.isPending) }
    private var resolved: [InboxItem] {
        model.inbox.filter { !$0.item.isPending }
            .sorted { ($0.item.closed ?? $0.item.created) > ($1.item.closed ?? $1.item.created) }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("Inbox")
                    .font(Theme.serif(34))
                    .foregroundStyle(Theme.text)
                segmented
                let rows = showResolved ? Array(resolved.prefix(50)) : pending
                if rows.isEmpty {
                    Text(showResolved ? "Nothing resolved yet." : "Nothing needs you.")
                        .font(Theme.serif(18))
                        .foregroundStyle(Theme.secondary)
                        .padding(.top, 24)
                } else {
                    VStack(spacing: 0) {
                        ForEach(rows) { entry in
                            NavigationLink(value: Route.item(entry.id)) { InboxRow(entry: entry) }
                                .buttonStyle(.plain)
                            Hairline()
                        }
                    }
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 8)
        }
        .readingRoom()
        .hidesNavigationBar()
        .refreshable { await model.refreshInbox() }
        .composer(nil)
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

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            icon.padding(.top, 2)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(item.isPending ? item.kindLabel : item.outcome)
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
        default: "checkmark"
        }
        if item.isPending {
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
