---
name: sidekick-dev
description: Maintain, debug, extend and deploy Sidekick — the daemon on this machine that relays agent questions, artifact reviews and status summaries from herdr-projects to the user's iPhone app. Use when asked to change how Sidekick works, when the app shows wrong or missing data, when questions/reviews/summaries aren't reaching the user, or to onboard agents onto Sidekick. Not for simply using Sidekick (that's the `sidekick` skill).
---

# Sidekick development and operations

Sidekick gives the user exactly two ways to be reached by agents, plus a summary per
project and per thread:

- **Questions:** Claude Code's `AskUserQuestion` is intercepted by a PreToolUse hook
  (`sidekick hook claude-ask`) and answered from the app; Codex uses `sidekick ask`.
- **Reviews:** `sidekick review <path>` snapshots a file or static site, the user
  approves or requests changes, and the verdict is typed back into the agent's pane.
- **Summaries:** `sidekick status "<headline>" -summary "…" -link <url>` from a
  coordinator pane sets the project's; from a thread pane, that thread's.
- **Messages (user → agent → user):** the app's + composer posts `/api/messages`; the
  text is typed into the coordinator's or thread's pane tagged `[sidekick msg:<id>]`.
  A Claude Code Stop hook (`sidekick hook claude-stop`) finds that tag in the
  transcript (as a user prompt, or a `queued_command` attachment if the agent was busy)
  and posts the turn's closing text as the reply. Codex replies with `sidekick reply`.

- **Plan and proposals (coordinator):** `sidekick plan` (JSON: focus, prioritized work
  with stages, merge order, next) is shown on the project page; `sidekick propose`
  files a new thread for approval in the inbox, and the decision goes back like a review
  verdict.
- **Provenance:** everything Sidekick types into a pane is tagged `[sidekick msg:<id>]`
  or `[sidekick verdict:<id>]`. Agents see it as pasted text and may distrust it, so the
  UserPromptSubmit hook (`sidekick hook claude-prompt`) checks the id with the daemon
  (`/v1/verify`) and adds a "Sidekick verified this prompt" note. Agents can also run
  `sidekick verify '<tag>'`. Forged ids get nothing.
- **PR status:** the daemon looks up GitHub PR links with `gh pr view` (cached, refreshed
  in the background) and the app tints PR pills by state and CI.

Keep it to those primitives. New features should make them better, not add channels.

## Where everything is

| What | Where |
|---|---|
| Source (Go daemon + CLI, iOS/Mac app, deploy scripts) | `~/code/me/sidekick`, a git checkout of `git@github.com:tarrencev/sidekick.git` (`server/`, `ios/`, `deploy/`) |
| Binary | `~/.local/bin/sidekick` (daemon and CLI are the same binary) |
| Service | `systemctl --user {status,restart} sidekick`; logs: `journalctl --user -u sidekick` |
| Config | `~/.config/sidekick/env` (`SIDEKICK_ARTIFACT_URL`, `SIDEKICK_NOTIFY_URL`, `SIDEKICK_APNS_KEY`/`_KEY_ID`/`_TEAM_ID`) |
| Push | Native APNs: key `~/.config/sidekick/apns.p8` (mode 600, never print it), registered phones in `~/.sidekick/devices.json`. Pushes go out for new questions, reviews, proposals and replies; Apple's errors appear in the journal as `apns:` lines, and dead tokens are dropped. |
| State | `~/.sidekick/items/*.json` (questions, reviews), `status/<project>.json`, `status/<project>~<tid>.json`, `artifacts/<project>/<id>/` |
| Agent API | unix socket `~/.sidekick/agent.sock` (`/v1/ask`, `/v1/items/{id}/wait`, `/v1/review`, `/v1/status`, `/v1/whoami`, `/v1/messages/pending`, `/v1/messages/{id}/reply`) |
| App API | `127.0.0.1:7600` → `https://<tailnet-host>:7443` (`/api/projects`, `/api/projects/{slug}`, `/api/projects/{slug}/threads/{tid}`, `/api/inbox`, `/api/events` SSE, `/api/messages` GET/POST, answer/review POSTs) |
| Artifacts | `127.0.0.1:7601` → `https://<tailnet-host>:7444`; `/<project>/<id>/` snapshots, `/lib/<project>/<tid>/…` thread library files. Separate origin on purpose: artifact pages must not be able to call the app API. |
| Skills | `~/.agents/skills/{sidekick,sidekick-dev}`, linked into `~/.claude/skills` and `~/.codex/skills` |
| Project instructions | the `## Sidekick` block in each `~/.herdr-projects/<slug>/PROJECT.md` (between `sidekick:start/end` markers). Never edit a project's `AGENTS.md`; herdr-projects regenerates it. |

Sidekick reads herdr-projects state read-only (`~/.herdr-projects/<slug>/PROJECT.md`,
`.state/coordinator.json`, `threads/<tid>.toml`, `threads/<tid>.md`, `library/<tid>/`).
It maps an agent to its project and thread by `HERDR_SESSION` + `HERDR_PANE_ID`, which
every Herdr pane (and every hook) inherits.

## First move for any problem

```bash
sidekick doctor          # service, tailscale serve, daemon, skills, Claude hook, project instructions
sidekick doctor --fix    # repairs all of it from the copies embedded in the binary
journalctl --user -u sidekick -n 50 --no-pager
```

The daemon re-applies the skills and the PROJECT.md block every 5 minutes, so new
projects get onboarded on their own.

## Changing Sidekick

1. Edit in `~/code/me/sidekick`. The skills and the PROJECT.md block live in
   `server/assets/` and are embedded in the binary; edit them there, never in
   `~/.agents/skills` (doctor overwrites those).
2. Test: `cd server && mise exec -- go vet ./... && mise exec -- go test ./...`.
   For an end-to-end check, run a second daemon on a fake root, which never touches the
   real install: `sidekick serve -projects-root /tmp/hp -data /tmp/skd -listen 127.0.0.1:17600 -artifacts-listen 127.0.0.1:17601 -artifact-url http://127.0.0.1:17601 -socket /tmp/sk.sock -herdr-projects /bin/true`,
   with `SIDEKICK_SOCKET=/tmp/sk.sock` for CLI calls.
3. Commit on a branch and push it (`git push -u origin <branch>`); open a PR for the
   user unless they asked you to land it on main. Deploy on this machine with
   `~/code/me/sidekick/deploy/deploy-local.sh`. It builds,
   swaps the binary, runs `doctor --fix` and restarts the service. Open questions survive
   a restart: their askers reconnect within the 20s grace period.
4. Verify against live data: `curl -s localhost:7600/api/projects | jq` and
   `HERDR_SESSION=… HERDR_PANE_ID=… sidekick whoami`.

Keep the API backwards compatible: the iOS app on the user's phone only updates when
the user's Mac rebuilds it. Add fields; don't rename or remove them.

## Testing end to end

The `trial` herdr-projects project is the scratch test bed; its coordinator is a real
Claude agent. Publish test artifacts as its coordinator from any shell:
`HERDR_SESSION=me HERDR_PANE_ID=<trial pane from .state/coordinator.json> sidekick review <path> -title "[test] …"`.
Message it through the API (`POST /api/messages {"project":"trial","text":"…"}`) to
exercise replies, questions, plans and proposals. The iOS UI tests
(`ios/SidekickUITests`) drive the same flows in the simulator from the user's Mac.

## The iOS app

SwiftUI, iOS 18, in `ios/`, generated with XcodeGen. It can only be built and installed
from the user's Mac with Xcode (`ios/install-device.sh <TEAM_ID>`); this machine can't
build it. If a change needs the app, make the server side backwards compatible, commit
the Swift changes, and tell the user it needs a rebuild from the Mac.

## Getting agents to use Sidekick

- New instructions belong in `server/assets/skills/sidekick/SKILL.md` (how) and
  `server/assets/project-instructions.md` (the short rule every thread brief carries).
  Deploying spreads them.
- Coordinators that are already running don't re-read PROJECT.md. Tell them directly:
  `printf '%s' "[sidekick] …" | HERDR_BIN_PATH=~/.local/bin/herdr herdr-projects coordinator prompt <slug> --text-file -`.
  Prefix it with `[sidekick]`, say what changed and what to do now, and ask them to pass
  it on to their threads. The command refuses if the pane holds an unsent draft; don't
  force it.
- Check usage: `ls -t ~/.sidekick/items | head` and `cat ~/.sidekick/status/*.json`.

## Repairs

- An inbox item closed by mistake (e.g. `cancelled`) can be put back:
  `curl -s --unix-socket ~/.sidekick/agent.sock -XPOST http://sidekick/v1/items/<id>/reopen`
  (re-sends the push). Questions come back async.
- An agent stuck in an old blocking question: `POST /v1/items/<id>/release` moves the
  question to an async one and tells the agent to carry on.

## Gotchas

- Async questions (the default) have no waiting agent. Never cancel them for a lost
  connection or a restart; only blocking `sidekick ask -wait` questions use leases.

- `/usr/bin/herdr` is stale; always use `~/.local/bin/herdr` (`HERDR_BIN_PATH`).
- A question lives only as long as its asker waits; an interrupted agent withdraws it.
  A hook that times out (23h) falls back to the terminal prompt.
- Review verdicts are delivered with `herdr-projects coordinator|thread prompt`, retried
  with backoff, and marked `delivered` in the item JSON.
- `~/.claude/settings.json` and every PROJECT.md have `.pre-sidekick` backups. Remove the
  hook by deleting the `sidekick hook claude-ask` PreToolUse entry.
