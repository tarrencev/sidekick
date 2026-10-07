import SwiftUI

/// One thread: its summary, what it's waiting on, its link, the files it produced and its full report.
struct ThreadView: View {
    @Environment(AppModel.self) private var model
    let slug: String
    let tid: String

    private var detail: ThreadDetail? { model.threads["\(slug)/\(tid)"] }

    var body: some View {
        ScrollView {
            if let d = detail {
                VStack(alignment: .leading, spacing: 28) {
                    VStack(alignment: .leading, spacing: 12) {
                        Text(d.title)
                            .font(Theme.serif(28))
                            .foregroundStyle(Theme.text)
                            .fixedSize(horizontal: false, vertical: true)
                        if let line = d.line, !line.isEmpty {
                            Text(line).font(.system(size: 13)).foregroundStyle(Theme.tertiary)
                        }
                        if let summary = d.summary, !summary.isEmpty {
                            LinkedText(summary)
                                .font(.system(size: 16))
                                .foregroundStyle(Theme.text.opacity(0.9))
                                .lineSpacing(3)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        let links = [d.link, d.pr].compactMap { $0?.asLink }
                        if !links.isEmpty {
                            HStack(spacing: 8) {
                                ForEach(Array(Set(links)).sorted { $0.absoluteString < $1.absoluteString }, id: \.self) { LinkPill(url: $0) }
                            }
                            .padding(.top, 2)
                        }
                    }

                    let reviews = d.items.filter { $0.kind == .review }
                    if !d.files.isEmpty || !reviews.isEmpty {
                        block("Artifacts") {
                            ForEach(reviews) { item in
                                Hairline()
                                NavigationLink(value: Route.item(item.id)) {
                                    ArtifactRow(title: item.title ?? "Artifact", subtitle: "\(item.outcome) · \(item.created.ago)")
                                        .padding(.vertical, 14)
                                }
                                .buttonStyle(.plain)
                            }
                            ForEach(d.files, id: \.self) { file in
                                Hairline()
                                NavigationLink(value: Route.file(title: file.name, url: file.url)) {
                                    ArtifactRow(title: file.name, subtitle: file.url.pathExtension.uppercased())
                                        .padding(.vertical, 14)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }

                    if let report = d.report, !report.isEmpty {
                        VStack(alignment: .leading, spacing: 12) {
                            SectionTitle("Full report")
                            Hairline()
                            MarkdownView(text: report)
                        }
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 32)
            } else {
                ProgressView().padding(.top, 80)
            }
        }
        .readingRoom()
        .hidesTabBar()
        .inlineTitle()
        .composer(.thread(slug, tid), tabBar: false)
        .refreshable { await model.refreshThread(slug, tid) }
        .task { await model.refreshThread(slug, tid) }
    }

    private func block<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionTitle(title).padding(.bottom, 10)
            content()
            Hairline()
        }
    }
}
