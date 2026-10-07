#!/usr/bin/env bash
# Deploy the pushed main branch to dl: it pulls from GitHub and builds there.
#   deploy/install.sh [ssh-host]          (default: dl)
#   SIDEKICK_NOTIFY_URL=https://ntfy.sh/<secret-topic> deploy/install.sh
# Push first; dl only fast-forwards, so it never overwrites work committed there.
set -euo pipefail
host=${1:-dl}
cd "$(dirname "$0")/.."

if [ -n "$(git status --porcelain)" ] || [ "$(git rev-parse HEAD)" != "$(git rev-parse @{u} 2>/dev/null)" ]; then
  echo "Commit and push first: dl deploys what's on origin/main." >&2
  exit 1
fi
ssh "$host" bash -s -- "${SIDEKICK_NOTIFY_URL:-}" <<'REMOTE'
set -euo pipefail
dir=~/code/me/sidekick
[ -d "$dir/.git" ] || git clone -q git@github.com:tarrencev/sidekick.git "$dir"
cd "$dir"
git pull -q --ff-only
SIDEKICK_NOTIFY_URL="${1:-}" deploy/deploy-local.sh
REMOTE
