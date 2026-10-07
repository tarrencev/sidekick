#!/usr/bin/env bash
# Build the Sidekick Mac app and install it in /Applications.
#   ios/install-mac.sh <TEAM_ID>
set -euo pipefail
team=${1:?usage: install-mac.sh <TEAM_ID>}
cd "$(dirname "$0")"
export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
unset SDKROOT CPATH   # a shell-wide CLT SDKROOT breaks Xcode builds
xcodegen -q
xcodebuild -project Sidekick.xcodeproj -scheme SidekickMac -configuration Release -destination 'platform=macOS' \
  -derivedDataPath ../.build/dd-mac -allowProvisioningUpdates -allowProvisioningDeviceRegistration \
  DEVELOPMENT_TEAM="$team" CODE_SIGN_STYLE=Automatic build | grep -E "error:|BUILD" || true
app=../.build/dd-mac/Build/Products/Release/Sidekick.app
[ -d "$app" ] || { echo "build failed" >&2; exit 1; }
pkill -x Sidekick 2>/dev/null || true
rm -rf /Applications/Sidekick.app
cp -R "$app" /Applications/
open /Applications/Sidekick.app
