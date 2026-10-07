#!/usr/bin/env bash
# Sync this checkout to dl and deploy it there.
#   deploy/install.sh [ssh-host]          (default: dl)
#   SIDEKICK_NOTIFY_URL=https://ntfy.sh/<secret-topic> deploy/install.sh
# Files that are newer on the host (edited there by an agent) are kept, not overwritten.
set -euo pipefail
host=${1:-dl}
cd "$(dirname "$0")/.."

rsync -az --update --exclude .build --exclude 'ios/Sidekick.xcodeproj' --exclude '.DS_Store' \
  ./ "$host":code/me/sidekick/
ssh "$host" "SIDEKICK_NOTIFY_URL='${SIDEKICK_NOTIFY_URL:-}' ~/code/me/sidekick/deploy/deploy-local.sh"
