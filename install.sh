#!/usr/bin/env bash
# Build megafactory.app signed with the local "Telepath Local Signing" cert and
# install it into /Applications. The identity and bundle ID stay the same across
# builds, so macOS keeps the permissions already granted.
set -euo pipefail

cd "$(dirname "$0")"

go tool mygo build --skip-dmg

app=$(find build -name 'megafactory.app' -type d -print -quit)
[ -n "$app" ] || { echo "megafactory.app not found under build/" >&2; exit 1; }

codesign --verify --deep --strict "$app"

osascript -e 'tell application "megafactory" to quit' 2>/dev/null || true
rm -rf /Applications/megafactory.app
ditto "$app" /Applications/megafactory.app
echo "Installed /Applications/megafactory.app"
