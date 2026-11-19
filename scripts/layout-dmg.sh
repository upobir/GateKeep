#!/bin/bash
set -euo pipefail

dmg=$1
attach=$(hdiutil attach -readwrite -noverify -noautoopen "$dmg")
mount=$(printf '%s\n' "$attach" | awk '/\/Volumes\// { print substr($0, index($0, "/Volumes/")); exit }')
trap 'hdiutil detach "$mount" >/dev/null' EXIT

osascript <<'EOF'
tell application "Finder"
	tell disk "GateKeep"
		open
		set current view of container window to icon view
		set toolbar visible of container window to false
		set statusbar visible of container window to false
		set bounds of container window to {200, 120, 760, 460}
		set theViewOptions to the icon view options of container window
		set arrangement of theViewOptions to not arranged
		set icon size of theViewOptions to 96
		delay 1
		set position of item "GateKeep.app" of container window to {140, 160}
		set position of item "Applications" of container window to {420, 160}
		update without registering applications
		delay 1
		close
	end tell
end tell
EOF
sync
