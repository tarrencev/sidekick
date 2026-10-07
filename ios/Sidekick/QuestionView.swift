import SwiftUI

struct QuestionView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let item: Item

    @State private var picks: [String: Set<String>] = [:]
    @State private var other: [String: String] = [:]
    @State private var sending = false
    @State private var error: String?

    private var questions: [Question] { item.questions ?? [] }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 36) {
                ForEach(questions, id: \.question) { q in
                    questionBlock(q)
                }
                if !item.isPending {
                    Text("\(item.outcome) \(item.closed.map { "· \($0.ago)" } ?? "")")
                        .font(.system(size: 14))
                        .foregroundStyle(Theme.secondary)
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 8)
            .padding(.bottom, 24)
        }
        .readingRoom()
        .hidesTabBar()
        .navigationTitle("Answer question")
        .inlineTitle()
        .composer(ComposerTarget(item))
        .safeAreaInset(edge: .bottom) {
            if item.isPending {
                VStack(spacing: 8) {
                    if let error { Text(error).font(.system(size: 12)).foregroundStyle(Theme.accent) }
                    PrimaryButton(title: "Submit answer", busy: sending, enabled: complete, action: send)
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 10)
                .background(Theme.background)
            }
        }
    }

    private func questionBlock(_ q: Question) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 10) {
                Text(q.question)
                    .font(Theme.serif(28))
                    .foregroundStyle(Theme.text)
                    .fixedSize(horizontal: false, vertical: true)
                Text(context(q))
                    .font(.system(size: 14))
                    .foregroundStyle(Theme.secondary)
            }
            let options = q.options ?? []
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(options.enumerated()), id: \.element.label) { i, option in
                    OptionRow(index: i, option: option, multi: q.multiSelect == true, selected: picks[q.question]?.contains(option.label) == true) {
                        toggle(q, option.label)
                    }
                    .disabled(!item.isPending)
                }
                if item.isPending {
                    VStack(alignment: .leading, spacing: 8) {
                        if !options.isEmpty {
                            Text("Other").font(.system(size: 15)).foregroundStyle(Theme.text)
                        }
                        InputBox(placeholder: options.isEmpty ? "Your answer" : "Share your thoughts…", text: binding(for: q), minLines: 1)
                    }
                    .padding(.top, 14)
                } else if let answer = item.answers?[q.question] {
                    Text("You answered: \(answer)")
                        .font(.system(size: 14))
                        .foregroundStyle(Theme.text)
                        .padding(.top, 14)
                }
            }
        }
    }

    private func context(_ q: Question) -> String {
        var parts = [q.header].compactMap { $0 }.filter { !$0.isEmpty }
        parts.append(model.source(of: item))
        parts.append(item.created.ago)
        if q.multiSelect == true { parts.append("pick any") }
        return parts.joined(separator: " · ")
    }

    private func toggle(_ q: Question, _ label: String) {
        var set = picks[q.question] ?? []
        if q.multiSelect == true {
            if set.contains(label) { set.remove(label) } else { set.insert(label) }
        } else {
            set = set.contains(label) ? [] : [label]
            other[q.question] = ""
        }
        picks[q.question] = set
    }

    private func binding(for q: Question) -> Binding<String> {
        Binding {
            other[q.question] ?? ""
        } set: { text in
            other[q.question] = text
            if q.multiSelect != true, !text.isEmpty { picks[q.question] = [] }
        }
    }

    /// Picked labels in option order, plus any typed text.
    private func answer(for q: Question) -> String? {
        let set = picks[q.question] ?? []
        var parts = (q.options ?? []).map(\.label).filter(set.contains)
        let typed = (other[q.question] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if !typed.isEmpty { parts.append(typed) }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    private var complete: Bool { questions.allSatisfy { answer(for: $0) != nil } }

    private func send() {
        var answers: [String: String] = [:]
        for q in questions { answers[q.question] = answer(for: q) }
        sending = true
        error = nil
        Task {
            do {
                try await model.answer(item, answers)
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
            sending = false
        }
    }
}

private struct OptionRow: View {
    let index: Int
    let option: Option
    let multi: Bool
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(alignment: .top, spacing: 14) {
                ZStack {
                    if multi {
                        RoundedRectangle(cornerRadius: 5).strokeBorder(selected ? Theme.accent : Theme.tertiary, lineWidth: 1.3)
                        if selected { Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.accent) }
                    } else {
                        Circle().strokeBorder(selected ? Theme.accent : Theme.tertiary, lineWidth: 1.3)
                        if selected { Circle().fill(Theme.accent).padding(5) }
                    }
                }
                .frame(width: 20, height: 20)
                .padding(.top, 1)
                VStack(alignment: .leading, spacing: 3) {
                    Text("\(letter). \(option.label)")
                        .font(.system(size: 15))
                        .foregroundStyle(Theme.text)
                    if let d = option.description, !d.isEmpty {
                        Text(d)
                            .font(.system(size: 13))
                            .foregroundStyle(Theme.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(.vertical, 12)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .sensoryFeedback(.selection, trigger: selected)
    }

    private var letter: String { String(UnicodeScalar(UInt8(65 + index % 26))) }
}
