import SwiftUI
import WebKit

/// Renders an artifact page inside the app. Links that leave the artifact's
/// origin (e.g. a GitHub PR) open outside, so they reach the GitHub app.
#if os(iOS)
struct WebView: UIViewRepresentable {
    let url: URL
    var scrollEnabled = true

    func makeCoordinator() -> WebCoordinator { WebCoordinator(origin: url) }

    func makeUIView(context: Context) -> WKWebView {
        let view = WKWebView()
        view.navigationDelegate = context.coordinator
        view.isOpaque = false
        view.backgroundColor = UIColor(Theme.background)
        view.scrollView.backgroundColor = UIColor(Theme.background)
        view.scrollView.isScrollEnabled = scrollEnabled
        view.allowsBackForwardNavigationGestures = true
        view.load(URLRequest(url: url))
        return view
    }

    func updateUIView(_ view: WKWebView, context: Context) {
        context.coordinator.reloadIfChanged(view, url)
    }
}
#else
struct WebView: NSViewRepresentable {
    let url: URL
    var scrollEnabled = true

    func makeCoordinator() -> WebCoordinator { WebCoordinator(origin: url) }

    func makeNSView(context: Context) -> WKWebView {
        let view = WKWebView()
        view.navigationDelegate = context.coordinator
        view.underPageBackgroundColor = NSColor(Theme.background)
        view.allowsBackForwardNavigationGestures = true
        view.load(URLRequest(url: url))
        return view
    }

    func updateNSView(_ view: WKWebView, context: Context) {
        context.coordinator.reloadIfChanged(view, url)
    }
}
#endif

final class WebCoordinator: NSObject, WKNavigationDelegate {
    var origin: URL
    init(origin: URL) { self.origin = origin }

    func reloadIfChanged(_ view: WKWebView, _ url: URL) {
        if origin.absoluteString != url.absoluteString {
            origin = url
            view.load(URLRequest(url: url))
        }
    }

    @MainActor
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
        guard action.navigationType == .linkActivated, let target = action.request.url,
              target.host() != origin.host() || target.port != origin.port
        else { return .allow }
        #if os(iOS)
        await UIApplication.shared.open(target)
        #else
        NSWorkspace.shared.open(target)
        #endif
        return .cancel
    }
}

/// A full-screen document: markdown is rendered natively, everything else in a web view.
struct FileView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openURL) private var openURL
    let title: String
    let url: URL

    @State private var markdown: String?

    private var isMarkdown: Bool { url.pathExtension.lowercased() == "md" }

    var body: some View {
        Group {
            if isMarkdown {
                ScrollView {
                    if let markdown {
                        MarkdownView(text: markdown)
                            .padding(20)
                    } else {
                        ProgressView().padding(.top, 80)
                    }
                }
                .task { markdown = try? await model.api?.text(at: url) }
            } else {
                WebView(url: url).ignoresSafeArea(edges: .bottom)
            }
        }
        .readingRoom()
        .composer(nil, tabBar: false)
        .hidesTabBar()
        .navigationTitle(title)
        .inlineTitle()
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button { openURL(url) } label: { Image(systemName: "safari") }
                    .accessibilityLabel("Open in Safari")
            }
        }
    }
}
