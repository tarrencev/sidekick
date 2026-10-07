import SwiftUI
import UserNotifications
#if os(iOS)
import UIKit
typealias PlatformAppDelegate = UIApplicationDelegate
#else
import AppKit
typealias PlatformAppDelegate = NSApplicationDelegate
#endif

/// Native push: registers with APNs, hands the device token to dl, and opens the
/// right screen when a notification is tapped.
final class AppDelegate: NSObject, PlatformAppDelegate, UNUserNotificationCenterDelegate {
    #if os(iOS)
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        registered(deviceToken)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        failed(error)
    }
    #else
    func applicationDidFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
    }

    func application(_ application: NSApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        registered(deviceToken)
    }

    func application(_ application: NSApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        failed(error)
    }

    /// Closing the window keeps Sidekick running in the menu bar.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    #endif

    private func registered(_ deviceToken: Data) {
        let token = deviceToken.map { String(format: "%02x", $0) }.joined()
        Task { @MainActor in await AppModel.shared.registerDevice(token) }
    }

    private func failed(_ error: Error) {
        Task { @MainActor in AppModel.shared.pushError = error.localizedDescription }
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .list, .sound])
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let info = response.notification.request.content.userInfo
        let link = DeepLink(
            item: info["item"] as? String,
            project: info["project"] as? String,
            thread: (info["thread"] as? String).flatMap { $0.isEmpty ? nil : $0 },
            kind: info["kind"] as? String
        )
        Task { @MainActor in AppModel.shared.deepLink = link }
        completionHandler()
    }
}

/// Where a tapped notification should take the user.
struct DeepLink: Equatable {
    let item: String?
    let project: String?
    let thread: String?
    let kind: String?

    /// Inbox items open in the Inbox tab; replies open their conversation's page.
    var isReply: Bool { kind == "reply" }

    var routes: [Route] {
        // A reply opens its conversation (via the message item's page).
        if isReply, let item { return [.item(item)] }
        if isReply, let project {
            return [thread.map { .thread(project: project, id: $0) } ?? .project(project)]
        }
        if let item { return [.item(item)] }
        if let project { return [.project(project)] }
        return []
    }
}

enum PushEnvironment {
    /// "development" for Xcode-installed builds, "production" for TestFlight/App Store,
    /// read from the provisioning profile the app was signed with.
    static var current: String {
        // iOS: embedded.mobileprovision; macOS: Contents/embedded.provisionprofile.
        let path = Bundle.main.path(forResource: "embedded", ofType: "mobileprovision")
            ?? Bundle.main.bundleURL.appending(path: "Contents/embedded.provisionprofile").path(percentEncoded: false)
        guard let data = FileManager.default.contents(atPath: path),
              let text = String(data: data, encoding: .isoLatin1),
              let range = text.range(of: "aps-environment</key>")
        else { return "production" }
        let tail = text[range.upperBound...].prefix(80) // macOS keys it com.apple.developer.aps-environment
        return tail.contains("development") ? "development" : "production"
    }
}
