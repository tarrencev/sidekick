import SwiftUI

/// A round button that does two things: tap, or press and hold to talk.
/// Holding records with on-device Parakeet; releasing delivers the transcript.
struct HoldToTalkCircle<Label: View>: View {
    @Environment(Dictation.self) private var dictation
    var size: CGFloat = 56
    var filled = true
    var name = "Compose"
    let onTap: () -> Void
    let onTranscript: (String) -> Void
    @ViewBuilder var label: Label

    @State private var id = UUID()
    @State private var touching = false
    @State private var holding = false
    @State private var holdTask: Task<Void, Never>?

    var body: some View {
        let active = dictation.isActive(id)
        ZStack {
            if active, dictation.phase == .recording {
                Circle()
                    .fill(Theme.accent.opacity(0.28))
                    .frame(width: size, height: size)
                    .scaleEffect(1.15 + CGFloat(dictation.level) * 0.7)
                    .animation(.easeOut(duration: 0.12), value: dictation.level)
            }
            Circle()
                .fill(filled || active ? Theme.accent : Theme.surface)
                .overlay(Circle().strokeBorder(filled || active ? .clear : Theme.line))
                .frame(width: size, height: size)
                .shadow(color: .black.opacity(0.35), radius: 10, y: 4)
            Group {
                if active, dictation.phase == .preparing || dictation.phase == .transcribing {
                    ProgressView().tint(filled ? Theme.background : Theme.accent)
                } else if active {
                    Image(systemName: "waveform")
                        .font(.system(size: size * 0.36, weight: .semibold))
                        .foregroundStyle(Theme.background)
                        .symbolEffect(.variableColor.iterative, isActive: dictation.phase == .recording)
                } else {
                    label
                }
            }
        }
        .scaleEffect(touching ? 0.94 : 1)
        .animation(.snappy(duration: 0.15), value: touching)
        .contentShape(Circle())
        .gesture(
            DragGesture(minimumDistance: 0)
                .onChanged { _ in
                    guard !touching else { return }
                    touching = true
                    holdTask = Task { @MainActor in
                        try? await Task.sleep(for: .milliseconds(280))
                        guard !Task.isCancelled, touching else { return }
                        holding = true
                        dictation.begin(id, deliver: onTranscript)
                    }
                }
                .onEnded { _ in
                    touching = false
                    holdTask?.cancel()
                    if holding {
                        holding = false
                        dictation.end(id)
                    } else {
                        onTap()
                    }
                }
        )
        .sensoryFeedback(.impact(weight: .medium), trigger: holding)
        .accessibilityElement()
        .accessibilityLabel(name)
        .accessibilityIdentifier(name)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { onTap() }
        .accessibilityHint("Tap to type. Press and hold to talk.")
    }
}

extension View {
    /// Adds the floating + composer. `target` is who messages go to; nil asks for a project.
    func composer(_ target: ComposerTarget?) -> some View {
        modifier(FloatingComposer(fixedTarget: target))
    }
}

private struct FloatingComposer: ViewModifier {
    @Environment(AppModel.self) private var model
    let fixedTarget: ComposerTarget?

    @State private var open = false
    @State private var text = ""
    @State private var pickedProject: String?

    private var target: ComposerTarget? {
        if let fixedTarget { return fixedTarget }
        let slug = pickedProject ?? model.lastProject ?? model.projects.first?.slug
        return slug.map { .project($0) }
    }

    private func openIfRequested() {
        guard let key = model.requestedConversation, let fixedTarget, fixedTarget.key == key else { return }
        model.requestedConversation = nil
        open = true
    }

    func body(content: Content) -> some View {
        content
        #if DEBUG
        .onAppear { if UserDefaults.standard.bool(forKey: "debugComposerOpen") { open = true } }
        #endif
        // Opened from the Inbox or a reply notification: show that conversation.
        .onAppear(perform: openIfRequested)
        .onChange(of: model.requestedConversation) { _, _ in openIfRequested() }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if !open {
                HStack {
                    Spacer()
                    HoldToTalkCircle(
                        onTap: { withAnimation(.snappy(duration: 0.3)) { open = true } },
                        onTranscript: { spoken in
                            if !spoken.isEmpty { text = text.isEmpty ? spoken : text + " " + spoken }
                            withAnimation(.snappy(duration: 0.3)) { open = true }
                        }
                    ) {
                        Image(systemName: "plus")
                            .font(.system(size: 22, weight: .semibold))
                            .foregroundStyle(Theme.background)
                    }
                    .overlay(alignment: .topTrailing) {
                        if let target, model.unreadReplies.contains(target.key) {
                            Circle().fill(Theme.text).frame(width: 12, height: 12)
                                .overlay(Circle().strokeBorder(Theme.background, lineWidth: 2))
                        }
                    }
                }
                .padding(.trailing, 20)
                .padding(.bottom, 12)
                .overlay(alignment: .bottomLeading) { DictationNote().padding(.leading, 20).padding(.bottom, 30) }
            }
        }
        #if os(iOS)
        // Full screen means no tab bar under the input. .automatic leaves screens that
        // already hide it (questions, reviews, threads) as they are once the chat closes.
        .toolbar(open ? .hidden : .automatic, for: .tabBar)
        #endif
        // The chat takes the whole content area: room for long replies.
        .overlay {
            if open, let target {
                ComposerPanel(
                    target: target,
                    canPickProject: fixedTarget == nil,
                    text: $text,
                    pick: { slug in pickedProject = slug; model.lastProject = slug },
                    close: { withAnimation(.snappy(duration: 0.25)) { open = false } }
                )
                .transition(.asymmetric(insertion: .scale(scale: 0.96, anchor: .bottomTrailing).combined(with: .opacity),
                                        removal: .opacity))
            }
        }
    }
}

/// The expanded composer: who it goes to, the conversation so far, and the input.
private struct ComposerPanel: View {
    @Environment(AppModel.self) private var model
    let target: ComposerTarget
    let canPickProject: Bool
    @Binding var text: String
    let pick: (String) -> Void
    let close: () -> Void

    @State private var sending = false
    @State private var error: String?
    @State private var contentHeight: CGFloat = 0
    @FocusState private var focused: Bool

    private var conversation: [Item] { model.messages[target.key] ?? [] }

    var body: some View {
        VStack(spacing: 0) {
            header
            Hairline()
            if conversation.isEmpty {
                VStack(spacing: 10) {
                    Text("Write to \(recipient)")
                        .font(Theme.serif(22))
                        .foregroundStyle(Theme.text)
                    Text(hint)
                        .font(.system(size: 13))
                        .foregroundStyle(Theme.tertiary)
                        .multilineTextAlignment(.center)
                }
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 22) {
                            ForEach(conversation) { MessageExchange(item: $0).id($0.id) }
                        }
                        .padding(.horizontal, 20)
                        .padding(.vertical, 20)
                        .frame(maxWidth: 760)
                        .frame(maxWidth: .infinity)
                    }
                    .defaultScrollAnchor(.bottom)
                    .onChange(of: conversation.last?.state) { _, _ in
                        if let last = conversation.last { withAnimation { proxy.scrollTo(last.id, anchor: .bottom) } }
                    }
                    .onChange(of: conversation.count) { _, _ in
                        if let last = conversation.last { withAnimation { proxy.scrollTo(last.id, anchor: .bottom) } }
                    }
                }
            }
            Hairline()
            VStack(alignment: .leading, spacing: 6) {
                if let error {
                    Text(error).font(.system(size: 12)).foregroundStyle(Theme.accent)
                }
                HStack(alignment: .bottom, spacing: 8) {
                    TextField("", text: $text, prompt: Text(placeholder).foregroundStyle(Theme.tertiary), axis: .vertical)
                        .lineLimit(1...10)
                        .font(.system(size: 15))
                        .foregroundStyle(Theme.text)
                        .tint(Theme.accent)
                        .textFieldStyle(.plain)
                        .focused($focused)
                        .padding(.vertical, 9)
                        .sendsOnReturn($text) { if canSend { send() } }
                    MicButton(text: $text)
                    Button(action: send) {
                        Image(systemName: sending ? "ellipsis" : "arrow.up")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundStyle(Theme.background)
                            .frame(width: 32, height: 32)
                            .background(Theme.accent.opacity(canSend ? 1 : 0.35), in: Circle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!canSend)
                    .accessibilityLabel("Send")
                }
                .padding(.leading, 16)
                .padding(.trailing, 8)
                .padding(.vertical, 6)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(Theme.line))
                DictationNote()
            }
            .frame(maxWidth: 760)
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            .frame(maxWidth: .infinity)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.background.ignoresSafeArea())
        .task(id: target) {
            model.openConversation = target.key
            model.markRead(target)
            await model.refreshMessages(target)
        }
        .onDisappear { model.openConversation = nil }
        .onAppear { if text.isEmpty { focused = true } }
    }

    private var hint: String {
        #if os(macOS)
        "Return sends, Shift-Return adds a line. Hold the mic to talk. Replies appear here."
        #else
        "Replies appear here. Hold the mic or the + to talk."
        #endif
    }

    private var header: some View {
        HStack(spacing: 8) {
            Text("To").font(.system(size: 13)).foregroundStyle(Theme.tertiary)
            if canPickProject {
                Menu {
                    ForEach(model.projects) { p in Button(p.name) { pick(p.slug) } }
                } label: {
                    HStack(spacing: 4) {
                        Text(recipient).lineLimit(1)
                        Image(systemName: "chevron.up.chevron.down").font(.system(size: 10))
                    }
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Theme.text)
                }
            } else {
                Text(recipient).font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.text).lineLimit(1)
            }
            Spacer()
            Button(action: close) {
                Image(systemName: "xmark").font(.system(size: 14, weight: .semibold)).foregroundStyle(Theme.secondary)
                    .frame(width: 32, height: 32)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .keyboardShortcut(.cancelAction) // Esc on the Mac
            .accessibilityLabel("Close")
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private var recipient: String {
        let project = model.projects.first { $0.slug == target.project }?.name ?? target.project
        guard let tid = target.thread else { return "\(project) · coordinator" }
        let title = model.details[target.project]?.threadsList.first { $0.id == tid }?.title ?? tid
        return title
    }

    private var placeholder: String {
        target.thread == nil ? "Ask or tell the coordinator…" : "Message this thread…"
    }

    private var canSend: Bool { !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !sending }

    private func send() {
        let body = text.trimmingCharacters(in: .whitespacesAndNewlines)
        sending = true
        error = nil
        Task {
            do {
                try await model.send(target, body)
                text = ""
            } catch {
                self.error = error.localizedDescription
            }
            sending = false
        }
    }
}

/// One message and its answer.
private struct MessageExchange: View {
    let item: Item

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Spacer(minLength: 40)
                Text(item.text ?? "")
                    .font(.system(size: 14))
                    .foregroundStyle(Theme.text)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Color.white.opacity(0.07), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            }
            switch item.state {
            case "replied":
                MarkdownView(text: item.reply ?? "")
            case "failed":
                Text(item.error ?? "Couldn't deliver this message.")
                    .font(.system(size: 13)).foregroundStyle(Theme.accent)
            default:
                HStack(spacing: 6) {
                    ProgressView().controlSize(.mini).tint(Theme.secondary)
                    Text("Delivered · waiting for a reply").font(.system(size: 12)).foregroundStyle(Theme.tertiary)
                }
            }
        }
    }
}
