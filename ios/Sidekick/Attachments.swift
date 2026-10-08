import PhotosUI
import SwiftUI
import UniformTypeIdentifiers
#if os(iOS)
import UIKit
typealias PlatformImage = UIImage
#else
import AppKit
typealias PlatformImage = NSImage
#endif

struct Attachment: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let type: String
    let size: Int
    let url: URL

    var isImage: Bool { type.hasPrefix("image/") }
}

/// Files the user is about to send: picked, previewed, uploaded right away, removable.
@MainActor
@Observable
final class AttachmentTray {
    struct Pending: Identifiable {
        let id = UUID()
        let name: String
        let preview: PlatformImage?
        var uploaded: Attachment?
        var error: String?
    }

    var pending: [Pending] = []
    var project: String

    init(project: String) { self.project = project }

    var isUploading: Bool { pending.contains { $0.uploaded == nil && $0.error == nil } }
    var readyIDs: [String] { pending.compactMap(\.uploaded?.id) }
    var isEmpty: Bool { pending.isEmpty }

    func remove(_ id: UUID) { pending.removeAll { $0.id == id } }
    func clear() { pending = [] }

    /// Adds a file. Photos are sent as JPEG: agents read it everywhere; HEIC they often can't.
    func add(data: Data, name: String, type: UTType?) {
        var data = data, name = name
        let image = PlatformImage(data: data)
        if let type, type.conforms(to: .image), !type.conforms(to: .gif), type != .png, let jpeg = image?.jpeg {
            data = jpeg
            name = (name as NSString).deletingPathExtension + ".jpg"
        }
        let item = Pending(name: name, preview: image)
        pending.append(item)
        let project = self.project
        Task {
            do {
                guard let api = AppModel.shared.api else { throw APIError(message: "No server") }
                let att = try await api.upload(project: project, name: name, data: data)
                update(item.id) { $0.uploaded = att }
            } catch {
                update(item.id) { $0.error = error.localizedDescription }
            }
        }
    }

    func add(fileURL url: URL) {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        guard let data = try? Data(contentsOf: url) else { return }
        add(data: data, name: url.lastPathComponent, type: UTType(filenameExtension: url.pathExtension))
    }

    private func update(_ id: UUID, _ change: (inout Pending) -> Void) {
        if let i = pending.firstIndex(where: { $0.id == id }) { change(&pending[i]) }
    }
}

extension PlatformImage {
    var jpeg: Data? {
        #if os(iOS)
        jpegData(compressionQuality: 0.85)
        #else
        guard let tiff = tiffRepresentation, let rep = NSBitmapImageRep(data: tiff) else { return nil }
        return rep.representation(using: .jpeg, properties: [.compressionFactor: 0.85])
        #endif
    }
}

extension Image {
    init(platform image: PlatformImage) {
        #if os(iOS)
        self.init(uiImage: image)
        #else
        self.init(nsImage: image)
        #endif
    }
}

/// The paperclip: Photos or Files on iPhone, a file chooser on the Mac (plus drag and
/// drop and paste, see `acceptsAttachments`).
struct AttachButton: View {
    let tray: AttachmentTray
    @State private var showPhotos = false
    @State private var showFiles = false
    @State private var photoItems: [PhotosPickerItem] = []

    var body: some View {
        Menu {
            #if os(iOS)
            Button { showPhotos = true } label: { Label("Photos", systemImage: "photo.on.rectangle") }
            #endif
            Button { showFiles = true } label: { Label(fileLabel, systemImage: "folder") }
        } label: {
            Image(systemName: "paperclip")
                .font(.system(size: 16, weight: .medium))
                .foregroundStyle(Theme.secondary)
                .frame(width: 32, height: 32)
                .contentShape(Rectangle())
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .accessibilityLabel("Attach")
        .photosPicker(isPresented: $showPhotos, selection: $photoItems, maxSelectionCount: 10, matching: .any(of: [.images, .videos]))
        .onChange(of: photoItems) { _, items in
            let picked = items
            photoItems = []
            for (n, item) in picked.enumerated() {
                Task {
                    guard let data = try? await item.loadTransferable(type: Data.self) else { return }
                    let type = item.supportedContentTypes.first
                    let ext = type?.preferredFilenameExtension ?? "dat"
                    tray.add(data: data, name: "photo-\(n + 1).\(ext)", type: type)
                }
            }
        }
        .fileImporter(isPresented: $showFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            if case .success(let urls) = result { urls.forEach(tray.add(fileURL:)) }
        }
    }

    private var fileLabel: String {
        #if os(iOS)
        "Files"
        #else
        "Choose File…"
        #endif
    }
}

/// Thumbnails of what's about to be sent, each removable.
struct AttachmentTrayView: View {
    let tray: AttachmentTray

    var body: some View {
        if !tray.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    ForEach(tray.pending) { item in
                        ZStack(alignment: .topTrailing) {
                            Group {
                                if let preview = item.preview {
                                    Image(platform: preview).resizable().scaledToFill()
                                } else {
                                    VStack(spacing: 4) {
                                        Image(systemName: "doc").font(.system(size: 18))
                                        Text(item.name).font(.system(size: 9)).lineLimit(2).multilineTextAlignment(.center)
                                    }
                                    .foregroundStyle(Theme.secondary)
                                    .padding(6)
                                }
                            }
                            .frame(width: 64, height: 64)
                            .background(Theme.surface)
                            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                            .overlay {
                                if item.uploaded == nil && item.error == nil {
                                    ProgressView().controlSize(.small).tint(Theme.text)
                                } else if item.error != nil {
                                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(Theme.accent)
                                }
                            }
                            Button { tray.remove(item.id) } label: {
                                Image(systemName: "xmark.circle.fill")
                                    .font(.system(size: 16))
                                    .foregroundStyle(Theme.text, Theme.background)
                            }
                            .buttonStyle(.plain)
                            .offset(x: 6, y: -6)
                            .accessibilityLabel("Remove \(item.name)")
                        }
                    }
                }
                .padding(.top, 8)
                .padding(.horizontal, 2)
            }
        }
    }
}

/// Attachments shown in a sent message: image thumbnails and file chips.
struct AttachmentsView: View {
    @Environment(\.openURL) private var openURL
    let attachments: [Attachment]

    var body: some View {
        if !attachments.isEmpty {
            HStack(spacing: 8) {
                ForEach(attachments) { a in
                    Button { openURL(a.url) } label: {
                        if a.isImage {
                            AsyncImage(url: a.url) { image in
                                image.resizable().scaledToFill()
                            } placeholder: {
                                Theme.surface
                            }
                            .frame(width: 96, height: 96)
                            .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                        } else {
                            Label(a.name, systemImage: "doc")
                                .font(.system(size: 13))
                                .foregroundStyle(Theme.text)
                                .lineLimit(1)
                                .padding(.horizontal, 10)
                                .padding(.vertical, 8)
                                .background(Theme.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Attachment \(a.name)")
                }
            }
        }
    }
}

extension View {
    /// On the Mac, files and images can be dropped onto, or pasted into, the input.
    @ViewBuilder func acceptsAttachments(_ tray: AttachmentTray) -> some View {
        #if os(macOS)
        self
            .onDrop(of: [.fileURL, .image], isTargeted: nil) { providers in
                load(providers, into: tray)
                return true
            }
            .onPasteCommand(of: [.fileURL, .image]) { providers in load(providers, into: tray) }
        #else
        self
        #endif
    }
}

#if os(macOS)
@MainActor
private func load(_ providers: [NSItemProvider], into tray: AttachmentTray) {
    for p in providers {
        if p.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
            _ = p.loadObject(ofClass: URL.self) { url, _ in
                guard let url else { return }
                Task { @MainActor in tray.add(fileURL: url) }
            }
        } else if p.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
            p.loadDataRepresentation(forTypeIdentifier: UTType.png.identifier) { data, _ in
                guard let data else { return }
                Task { @MainActor in tray.add(data: data, name: "pasted-image.png", type: .png) }
            }
        }
    }
}
#endif
