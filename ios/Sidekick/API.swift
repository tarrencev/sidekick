import Foundation

struct APIError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

/// Talks to sidekickd over the tailnet.
struct API {
    let base: URL

    func projects() async throws -> [ProjectSummary] {
        try await get("api/projects")
    }

    func project(_ slug: String) async throws -> ProjectDetail {
        try await get("api/projects/\(slug)")
    }

    func thread(_ slug: String, _ tid: String) async throws -> ThreadDetail {
        try await get("api/projects/\(slug)/threads/\(tid)")
    }

    func text(at url: URL) async throws -> String {
        let (data, resp) = try await URLSession.shared.data(from: url)
        try check(resp, data)
        return String(decoding: data, as: UTF8.self)
    }

    /// Registers this device for push; returns whether dl can send it native push.
    func registerDevice(token: String, env: String) async throws -> Bool {
        struct Reply: Decodable { let push: Bool }
        let r: Reply = try await postReturning("api/devices", body: [
            "token": token, "env": env, "topic": Bundle.main.bundleIdentifier ?? "gg.cartridge.sidekick",
        ])
        return r.push
    }

    func inbox() async throws -> [InboxItem] {
        try await get("api/inbox")
    }

    func prompt(_ slug: String, text: String) async throws {
        try await post("api/projects/\(slug)/prompt", body: ["text": text])
    }

    func answer(_ item: Item, answers: [String: String]) async throws {
        try await post("api/items/\(item.id)/answer", body: ["answers": answers])
    }

    enum Verdict: String { case approve, changes, reject }

    func review(_ item: Item, _ verdict: Verdict, comment: String, attachments: [String] = []) async throws {
        struct Body: Encodable { let verdict, comment: String; let attachments: [String] }
        try await post("api/items/\(item.id)/review", body: Body(verdict: verdict.rawValue, comment: comment, attachments: attachments))
    }

    func markSeen(_ target: ComposerTarget) async throws {
        try await post("api/messages/seen", body: ["project": target.project, "thread": target.thread ?? ""])
    }

    func messages(_ target: ComposerTarget) async throws -> [Item] {
        var query = [URLQueryItem(name: "project", value: target.project)]
        if let t = target.thread { query.append(URLQueryItem(name: "thread", value: t)) }
        return try await get("api/messages", query: query)
    }

    func send(_ target: ComposerTarget, text: String, attachments: [String] = []) async throws -> Item {
        struct Body: Encodable { let project, thread, text: String; let attachments: [String] }
        return try await postReturning("api/messages", body: Body(project: target.project, thread: target.thread ?? "", text: text, attachments: attachments))
    }

    func upload(project: String, name: String, data: Data) async throws -> Attachment {
        var url = base.appending(path: "api/uploads")
        url.append(queryItems: [URLQueryItem(name: "project", value: project), URLQueryItem(name: "name", value: name)])
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 300
        let (body, resp) = try await URLSession.shared.upload(for: req, from: data)
        try check(resp, body)
        return try JSONDecoder.sidekick.decode(Attachment.self, from: body)
    }

    /// Yields the slug of each project that changes, until cancelled or disconnected.
    func events() -> AsyncThrowingStream<String, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var req = URLRequest(url: base.appending(path: "api/events"))
                    req.timeoutInterval = 120
                    let (bytes, resp) = try await URLSession.shared.bytes(for: req)
                    try check(resp)
                    var event = ""
                    for try await line in bytes.lines {
                        if line.hasPrefix("event: ") {
                            event = String(line.dropFirst(7))
                        } else if line.hasPrefix("data: "), event == "changed" {
                            continuation.yield(String(line.dropFirst(6)))
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    private func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        var url = base.appending(path: path)
        if !query.isEmpty { url.append(queryItems: query) }
        let (data, resp) = try await URLSession.shared.data(from: url)
        try check(resp, data)
        return try JSONDecoder.sidekick.decode(T.self, from: data)
    }

    private func post(_ path: String, body: some Encodable) async throws {
        var req = URLRequest(url: base.appending(path: path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data)
    }

    private func postReturning<T: Decodable>(_ path: String, body: some Encodable) async throws -> T {
        var req = URLRequest(url: base.appending(path: path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data)
        return try JSONDecoder.sidekick.decode(T.self, from: data)
    }

    private func check(_ resp: URLResponse, _ data: Data = Data()) throws {
        guard let http = resp as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            let text = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
            throw APIError(message: text?.isEmpty == false ? text! : "Server returned \(http.statusCode)")
        }
    }
}
