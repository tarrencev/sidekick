#!/usr/bin/env bash
# Build Sidekick and install it on a connected iPhone.
#   ios/install-device.sh <TEAM_ID> [device-udid]
set -euo pipefail
team=${1:?usage: install-device.sh <TEAM_ID> [device-udid]}
cd "$(dirname "$0")"
export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
unset SDKROOT CPATH   # a shell-wide CLT SDKROOT breaks iOS builds

device=${2:-$(xcrun devicectl list devices --json-output /dev/stdout 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)["result"]["devices"]
p=[x for x in d if x["hardwareProperties"].get("platform")=="iOS" and x["connectionProperties"].get("pairingState")=="paired"]
print(p[0]["hardwareProperties"]["udid"] if p else "")')}
[ -n "$device" ] || { echo "No paired iPhone found. Connect it, tap Trust, and enable Developer Mode." >&2; exit 1; }

xcodegen -q
xcodebuild -project Sidekick.xcodeproj -scheme Sidekick -configuration Release \
  -destination "id=$device" -derivedDataPath ../.build/dd-device \
  -allowProvisioningUpdates DEVELOPMENT_TEAM="$team" CODE_SIGN_STYLE=Automatic build | grep -E "error:|BUILD" || true
app=../.build/dd-device/Build/Products/Release-iphoneos/Sidekick.app
[ -d "$app" ] || { echo "build failed" >&2; exit 1; }
xcrun devicectl device install app --device "$device" "$app"
xcrun devicectl device process launch --device "$device" gg.cartridge.sidekick || true
