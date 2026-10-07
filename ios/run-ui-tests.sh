#!/usr/bin/env bash
# Run the UI flows against the live server in the simulator and export screenshots.
#   ios/run-ui-tests.sh [TestName ...]        (default: all)   OUT=<dir> to choose where screenshots go
# Hold-to-talk needs a spoken clip: SK_DICTATION_AUDIO=<16 kHz wav> (made with `say` if unset).
set -uo pipefail
cd "$(dirname "$0")"
export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
unset SDKROOT CPATH
out=${OUT:-../.build/ui}
mkdir -p "$out"
sim=${SIM:-$(xcrun simctl list devices available -j | python3 -c 'import json,sys;d=json.load(sys.stdin)["devices"];print(next(x["udid"] for v in d.values() for x in v if x["name"].startswith("iPhone")))')}
xcrun simctl boot "$sim" 2>/dev/null
xcrun simctl privacy "$sim" grant microphone gg.cartridge.sidekick 2>/dev/null
clip=${SK_DICTATION_AUDIO:-$out/dictation.wav}
# (Re)make the clip if it's missing or truncated (a good one is ~100 KB).
if [ ! -f "$clip" ] || [ "$(wc -c < "$clip")" -lt 20000 ]; then
  say -o "$clip" --data-format=LEI16@16000 "Please summarize what the smoke test thread has done so far."
fi
xcodegen -q
only=()
for t in "$@"; do only+=("-only-testing:SidekickUITests/SidekickFlows/$t"); done
result="$out/run.xcresult"; rm -rf "$result"
TEST_RUNNER_SK_DICTATION_AUDIO="$clip" xcodebuild test -project Sidekick.xcodeproj -scheme Sidekick \
  -destination "id=$sim" -derivedDataPath ../.build/dd -resultBundlePath "$result" "${only[@]}" 2>&1 \
  | grep -E "error: -\[|Test Case .*(passed|failed)|TEST (SUCCEEDED|FAILED)"
rm -rf "$out/shots"; mkdir -p "$out/shots"
xcrun xcresulttool export attachments --path "$result" --output-path "$out/shots" >/dev/null 2>&1
python3 - "$out/shots" <<'PY'
import json, os, sys
d = sys.argv[1]
try:
    manifest = json.load(open(os.path.join(d, "manifest.json")))
except Exception:
    sys.exit()
for test in manifest:
    for a in test["attachments"]:
        name = a.get("suggestedHumanReadableName", "x").split("_0_")[0]
        if name.startswith(("UI Snapshot", "Synthesized", "Screen Recording")):
            os.remove(os.path.join(d, a["exportedFileName"]))
            continue
        os.rename(os.path.join(d, a["exportedFileName"]), os.path.join(d, name + ".png"))
PY
echo "screenshots: $out/shots"
