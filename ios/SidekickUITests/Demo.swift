import Foundation

/// Talks to the demo daemon: the app API, and the test agent API (as an agent pane).
enum Demo {
    static let app = URL(string: "http://127.0.0.1:17600")!
    static let agent = URL(string: "http://127.0.0.1:17602")!

    @discardableResult
    static func request(_ url: URL, _ method: String = "GET", _ body: Any? = nil) -> Any? {
        var req = URLRequest(url: url)
        req.httpMethod = method
        if let body { req.httpBody = try? JSONSerialization.data(withJSONObject: body) }
        let done = DispatchSemaphore(value: 0)
        var out: Any?
        URLSession.shared.dataTask(with: req) { data, _, _ in
            if let data { out = (try? JSONSerialization.jsonObject(with: data)) ?? String(decoding: data, as: UTF8.self) }
            done.signal()
        }.resume()
        done.wait()
        return out
    }

    static func origin(_ pane: String) -> [String: String] { ["session": "demo", "pane": pane] }

    static func ask(pane: String, _ question: String, options: [String]) -> String {
        let opts = options.map { o -> [String: String] in
            let parts = o.components(separatedBy: "::")
            return parts.count == 2 ? ["label": parts[0], "description": parts[1]] : ["label": o]
        }
        let r = request(agent.appending(path: "v1/ask"), "POST",
                        ["origin": origin(pane), "async": true, "questions": [["question": question, "options": opts]]]) as? [String: Any]
        return r?["id"] as? String ?? ""
    }

    static func review(pane: String, title: String) -> String {
        let dir = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let page = """
        <!doctype html><meta name=viewport content="width=device-width,initial-scale=1"><title>\(title)</title>
        <h1>\(title)</h1><p>This is a QA artifact for the \(title.lowercased()). It exists to check that reviews render \
        full screen and that approving, refining and rejecting each reach the agent. Nothing real depends on it, so \
        decide either way. The page explains itself in plain words, as every artifact must.</p>
        """
        try? page.write(to: dir.appending(path: "index.html"), atomically: true, encoding: .utf8)
        let raw = request(agent.appending(path: "v1/review"), "POST",
                          ["origin": origin(pane), "path": dir.path, "title": title, "summary": "QA review"])
        let r = raw as? [String: Any]
        if r?["id"] == nil { print("Demo.review failed for \(dir.path): \(String(describing: raw))") }
        return r?["id"] as? String ?? ""
    }

    static func propose(pane: String, title: String) -> String {
        let r = request(agent.appending(path: "v1/propose"), "POST",
                        ["origin": origin(pane), "title": title, "why": "QA: customers keep asking for it."]) as? [String: Any]
        return r?["id"] as? String ?? ""
    }

    /// Sends a message as the user, straight through the app API.
    static func sendMessage(project: String, _ text: String) -> String {
        let r = request(app.appending(path: "api/messages"), "POST", ["project": project, "text": text]) as? [String: Any]
        return r?["id"] as? String ?? ""
    }

    /// The id of a pending message to `pane` whose text contains `text`, if any.
    static func pendingMessage(pane: String, text: String, tries: Int = 10) -> String? {
        for _ in 0..<tries {
            let list = request(agent.appending(path: "v1/messages/pending"), "POST", origin(pane)) as? [[String: Any]] ?? []
            if let hit = list.first(where: { ($0["text"] as? String ?? "").contains(text) }) { return hit["id"] as? String }
            Thread.sleep(forTimeInterval: 0.5)
        }
        return nil
    }

    static func reply(_ id: String, _ text: String) {
        request(agent.appending(path: "v1/messages/\(id)/reply"), "POST", ["text": text])
    }

    /// An item's current state, from the inbox (pending and resolved) or any conversation.
    static func item(_ id: String) -> [String: Any]? {
        for _ in 0..<10 {
            let inbox = request(app.appending(path: "api/inbox")) as? [[String: Any]] ?? []
            if let hit = inbox.first(where: { $0["id"] as? String == id }) { return hit }
            for p in ["acme", "field"] {
                var url = app.appending(path: "api/projects/\(p)")
                if let detail = request(url) as? [String: Any], let items = detail["items"] as? [[String: Any]],
                   let hit = items.first(where: { $0["id"] as? String == id }) { return hit }
                url = app.appending(path: "api/messages")
                url.append(queryItems: [URLQueryItem(name: "project", value: p)])
                if let msgs = request(url) as? [[String: Any]], let hit = msgs.first(where: { $0["id"] as? String == id }) { return hit }
            }
            Thread.sleep(forTimeInterval: 0.5)
        }
        return nil
    }
}
