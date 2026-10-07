import SwiftUI

struct ProjectView: View {
    @Environment(AppModel.self) private var model
    let slug: String

    @State private var expanded: Set<String> = []

    private var detail: ProjectDetail? { model.details[slug] }

    var body: some View {
        ScrollView {
            if let detail {
                VStack(alignment: .leading, spacing: 28) {
                    header(detail)
                    if let plan = detail.plan {
                        PlanSection(plan: plan, slug: slug, threads: detail.threadsList)
                    }
                    if !detail.threadsList.isEmpty {
                        section("Threads") {
                            ForEach(detail.threadsList) { thread in
                                Hairline()
                                ThreadRow(
                                    thread: thread,
                                    slug: slug,
                                    expanded: expanded.contains(thread.id)
                                ) {
                                    withAnimation(.snappy(duration: 0.25)) {
                                        if expanded.contains(thread.id) { expanded.remove(thread.id) } else { expanded.insert(thread.id) }
                                    }
                                }
                            }
                        }
                    }
                    if let artifact = detail.items.first(where: { $0.kind == .review && !$0.isPending }) {
                        section("Latest artifact") {
                            Hairline()
                            NavigationLink(value: Route.item(artifact.id)) {
                                ArtifactRow(title: artifact.title ?? "Artifact", subtitle: "\(artifact.outcome) · \(artifact.created.ago)")
                                    .padding(.vertical, 14)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 24)
            } else {
                ProgressView().padding(.top, 80)
            }
        }
        .readingRoom()
        .inlineTitle()
        .composer(.project(slug))
        .refreshable { await model.refresh(slug) }
        .task { await model.refresh(slug) }
    }

    private func header(_ d: ProjectDetail) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(d.name)
                .font(Theme.serif(32))
                .foregroundStyle(Theme.text)
            if let status = d.status {
                Text("\(status.tone.label) · updated \(status.updated.ago)")
                    .font(.system(size: 12))
                    .foregroundStyle(status.tone == .blocked || status.tone == .atRisk ? Theme.accent : Theme.tertiary)
                Text(status.headline)
                    .font(Theme.serif(22))
                    .foregroundStyle(Theme.text)
                    .fixedSize(horizontal: false, vertical: true)
                if let summary = status.summary, !summary.isEmpty {
                    LinkedText(summary)
                        .font(.system(size: 16))
                        .foregroundStyle(Theme.text.opacity(0.85))
                        .lineSpacing(3)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    ForEach(status.notes ?? [], id: \.self) { note in
                        Text(note).font(.system(size: 14)).foregroundStyle(Theme.secondary)
                    }
                }
                if let link = status.link?.asLink {
                    LinkPill(url: link)
                }
            } else {
                Text(d.goal ?? "No status yet.")
                    .font(.system(size: 15))
                    .foregroundStyle(Theme.secondary)
            }
        }
        .padding(.top, 4)
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionTitle(title).padding(.bottom, 10)
            content()
            Hairline()
        }
    }

}

/// A thread that expands in place to its summary, its link and the way in.
private struct ThreadRow: View {
    let thread: Thread
    let slug: String
    let expanded: Bool
    let toggle: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button(action: toggle) {
                HStack(alignment: .center, spacing: 14) {
                    AgentMarker(group: thread.group)
                    Text(thread.title)
                        .font(.system(size: 15))
                        .foregroundStyle(Theme.text)
                        .lineLimit(expanded ? nil : 2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(label)
                        .font(.system(size: 12))
                        .foregroundStyle(thread.group == "waiting-on-you" ? Theme.accent : Theme.secondary)
                        .fixedSize()
                    Image(systemName: "chevron.down")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(Theme.tertiary)
                        .rotationEffect(.degrees(expanded ? 180 : 0))
                }
                .padding(.vertical, 13)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if expanded {
                VStack(alignment: .leading, spacing: 14) {
                    LinkedText(thread.summary?.isEmpty == false ? thread.summary! : (thread.line ?? "No summary yet."))
                        .font(.system(size: 14))
                        .foregroundStyle(Theme.text.opacity(0.82))
                        .lineSpacing(2)
                        .fixedSize(horizontal: false, vertical: true)
                    // Open sits on the left: the floating + covers the trailing edge.
                    HStack(spacing: 10) {
                        NavigationLink(value: Route.thread(project: slug, id: thread.id)) {
                            HStack(spacing: 4) {
                                Text("Open thread")
                                Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold))
                            }
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(Theme.text)
                            .padding(.horizontal, 11)
                            .padding(.vertical, 6)
                            .overlay(Capsule().strokeBorder(Theme.line))
                        }
                        .buttonStyle(.plain)
                        if let link = (thread.link ?? thread.pr)?.asLink {
                            LinkPill(url: link)
                        }
                        Spacer(minLength: 0)
                    }
                }
                .padding(.leading, 34)
                .padding(.bottom, 16)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
    }

    private var label: String {
        switch thread.group {
        case "waiting-on-you": "Waiting"
        case "ready-for-review": "Ready for review"
        case "landing": "Landing"
        case "working": "In progress"
        case "idle": "Idle"
        default: thread.group.capitalized
        }
    }
}

/// Coral check = ready, coral dot = working, coral ring = needs you, empty ring = idle.
private struct AgentMarker: View {
    let group: String

    var body: some View {
        ZStack {
            switch group {
            case "ready-for-review", "landing":
                Circle().fill(Theme.accent)
                Image(systemName: "checkmark").font(.system(size: 10, weight: .bold)).foregroundStyle(Theme.background)
            case "working":
                Circle().fill(Theme.accent)
            case "waiting-on-you":
                Circle().strokeBorder(Theme.accent, lineWidth: 1.5)
                Circle().fill(Theme.accent).padding(6)
            default:
                Circle().strokeBorder(Theme.tertiary, lineWidth: 1.2)
            }
        }
        .frame(width: 20, height: 20)
    }
}

/// The coordinator's plan: focus, work by priority and stage, merge order, next.
private struct PlanSection: View {
    let plan: Plan
    let slug: String
    let threads: [Thread]

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            if let focus = plan.focus, !focus.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    SectionTitle("Focus")
                    Text(focus).font(Theme.serif(18)).foregroundStyle(Theme.text)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            if !plan.work.isEmpty {
                block("Priorities") {
                    ForEach(Array(plan.work.enumerated()), id: \.offset) { i, work in
                        Hairline()
                        WorkRow(rank: i + 1, work: work, slug: slug, threadTitle: threads.first { $0.id == work.thread }?.title)
                    }
                }
            }
            if let merges = plan.mergeOrder, !merges.isEmpty {
                block("Merge order") {
                    ForEach(Array(merges.enumerated()), id: \.offset) { i, m in
                        Hairline()
                        HStack(alignment: .firstTextBaseline, spacing: 12) {
                            Text("\(i + 1)").font(.system(size: 13, weight: .semibold).monospacedDigit())
                                .foregroundStyle(Theme.tertiary).frame(width: 16)
                            VStack(alignment: .leading, spacing: 8) {
                                Text(m.title).font(.system(size: 15)).foregroundStyle(Theme.text)
                                if let note = m.note, !note.isEmpty {
                                    Text(note).font(.system(size: 12)).foregroundStyle(Theme.secondary)
                                }
                                if let url = m.pr.asLink { LinkPill(url: url) }
                            }
                        }
                        .padding(.vertical, 12)
                    }
                }
            }
            if let next = plan.next, !next.isEmpty {
                block("Up next") {
                    ForEach(Array(next.enumerated()), id: \.offset) { _, n in
                        Hairline()
                        VStack(alignment: .leading, spacing: 3) {
                            Text(n.title).font(.system(size: 15)).foregroundStyle(Theme.text.opacity(0.85))
                            if let note = n.note, !note.isEmpty {
                                Text(note).font(.system(size: 12)).foregroundStyle(Theme.secondary)
                            }
                        }
                        .padding(.vertical, 11)
                    }
                }
            }
            Text("Plan updated \(plan.updated.ago)").font(.system(size: 11)).foregroundStyle(Theme.tertiary)
        }
    }

    private func block<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionTitle(title).padding(.bottom, 10)
            content()
            Hairline()
        }
    }
}

private struct WorkRow: View {
    let rank: Int
    let work: Plan.Work
    let slug: String
    let threadTitle: String?

    var body: some View {
        let row = HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text("\(rank)").font(.system(size: 13, weight: .semibold).monospacedDigit())
                .foregroundStyle(Theme.tertiary).frame(width: 16)
            VStack(alignment: .leading, spacing: 6) {
                HStack(alignment: .firstTextBaseline) {
                    Text(work.title).font(.system(size: 15)).foregroundStyle(Theme.text)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    StageChip(stage: work.stage)
                }
                if let note = work.note, !note.isEmpty {
                    Text(note).font(.system(size: 12)).foregroundStyle(Theme.secondary)
                }
                if work.stage == "blocked" { blocker }
                if let url = work.pr?.asLink { LinkPill(url: url) }
            }
        }
        .padding(.vertical, 12)
        .contentShape(Rectangle())
        // Work waiting on the user opens what it's waiting for; otherwise its thread.
        if let ask = work.ask {
            NavigationLink(value: Route.item(ask)) { row }.buttonStyle(.plain)
        } else if let tid = work.thread {
            NavigationLink(value: Route.thread(project: slug, id: tid)) { row }.buttonStyle(.plain)
        } else {
            row
        }
    }

    @ViewBuilder private var blocker: some View {
        if work.ask != nil {
            Label("Waiting on your answer", systemImage: "arrow.turn.down.right")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(Theme.accent)
        } else if work.waitsOnUser {
            Text("Waiting on you, but nothing's in your inbox yet. The coordinator has been asked to send it.")
                .font(.system(size: 12))
                .foregroundStyle(Theme.tertiary)
        } else if let waitingOn = work.waitingOn, !waitingOn.isEmpty {
            Text("Waiting on \(waitingOn)").font(.system(size: 12)).foregroundStyle(Theme.secondary)
        }
    }
}

/// research → planning → building → review → merging → done, or blocked.
struct StageChip: View {
    let stage: String

    var body: some View {
        HStack(spacing: 5) {
            Circle().fill(color).frame(width: 6, height: 6)
            Text(stage.capitalized)
        }
        .font(.system(size: 11, weight: .medium))
        .foregroundStyle(color)
        .fixedSize()
    }

    private var color: Color {
        switch stage {
        case "research": Color(red: 0.55, green: 0.70, blue: 1.0)
        case "planning": Color(red: 0.70, green: 0.62, blue: 1.0)
        case "building": Theme.accent
        case "review": PRLook.running
        case "merging": PRLook.merged
        case "blocked": PRLook.failing
        case "done": PRLook.passing
        default: Theme.secondary
        }
    }
}
