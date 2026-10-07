import SwiftUI

/// A review, artifact first: the page fills the screen and the decision sits at the
/// bottom as three circles: reject, refine (tap to type, hold to talk), approve.
struct ApprovalView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let item: Item

    @State private var refining = false
    @State private var feedback = ""
    @State private var confirmReject = false
    @State private var showSummary = false
    @State private var sending = false
    @State private var error: String?
    @FocusState private var feedbackFocused: Bool

    var body: some View {
        Group {
            if item.kind == .proposal {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        Text(item.title ?? "Proposed thread").font(Theme.serif(28)).foregroundStyle(Theme.text)
                            .fixedSize(horizontal: false, vertical: true)
                        if let why = item.summary, !why.isEmpty {
                            Text(why).font(.system(size: 16)).foregroundStyle(Theme.text.opacity(0.85)).lineSpacing(3)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        if let plan = item.text, !plan.isEmpty {
                            Hairline()
                            MarkdownView(text: plan)
                        }
                    }
                    .padding(20)
                }
            } else if let url = item.url, url.pathExtension.lowercased() == "md" {
                MarkdownFile(url: url)
            } else if let url = item.url {
                WebView(url: url).ignoresSafeArea(edges: .bottom)
            } else {
                ContentUnavailableView("No artifact", systemImage: "doc")
            }
        }
        .readingRoom()
        .hidesTabBar()
        .inlineTitle()
        .toolbar {
            ToolbarItem(placement: .principal) {
                Button { withAnimation(.snappy) { showSummary.toggle() } } label: {
                    VStack(spacing: 1) {
                        Text(item.title ?? "Review").font(.system(size: 15, weight: .semibold)).foregroundStyle(Theme.text)
                        Text(model.source(of: item)).font(.system(size: 11)).foregroundStyle(Theme.secondary)
                    }
                    .lineLimit(1)
                }
                .buttonStyle(.plain)
                .accessibilityHint("Shows what the agent wants you to look at")
            }
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            if item.kind != .proposal, showSummary || !item.isPending, let summary = item.summary, !summary.isEmpty {
                Text(summary)
                    .font(.system(size: 14))
                    .foregroundStyle(Theme.text.opacity(0.85))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 20)
                    .padding(.vertical, 12)
                    .background(Theme.background)
                    .overlay(alignment: .bottom) { Hairline() }
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if item.isPending { decision } else { outcome }
        }
        .confirmationDialog(item.kind == .proposal ? "Decline this thread?" : "Reject this?", isPresented: $confirmReject, titleVisibility: .visible) {
            Button(item.kind == .proposal ? "Decline" : "Reject", role: .destructive) { send(.reject) }
        } message: {
            Text(item.kind == .proposal ? "The coordinator won't start it." : "The agent drops this direction and asks before trying another.")
        }
    }

    private var decision: some View {
        VStack(spacing: 12) {
            if let error {
                Text(error).font(.system(size: 12)).foregroundStyle(Theme.accent)
            }
            HStack(alignment: .top, spacing: 0) {
                circle(item.kind == .proposal ? "Decline" : "Reject") {
                    Button { confirmReject = true } label: {
                        Image(systemName: "xmark")
                            .font(.system(size: 20, weight: .semibold))
                            .foregroundStyle(Theme.text)
                            .frame(width: 60, height: 60)
                            .background(Theme.surface, in: Circle())
                            .overlay(Circle().strokeBorder(Theme.line))
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(item.kind == .proposal ? "Decline" : "Reject")
                }
                circle(refining ? "Close" : "Refine") {
                    HoldToTalkCircle(
                        size: 60,
                        filled: false,
                        name: "Refine",
                        onTap: { toggleRefine() },
                        onTranscript: { spoken in
                            if !spoken.isEmpty { feedback = feedback.isEmpty ? spoken : feedback + " " + spoken }
                            withAnimation(.snappy) { refining = true }
                        }
                    ) {
                        Image(systemName: refining ? "chevron.down" : "text.bubble")
                            .font(.system(size: 20, weight: .semibold))
                            .foregroundStyle(Theme.text)
                    }
                }
                circle("Approve") {
                    Button { send(.approve) } label: {
                        Image(systemName: sending ? "ellipsis" : "checkmark")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundStyle(Theme.background)
                            .frame(width: 60, height: 60)
                            .background(Theme.accent, in: Circle())
                    }
                    .buttonStyle(.plain)
                    .disabled(sending)
                    .accessibilityLabel("Approve")
                }
            }
            .padding(.horizontal, 24)
            if refining {
                VStack(alignment: .leading, spacing: 8) {
                    HStack(alignment: .bottom, spacing: 8) {
                        TextField("", text: $feedback, prompt: Text("What should change?").foregroundStyle(Theme.tertiary), axis: .vertical)
                            .lineLimit(1...6)
                            .font(.system(size: 15))
                            .foregroundStyle(Theme.text)
                            .tint(Theme.accent)
                            .focused($feedbackFocused)
                            .padding(.vertical, 8)
                        MicButton(text: $feedback)
                        Button { send(.changes) } label: {
                            Image(systemName: "arrow.up")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundStyle(Theme.background)
                                .frame(width: 32, height: 32)
                                .background(Theme.accent.opacity(hasFeedback ? 1 : 0.35), in: Circle())
                        }
                        .disabled(!hasFeedback || sending)
                        .accessibilityLabel("Send changes")
                    }
                    .padding(.leading, 14)
                    .padding(.trailing, 8)
                    .padding(.vertical, 6)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(Theme.line))
                    DictationNote().padding(.leading, 6)
                }
                .padding(.horizontal, 16)
                .transition(.opacity.combined(with: .move(edge: .bottom)))
            }
        }
        .padding(.top, 12)
        .padding(.bottom, 8)
        .background(LinearGradient(colors: [Theme.background.opacity(0), Theme.background.opacity(0.92), Theme.background],
                                   startPoint: .top, endPoint: .center).ignoresSafeArea())
    }

    private func circle<Content: View>(_ label: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(spacing: 6) {
            content()
            Text(label).font(.system(size: 12)).foregroundStyle(Theme.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    private var outcome: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("\(item.outcome)\(item.closed.map { " · \($0.ago)" } ?? "")")
                .font(.system(size: 15, weight: .medium))
                .foregroundStyle(Theme.text)
            if let comment = item.comment, !comment.isEmpty {
                Text(comment).font(.system(size: 14)).foregroundStyle(Theme.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 20)
        .padding(.vertical, 14)
        .background(Theme.background)
        .overlay(alignment: .top) { Hairline() }
    }

    private var hasFeedback: Bool { !feedback.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    private func toggleRefine() {
        withAnimation(.snappy) { refining.toggle() }
        feedbackFocused = refining
    }

    private func send(_ verdict: API.Verdict) {
        sending = true
        error = nil
        Task {
            do {
                try await model.review(item, verdict, comment: feedback.trimmingCharacters(in: .whitespacesAndNewlines))
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
            sending = false
        }
    }
}

/// A markdown artifact, rendered natively.
private struct MarkdownFile: View {
    @Environment(AppModel.self) private var model
    let url: URL
    @State private var text: String?

    var body: some View {
        ScrollView {
            if let text {
                MarkdownView(text: text).padding(20)
            } else {
                ProgressView().padding(.top, 80)
            }
        }
        .task { text = try? await model.api?.text(at: url) }
    }
}
