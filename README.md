# Sidekick

A phone app for the [herdr-projects](https://github.com/eliasstravik/herdr-projects)
projects running on `dl`. Agents reach you in exactly two ways, and each project has
one status line:

- **Questions**: Claude Code's `AskUserQuestion` (or `sidekick ask`) is relayed to the
  app. Your answer goes back as the tool result.
- **Reviews**: `sidekick review <path>` snapshots a file or static site and serves it
  on the tailnet. Your approval or requested changes are sent back to the agent's pane.
- **Status**: the coordinator keeps a headline (`sidekick status`), shown next to the
  project's threads, which come from herdr-projects.

```
 Claude/Codex pane ──hook / CLI──▶ sidekickd (dl) ◀──https (tailnet)── iOS app
   (herdr-projects)  unix socket   ~/.sidekick        :7443 API, :7444 artifacts
         ▲                              │
         └── herdr-projects prompt ◀────┘  review verdicts
```

## Layout

- `server/`: `sidekick`, a single Go binary that is the daemon (`serve`), the agent CLI
  (`ask`, `review`, `status`, `whoami`, `hook claude-ask`) and the installer (`doctor`).
- `server/assets/`: the agent skills (`sidekick` to use it, `sidekick-dev` to maintain
  it), the `PROJECT.md` instruction block and the systemd unit. These are embedded in
  the binary, so `sidekick doctor --fix` can restore everything from the binary alone.
- `ios/`: the SwiftUI app (iOS 18). The project is generated with XcodeGen.
- `deploy/`: `install.sh` (from the Mac) and `deploy-local.sh` (on dl).

## How it maps onto herdr-projects

- **Projects:** `~/.herdr-projects/<slug>` (read-only). Archived projects are hidden.
- **Attribution:** the CLI sends `HERDR_SESSION` and `HERDR_PANE_ID`. The daemon matches
  them to the coordinator (`.state/coordinator.json`) or a thread (`threads/*.toml`).
- **Summaries:** `sidekick status` from the coordinator sets the project's summary; from
  a thread, that thread's. Threads without one show the opening of their report.
- **Questions** live only as long as the asking agent waits. If you interrupt the agent,
  or the hook times out, the question is withdrawn and Claude falls back to its
  terminal prompt.
- **Verdicts and messages from the app** go back with `herdr-projects coordinator|thread
  prompt`, so its draft protection applies.
- **Onboarding:** the daemon keeps the skills installed for Claude and Codex and keeps a
  `## Sidekick` block in every project's `PROJECT.md`, so new projects and threads learn
  the rules on their own.

## Deploy

Push to `main`, then from the Mac (dl pulls from GitHub, then builds and deploys):

```bash
deploy/install.sh
SIDEKICK_NOTIFY_URL=https://ntfy.sh/<secret-topic> deploy/install.sh   # optional push alerts
```

On dl, agents with the `sidekick-dev` skill can work in `~/code/me/sidekick` and run
`deploy/deploy-local.sh`. Check the install with `sidekick doctor`, and repair it with
`sidekick doctor --fix`.

## Push notifications

dl sends native push through APNs for new questions, reviews and proposals, and for
replies to your messages; tapping one opens the item. It needs an APNs auth key from the
Apple developer account, configured in `dl:~/.config/sidekick/env`:

```
SIDEKICK_APNS_KEY=/home/<you>/.config/sidekick/apns.p8   # chmod 600
SIDEKICK_APNS_KEY_ID=<10-char key id>
SIDEKICK_APNS_TEAM_ID=<team id>
```

The app registers its device token with dl on launch (`POST /api/devices`).

## Voice input

Every text box in the app has a mic button. Speech is transcribed on the phone with the
same engine as the Mac's local dictation: NVIDIA Parakeet TDT 0.6B v2 through
[FluidAudio](https://github.com/FluidInference/FluidAudio) 0.17.2 on the Neural Engine
(`ios/Sidekick/Dictation.swift`), with the same word replacements. Audio never leaves
the phone. The model (~450 MB) downloads the first time you tap the mic.

## Mac app

The same SwiftUI code builds a native Mac app (`SidekickMac` target, bundle id
`gg.cartridge.sidekick.mac`): Inbox and projects in a sidebar, the floating composer with
hold-to-talk, a menu bar item with the pending count and what's waiting, a Dock badge,
and native push. Closing the window keeps it in the menu bar.

```bash
ios/install-mac.sh <TEAM_ID>    # builds, signs and installs /Applications/Sidekick.app
```

## Build the app

The app needs Xcode, so it builds only on the Mac:

```bash
ios/install-device.sh <TEAM_ID>    # builds, signs, installs and launches on a connected iPhone
```

The script clears the `SDKROOT`, `CPATH` and `DEVELOPER_DIR` values that this shell
points at the command-line tools; otherwise iOS builds fail.
