import SwiftUI

struct ProjectsView: View {
    @Environment(AppModel.self) private var model
    @State private var showSettings = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .center) {
                    Text("Sidekick")
                        .font(Theme.serif(34))
                        .foregroundStyle(Theme.text)
                        .onLongPressGesture { showSettings = true }
                    Spacer()
                }
                .padding(.bottom, 20)

                if model.projects.isEmpty {
                    EmptyProjects(error: model.lastError) { showSettings = true }
                }
                ForEach(model.projects) { project in
                    Hairline()
                    NavigationLink(value: Route.project(project.slug)) {
                        ProjectRow(project: project)
                    }
                    .buttonStyle(.plain)
                }
                if !model.projects.isEmpty { Hairline() }
            }
            .padding(.horizontal, 20)
            .padding(.top, 8)
        }
        .readingRoom()
        .hidesNavigationBar()
        .refreshable { await model.refresh() }
        .sheet(isPresented: $showSettings) { SettingsView() }
        .composer(nil)
    }
}

private struct ProjectRow: View {
    let project: ProjectSummary

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 6) {
                Text(project.name)
                    .font(Theme.serif(20))
                    .foregroundStyle(Theme.text)
                HStack(alignment: .firstTextBaseline) {
                    Text(project.status?.headline ?? project.goal ?? "No status yet.")
                        .font(.system(size: 14))
                        .foregroundStyle(Theme.secondary)
                        .lineLimit(2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            Image(systemName: "chevron.right")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(Theme.tertiary)
        }
        .padding(.vertical, 18)
        .contentShape(Rectangle())
    }
}

private struct EmptyProjects: View {
    let error: String?
    let openSettings: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(error == nil ? "No projects yet." : "Can't reach dl.")
                .font(Theme.serif(20))
                .foregroundStyle(Theme.text)
            Text(error ?? "Projects created with herdr-projects show up here.")
                .font(.system(size: 14))
                .foregroundStyle(Theme.secondary)
            Button("Server settings", action: openSettings)
                .font(.system(size: 14))
                .foregroundStyle(Theme.accent)
                .padding(.top, 4)
        }
        .padding(.top, 40)
    }
}
