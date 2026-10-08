import AVFoundation
import FluidAudio
import Observation
import SwiftUI

/// On-device dictation with the same engine as the Mac: NVIDIA Parakeet TDT 0.6B v2 via
/// FluidAudio 0.17.2 on the Neural Engine. Audio never leaves the phone. The model
/// (~450 MB) downloads on first use and is cached after that.
@MainActor
@Observable
final class Dictation {
    enum Phase: Equatable {
        case idle
        case preparing        // downloading or loading the model
        case recording
        case transcribing
        case failed(String)
    }

    private(set) var phase: Phase = .idle
    /// Which input is dictating, so only its mic button reacts.
    private(set) var owner: UUID?
    /// Microphone level 0...1 while recording, for the button's pulse.
    private(set) var level: Float = 0

    private let engine = ParakeetEngine()
    private let audio = AVAudioEngine()
    private var samples: [Float] = []
    private var inputRate: Double = 48_000
    private var deliver: ((String) -> Void)?

    /// Same fixes as ~/local-dictation/config.json on the Mac.
    private let replacements: [String: String] = [
        "PS RESTful": "PSRestful",
        "PSR-SVL": "PSRestful",
        "Mac OS": "macOS",
        "Whisper CPP": "whisper.cpp",
    ]

    func isActive(_ id: UUID) -> Bool { owner == id && phase != .idle }

    private var stopRequested = false

    /// Starts dictating for `id`, or stops and transcribes if it's already recording.
    func toggle(_ id: UUID, deliver: @escaping (String) -> Void) {
        if owner == id, phase == .recording || phase == .preparing {
            end(id)
        } else {
            begin(id, deliver: deliver)
        }
    }

    /// Push-to-talk: start recording for `id`; `end` stops it and delivers the text.
    func begin(_ id: UUID, deliver: @escaping (String) -> Void) {
        guard phase == .idle || isFailed else { return }
        owner = id
        stopRequested = false
        self.deliver = deliver
        Task { await start() }
    }

    func end(_ id: UUID) {
        guard owner == id else { return }
        if phase == .recording {
            stop()
        } else {
            stopRequested = true // released while the mic was still opening
        }
    }

    private var isFailed: Bool { if case .failed = phase { true } else { false } }

    private func start() async {
        guard await AVAudioApplication.requestRecordPermission() else {
            phase = .failed("Microphone access is off. Turn it on in Settings → Sidekick.")
            finish(nil)
            return
        }
        // Open the mic right away; the model loads in parallel and is only needed
        // once the user lets go.
        loadTask = Task { try await engine.load() }
        #if DEBUG
        // `-debugDictationAudio <file>` stands in for the mic (the simulator's mic
        // needs macOS permissions UI tests can't grant).
        if let path = UserDefaults.standard.string(forKey: "debugDictationAudio"),
           let clip = try? AudioConverter().resampleAudioFile(path: path) {
            samples = clip
            inputRate = 16_000
            phase = .recording
            if stopRequested { stop() }
            return
        }
        #endif
        do {
            #if os(iOS)
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.record, mode: .measurement)
            try session.setActive(true, options: .notifyOthersOnDeactivation)
            #endif

            let input = audio.inputNode
            let format = input.outputFormat(forBus: 0)
            inputRate = format.sampleRate
            samples = []
            input.removeTap(onBus: 0)
            input.installTap(onBus: 0, bufferSize: 4096, format: format, block: Self.tap { [weak self] chunk, level in
                guard self?.phase == .recording else { return }
                self?.samples.append(contentsOf: chunk)
                self?.level = level
            })
            audio.prepare()
            try audio.start()
            phase = .recording
            if stopRequested { stop() } // released while the mic was opening
        } catch {
            phase = .failed("Couldn't start the microphone: \(error.localizedDescription)")
            finish(nil)
        }
    }

    private var loadTask: Task<Void, Error>?

    /// Hands the result (nil when nothing usable was heard) to whoever asked, once.
    private func finish(_ text: String?) {
        if isFailed {
            // Let the note be read, then clear it.
            Task {
                try? await Task.sleep(for: .seconds(4))
                if self.isFailed, self.owner == nil { self.phase = .idle }
            }
        }
        let deliver = self.deliver
        owner = nil
        self.deliver = nil
        deliver?(text ?? "")
    }

    /// Builds the tap outside the main actor: AVAudioEngine calls it on its own
    /// audio thread, where a main-actor-isolated closure would trap.
    nonisolated private static func tap(
        _ receive: @escaping @MainActor ([Float], Float) -> Void
    ) -> AVAudioNodeTapBlock {
        { buffer, _ in
            guard let channel = buffer.floatChannelData?[0] else { return }
            let chunk = Array(UnsafeBufferPointer(start: channel, count: Int(buffer.frameLength)))
            let rms = sqrt(chunk.reduce(0) { $0 + $1 * $1 } / Float(max(chunk.count, 1)))
            Task { @MainActor in receive(chunk, min(1, rms * 12)) }
        }
    }

    private func stop() {
        if audio.isRunning {
            audio.inputNode.removeTap(onBus: 0)
            audio.stop()
            #if os(iOS)
            try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
            #endif
        }
        level = 0
        phase = .transcribing
        let recorded = samples, rate = inputRate
        samples = []
        Task {
            do {
                if await !engine.isReady {
                    phase = .preparing
                    try await loadTask?.value
                    try await engine.load()
                    phase = .transcribing
                }
                guard recorded.count > Int(rate * 0.3) else {
                    phase = .failed("Didn't catch that. Hold, speak, then let go.")
                    finish(nil)
                    return
                }
                var text = try await engine.transcribe(recorded, sampleRate: rate)
                for (from, to) in replacements { text = text.replacingOccurrences(of: from, with: to) }
                phase = text.isEmpty ? .failed("Didn't catch that. Try again a little louder.") : .idle
                finish(text)
            } catch {
                phase = .failed("Voice model error: \(error.localizedDescription)")
                finish(nil)
            }
        }
    }

    /// Loads the model ahead of time if it's already on the phone, so the first
    /// dictation starts instantly. Never triggers the large first download.
    func warmUpIfCached() {
        Task.detached(priority: .utility) { [engine] in
            if ParakeetEngine.modelsCached() { try? await engine.load() }
        }
    }
}

/// Mirrors the Engine actor in ~/local-dictation/parakeet/Sources/parakeet-server/main.swift.
actor ParakeetEngine {
    private var asr: AsrManager?
    private var loading: Task<AsrManager, Error>?

    var isReady: Bool { asr != nil }

    static func modelsCached() -> Bool {
        let dir = AsrModels.defaultCacheDirectory(for: .v2)
        return FileManager.default.fileExists(atPath: dir.appendingPathComponent("Encoder.mlmodelc").path)
    }

    func load() async throws {
        if asr != nil { return }
        if let loading { asr = try await loading.value; return }
        let task = Task { () throws -> AsrManager in
            let models = try await AsrModels.downloadAndLoad(version: .v2)
            let manager = AsrManager(config: .default)
            try await manager.loadModels(models)
            // One throwaway decode so the first real utterance is not slow.
            var state = TdtDecoderState.make(decoderLayers: await manager.decoderLayerCount)
            _ = try? await manager.transcribe([Float](repeating: 0, count: 16_000), decoderState: &state)
            return manager
        }
        loading = task
        do {
            asr = try await task.value
        } catch {
            loading = nil
            throw error
        }
    }

    #if DEBUG
    static func debugTranscribe(_ path: String) async {
        let engine = ParakeetEngine()
        let started = Date()
        do {
            try await engine.load()
            let loaded = Date()
            let samples = try AudioConverter().resampleAudioFile(path: path)
            let text = try await engine.transcribe(samples, sampleRate: 16_000)
            print("SIDEKICK_TRANSCRIBE load=\(loaded.timeIntervalSince(started))s decode=\(Date().timeIntervalSince(loaded))s text=\(text)")
        } catch {
            print("SIDEKICK_TRANSCRIBE error=\(error)")
        }
    }
    #endif

    func transcribe(_ samples: [Float], sampleRate: Double) async throws -> String {
        guard let asr else { throw NSError(domain: "parakeet", code: 503) }
        var audio = sampleRate == 16_000 ? samples : try AudioConverter().resample(samples, from: sampleRate)
        // Parakeet needs about a second of audio; pad short clips with silence.
        if audio.count < 16_000 { audio += [Float](repeating: 0, count: 16_000 - audio.count) }
        var state = TdtDecoderState.make(decoderLayers: await asr.decoderLayerCount)
        let result = try await asr.transcribe(audio, decoderState: &state)
        return result.text.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

/// The mic button placed in every text input. Tap to talk, tap again to insert the text.
struct MicButton: View {
    @Environment(Dictation.self) private var dictation
    @Binding var text: String
    @State private var id = UUID()

    var body: some View {
        let active = dictation.isActive(id)
        Button {
            dictation.toggle(id) { spoken in
                guard !spoken.isEmpty else { return }
                let base = text.trimmingCharacters(in: .whitespaces)
                text = base.isEmpty ? spoken : base + " " + spoken
            }
        } label: {
            ZStack {
                if active, dictation.phase == .recording {
                    Circle()
                        .fill(Theme.accent.opacity(0.25))
                        .scaleEffect(1 + CGFloat(dictation.level) * 0.6)
                        .animation(.easeOut(duration: 0.12), value: dictation.level)
                }
                if active, dictation.phase == .preparing || dictation.phase == .transcribing {
                    ProgressView().controlSize(.small).tint(Theme.accent)
                } else {
                    Image(systemName: active ? "stop.fill" : "mic")
                        .font(.ui(size: active ? 13 : 16, weight: .medium))
                        .foregroundStyle(active ? Theme.accent : Theme.secondary)
                }
            }
            .frame(width: 32, height: 32)
            .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(active ? "Stop dictation" : "Dictate")
        .sensoryFeedback(.impact(weight: .light), trigger: active)
    }
}

/// One line under an input when dictation needs to say something.
struct DictationNote: View {
    @Environment(Dictation.self) private var dictation

    var body: some View {
        switch dictation.phase {
        case .preparing:
            note(Self.firstRun ? "Downloading the voice model (about 450 MB, one time)…" : "Loading the voice model…")
        case .transcribing:
            note("Transcribing…")
        case .failed(let message):
            note(message, color: Theme.accent)
        default:
            EmptyView()
        }
    }

    private static var firstRun: Bool { !ParakeetEngine.modelsCached() }

    private func note(_ text: String, color: Color = Theme.tertiary) -> some View {
        Text(text).font(.ui(size: 12)).foregroundStyle(color)
    }
}
