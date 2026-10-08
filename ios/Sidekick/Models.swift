import Foundation

struct ProjectSummary: Codable, Identifiable, Hashable {
    let slug: String
    let name: String
    let goal: String?
    let status: ProjectStatus?
    let pending: Int
    let threads: [String: Int]

    var id: String { slug }
}

struct ProjectDetail: Codable {
    let slug: String
    let name: String
    let goal: String?
    let status: ProjectStatus?
    let pending: Int
    let items: [Item]
    let threadsList: [Thread]
    let plan: Plan?
    let prs: [String: PRStatus]?
}

/// The coordinator's structured plan.
struct Plan: Codable, Hashable {
    let focus: String?
    let work: [Work]
    let mergeOrder: [Merge]?
    let next: [Next]?
    let updated: Date

    struct Work: Codable, Hashable {
        let title: String
        let stage: String
        let thread: String?
        let pr: String?
        let note: String?
        let waitingOn: String?
        /// The inbox item that unblocks this work, filled in by the daemon.
        let ask: String?

        var waitsOnUser: Bool {
            stage == "blocked" && (ask != nil || waitingOn?.lowercased() == "user")
        }
    }

    struct Merge: Codable, Hashable {
        let pr: String
        let title: String
        let note: String?
    }

    struct Next: Codable, Hashable {
        let title: String
        let note: String?
    }
}

struct PRStatus: Codable, Hashable {
    let state: String  // open | draft | merged | closed
    let checks: String // passing | failing | pending | none
}

struct ProjectStatus: Codable, Hashable {
    let headline: String
    let summary: String?
    let link: String?
    let state: String
    let notes: [String]?
    let updated: Date

    var tone: StatusTone { StatusTone(rawValue: state) ?? .onTrack }
}

enum StatusTone: String {
    case onTrack = "on-track", atRisk = "at-risk", blocked, done

    var label: String {
        switch self {
        case .onTrack: "On track"
        case .atRisk: "At risk"
        case .blocked: "Blocked"
        case .done: "Done"
        }
    }
}

struct Thread: Codable, Identifiable, Hashable {
    let id: String
    let title: String
    let group: String
    let line: String?
    let pr: String?
    let summary: String?
    let link: String?
}

struct ThreadDetail: Codable {
    let id: String
    let title: String
    let group: String
    let line: String?
    let pr: String?
    let summary: String?
    let link: String?
    let report: String?
    let files: [LibraryFile]
    let items: [Item]
    let prs: [String: PRStatus]?
}

struct LibraryFile: Codable, Hashable {
    let name: String
    let url: URL
}

extension Thread {
    var prURL: URL? { pr.flatMap { $0.isEmpty ? nil : URL(string: $0) } }
}

/// A short label for a link: "PR #2629" for a GitHub pull request, else the host.
func linkLabel(_ url: URL) -> String {
    let parts = url.pathComponents
    if url.host()?.hasSuffix("github.com") == true, parts.count >= 2, parts[parts.count - 2] == "pull", Int(parts.last!) != nil {
        return "PR #\(parts.last!)"
    }
    return url.host() ?? url.absoluteString
}

extension String {
    /// A URL from a stored link, if it is one.
    var asLink: URL? {
        let s = trimmingCharacters(in: .whitespaces)
        return s.isEmpty ? nil : URL(string: s)
    }
}

struct Item: Codable, Identifiable, Hashable {
    let id: String
    let project: String
    let thread: String?
    let kind: Kind
    let state: String
    let created: Date
    let closed: Date?

    let questions: [Question]?
    let answers: [String: String]?

    let title: String?
    let summary: String?
    let url: URL?
    let comment: String?

    let text: String?
    let reply: String?
    let error: String?
    let seen: Bool?
    let attachments: [Attachment]?

    enum Kind: String, Codable { case question, review, message, proposal }

    var isPending: Bool { state == "pending" }

    /// An agent's reply the user hasn't read yet.
    var isUnreadReply: Bool { kind == .message && state == "replied" && seen != true }

    /// Whether this belongs in the Inbox's pending list.
    var needsYou: Bool { isPending || isUnreadReply }
}

/// An item with the names needed to show it outside its project.
struct InboxItem: Codable, Identifiable, Hashable {
    let item: Item
    let projectName: String
    let threadTitle: String?

    var id: String { item.id }

    init(from decoder: Decoder) throws {
        item = try Item(from: decoder)
        let c = try decoder.container(keyedBy: Keys.self)
        projectName = try c.decode(String.self, forKey: .projectName)
        threadTitle = try c.decodeIfPresent(String.self, forKey: .threadTitle)
    }

    func encode(to encoder: Encoder) throws {
        try item.encode(to: encoder)
        var c = encoder.container(keyedBy: Keys.self)
        try c.encode(projectName, forKey: .projectName)
        try c.encodeIfPresent(threadTitle, forKey: .threadTitle)
    }

    private enum Keys: String, CodingKey { case projectName, threadTitle }
}

extension Item {
    /// The one-line description used in lists.
    var headline: String {
        switch kind {
        case .question: questions?.first?.question ?? "Question"
        case .message: (reply ?? text ?? "Reply").split(separator: "\n").first.map(String.init) ?? "Reply"
        default: title ?? "Artifact"
        }
    }

    var kindLabel: String {
        switch kind {
        case .question: "Question"
        case .review: "Approval"
        case .proposal: "Proposed thread"
        case .message: "Reply"
        }
    }

    var outcome: String {
        switch state {
        case "answered": answers?.values.sorted().joined(separator: " · ") ?? "Answered"
        case "approved": "Approved"
        case "changes": "Changes requested"
        case "rejected": kind == .proposal ? "Declined" : "Rejected"
        case "cancelled": "Withdrawn"
        default: "Pending"
        }
    }
}

struct Question: Codable, Hashable {
    let question: String
    let header: String?
    let options: [Option]?
    let multiSelect: Bool?
}

struct Option: Codable, Hashable {
    let label: String
    let description: String?
}

extension JSONDecoder {
    static let sidekick: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = f.date(from: s) { return date }
            f.formatOptions = [.withInternetDateTime]
            if let date = f.date(from: s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
        }
        return d
    }()
}

/// Who a message from the composer goes to.
enum ComposerTarget: Hashable {
    case project(String)
    case thread(String, String)

    var project: String {
        switch self {
        case .project(let p), .thread(let p, _): p
        }
    }

    var thread: String? {
        if case .thread(_, let t) = self { return t }
        return nil
    }

    var key: String { thread.map { "\(project)/\($0)" } ?? project }

    init(_ item: Item) {
        self = item.thread.map { .thread(item.project, $0) } ?? .project(item.project)
    }
}
