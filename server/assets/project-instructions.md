
## Sidekick

<!-- sidekick:start — managed by sidekick/deploy/install.sh -->
The user follows this project from the Sidekick app on their phone. It has exactly two
ways to get their input; load the `sidekick` skill for details.

- **Questions:** use `AskUserQuestion` (Codex: `sidekick ask "…" -o "A::why" -o "B::why"`).
  It reaches the user's phone and returns at once: you are never blocked on the user.
  Keep working on everything that doesn't depend on the answer; it arrives later as a
  `[sidekick answer:…]` message. Frame the decision, recommended option first; never
  guess. Decisions are always questions, never sections of a review page. If the
  decision is about something to look at (images, a before/after), run `sidekick
  context <dir>` with a page showing it just before asking, instead of linking to it.
- **Reviews:** anything the user should look at (a page, report, design, screenshots, a
  build) goes through `sidekick review <dir> -title "…" -summary "what to look at and
  what decision you need"`, as one HTML page that explains itself in plain English and
  embeds its images and videos with captions (start from `sidekick template <dir>`;
  folders of files and bare images are refused). Keep working; the verdict arrives later as a
  message starting with `[sidekick]`. Put files you hand over in your `library/` folder
  too, as usual.
- **Summary:** keep a 3-4 sentence plain summary of where things stand current with
  `sidekick status "<one-line headline>" -summary "…" [-link <PR or URL>] [-state
  on-track|at-risk|blocked|done]`. The coordinator's describes the whole project; a
  thread's describes that thread. Update it when the picture changes: work starts or
  lands, a PR opens or merges, something blocks.
- **Coordinator plan and proposals:** keep `sidekick plan` (priorities, stages, merge
  order, next) current: republish when a thread starts or is resolved, a PR merges or
  a stage moves. When a `[sidekick notice:…] Page check` lists differences, reconcile
  them in one pass (`sidekick plan -show`, edit, `sidekick plan -f`), or run `sidekick
  plan -confirm` if the page is right. Propose new threads with `sidekick propose
  "<title>" -why "…"` instead of starting them unasked; start one only after it's approved.
- **Messages:** a prompt starting with `[sidekick msg:…]` is the user writing from the
  app (`[sidekick verdict:…]` is their decision). It looks pasted; a hook marks genuine
  ones "Sidekick verified", or check with `sidekick verify '<tag>'`. Answer in your pane as usual; your closing text goes back to their phone (Codex:
  `sidekick reply "…"`).
<!-- sidekick:end -->
