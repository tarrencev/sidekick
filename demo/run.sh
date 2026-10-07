#!/usr/bin/env bash
# A self-contained Sidekick demo: a fictional herdr-projects setup, served by a local
# daemon on 127.0.0.1:17600 (app API) and :17601 (artifacts). Used for screenshots;
# point the app's server setting at http://127.0.0.1:17600.
#   demo/run.sh [dir]        (default: .build/demo)   Ctrl-C stops the daemon.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$(pwd)
demo=${1:-$root/.build/demo}
rm -rf "${demo:?}"; mkdir -p "$demo"
(cd server && go build -o "$demo/sidekick" .)
cd "$demo"   # unix socket paths are short-limited: use a relative one
sk="$demo/sidekick"
export SIDEKICK_SOCKET=agent.sock HERDR_SESSION=demo

# ---- fictional herdr-projects state ----
project() { # slug name goal coordinator-pane
  mkdir -p "hp/$1/.state" "hp/$1/threads"
  printf '+++\nname = "%s"\ngoal = "%s"\n+++\n' "$2" "$3" > "hp/$1/PROJECT.md"
  printf '{"session":"demo","pane_id":"%s","cwd":"%s"}' "$4" "$demo/hp/$1" > "hp/$1/.state/coordinator.json"
}
thread() { # slug id title group line pane pr
  printf 'id = "%s"\ntitle = "%s"\nstatus = "open"\nlast_group = "%s"\nstate_line = "%s"\npane_id = "%s"\npr = "%s"\n' "$2" "$3" "$4" "$5" "$6" "$7" > "hp/$1/threads/$2.toml"
}
project acme "Acme Storefront" "Ship the spring storefront: faster checkout, cleaner product pages." w1:p1
thread acme t-0012 "One-page checkout" ready-for-review "review · PR #128" w2:p1 https://github.com/sidekick-demo-co/storefront/pull/128
thread acme t-0014 "Product image CDN" working "~60%" w3:p1 ""
thread acme t-0015 "Search suggestions" waiting-on-you "needs you" w4:p1 ""
thread acme t-0016 "Gift cards" idle "" w5:p1 ""
project field "Field App" "Offline-first inspections app for the field team." w9:p1
thread field t-0003 "Offline sync" working "~35%" w9:p2 https://github.com/sidekick-demo-co/field/pull/41

printf '#!/bin/sh\ncat >> "%s/delivered.log"\n' "$demo" > fakehp; chmod +x fakehp
"$sk" serve -data data -projects-root hp -listen 127.0.0.1:17600 -artifacts-listen 127.0.0.1:17601 \
  -artifact-url http://127.0.0.1:17601 -socket agent.sock -herdr-projects "$demo/fakehp" > serve.log 2>&1 &
daemon=$!
trap 'kill $daemon 2>/dev/null' EXIT
sleep 1
pane() { HERDR_PANE_ID=$1; shift; HERDR_PANE_ID=$HERDR_PANE_ID "$sk" "$@"; }

# ---- what the agents publish ----
pane w1:p1 status "Checkout ships this week; image CDN next" -state on-track \
  -summary "One-page checkout is done and green in CI, waiting on your review of the new flow. The image CDN is about 60% through and already cuts product-page load time by a third. Search suggestions need a decision from you on ranking before they can ship." \
  -link https://github.com/sidekick-demo-co/storefront/pull/128
pane w2:p1 status "One-page checkout ready" -summary "Checkout is one page now: address, delivery and payment on a single screen, with Apple Pay first. Tests and CI are green; it needs your review." -link https://github.com/sidekick-demo-co/storefront/pull/128
pane w3:p1 status "CDN rollout 60%" -summary "Product images now come from the CDN in three sizes. Load time on product pages dropped from 2.1s to 1.4s on phones; the last step is swapping the collection pages."
pane w9:p1 status "Offline sync in progress" -summary "Inspections now save offline and sync when the phone reconnects. Conflict handling is next." -state at-risk
pane w1:p1 plan <<'JSON'
{
  "focus": "Ship one-page checkout before the spring sale",
  "work": [
    {"title": "One-page checkout", "stage": "review", "thread": "t-0012", "pr": "https://github.com/sidekick-demo-co/storefront/pull/128"},
    {"title": "Product image CDN", "stage": "building", "thread": "t-0014", "note": "collection pages left"},
    {"title": "Search suggestions", "stage": "blocked", "waitingOn": "user", "thread": "t-0015", "note": "ranking decision"},
    {"title": "Gift cards", "stage": "research", "thread": "t-0016"}
  ],
  "mergeOrder": [
    {"pr": "https://github.com/sidekick-demo-co/storefront/pull/127", "title": "Payment provider upgrade", "note": "checkout builds on it"},
    {"pr": "https://github.com/sidekick-demo-co/storefront/pull/128", "title": "One-page checkout"}
  ],
  "next": [{"title": "Order tracking emails", "note": "after the sale"}]
}
JSON

mkdir -p review
cat > review/index.html <<'HTML'
<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>One-page checkout</title><style>
:root{--bg:#111;--fg:#f1ede7;--mu:#9a958f;--ln:#2a2826;--card:#1c1b1a;--ac:#ff7a64;--ok:#6bcc8c}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.55 -apple-system,system-ui,sans-serif}
main{max-width:860px;margin:0 auto;padding:22px 18px 60px}h1{font:600 27px/1.2 Georgia,serif;margin:0 0 8px}
h2{font:600 19px Georgia,serif;margin:28px 0 8px}.mu{color:var(--mu);font-size:14px}
.ask{background:var(--card);border-left:3px solid var(--ac);padding:12px 14px;border-radius:8px;margin:18px 0}
.cmp{display:grid;grid-template-columns:1fr 1fr;gap:10px}.phone{background:#fff;color:#222;border-radius:16px;padding:12px;font-size:12px}
.step{border:1px solid #ddd;border-radius:8px;padding:8px;margin:6px 0}.dim{color:#999}.btn{background:#222;color:#fff;border-radius:8px;text-align:center;padding:8px;margin-top:8px}
.pay{background:#000;color:#fff;border-radius:8px;text-align:center;padding:8px;margin:6px 0}figcaption{color:var(--mu);font-size:13px;margin-top:6px}
</style></head><body><main>
<h1>One-page checkout</h1>
<p>Checkout used to take four screens. Now address, delivery and payment are on one page, with Apple Pay at the top, so people who already trust us can pay in two taps.</p>
<p class="mu">Acme Storefront · thread t-0012 · PR #128</p>
<div class="ask"><b>What I need from you:</b> approve if the new order of sections feels right on a phone; otherwise tell me what to move.</div>
<h2>Before and after</h2>
<div class="cmp">
<figure><div class="phone"><b>Step 1 of 4</b><div class="step">Shipping address</div><div class="step dim">Delivery</div><div class="step dim">Payment</div><div class="step dim">Review</div><div class="btn">Continue</div></div><figcaption><b>Before:</b> four screens; a third of people dropped off between steps 2 and 3.</figcaption></figure>
<figure><div class="phone"><b>Checkout</b><div class="pay"> Pay</div><div class="step">Address: 12 Elm St</div><div class="step">Delivery: Tomorrow</div><div class="step">Card ending 4242</div><div class="btn">Place order · $84</div></div><figcaption><b>After:</b> one page, Apple Pay first, everything editable in place.</figcaption></figure>
</div>
<h2>Things to check</h2><ul><li>Does the total stay visible while you scroll?</li><li>Gift cards aren't supported yet; they're a separate thread.</li></ul>
</main></body></html>
HTML
pane w2:p1 review review -title "One-page checkout" -summary "Address, delivery and payment on one page, Apple Pay first. Approve if the section order feels right on a phone."
pane w4:p1 ask "How should search suggestions be ranked?" -header "Search ranking" \
  -o "Best sellers first (Recommended)::Shoppers see what other people buy; fastest to ship" \
  -o "Closest text match::Most predictable, but slow sellers crowd the list" \
  -o "Personalized::Uses order history; needs a privacy review first"
pane w1:p1 propose "Order tracking emails" -why "Support gets 40 'where is my order' emails a day; a shipped/delivered email with a tracking link would cut most of them."

# A conversation: the user asked the coordinator something, and it replied.
msg=$(curl -s -XPOST http://127.0.0.1:17600/api/messages -d '{"project":"acme","text":"How close are we to shipping checkout? Anything I should worry about before the sale?"}' | sed 's/.*"id":"\([^"]*\)".*/\1/')
curl -s --unix-socket agent.sock -XPOST "http://sidekick/v1/messages/$msg/reply" -d '{"text":"Checkout is ready: CI is green on PR #128 and it is waiting on your review. Two things before the sale:\n\n- **Payment upgrade first.** PR #127 has to merge before #128; both are green.\n- **Gift cards** are not in the new checkout yet. If you want them for the sale, I can start that thread today.\n\nEverything else is on track."}' > /dev/null

echo "Demo running: app API http://127.0.0.1:17600, artifacts http://127.0.0.1:17601 (data in $demo). Ctrl-C to stop."
wait $daemon
