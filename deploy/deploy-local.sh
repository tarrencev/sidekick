#!/usr/bin/env bash
# Build sidekick from this checkout and deploy it on this machine (run on dl).
#   deploy/deploy-local.sh
#   SIDEKICK_NOTIFY_URL=https://ntfy.sh/<secret-topic> deploy/deploy-local.sh
set -euo pipefail
cd "$(dirname "$0")/../server"

go=(go)
command -v mise >/dev/null && go=(mise exec -- go)
"${go[@]}" vet ./...
CGO_ENABLED=0 "${go[@]}" build -trimpath -o ~/.local/bin/sidekick.new .
mv ~/.local/bin/sidekick.new ~/.local/bin/sidekick

if [ -n "${SIDEKICK_NOTIFY_URL:-}" ]; then
  mkdir -p ~/.config/sidekick && touch ~/.config/sidekick/env
  sed -i '/^SIDEKICK_NOTIFY_URL=/d' ~/.config/sidekick/env
  echo "SIDEKICK_NOTIFY_URL=$SIDEKICK_NOTIFY_URL" >> ~/.config/sidekick/env
fi

~/.local/bin/sidekick doctor --fix >/dev/null || true
systemctl --user restart sidekick
sleep 1
~/.local/bin/sidekick doctor
