# GateKeep

GateKeep gives AI coding agents access to your production databases, with a human in the loop. It runs as an MCP server. Every command an agent wants to run waits until you approve it in GateKeep's local web UI.

## Supported

**Platforms**
- macOS 12+ on Apple Silicon or Intel
- Linux and Windows are not supported yet

**Resources**
- MySQL
- PostgreSQL
- ClickHouse
- MongoDB
- Redis

## Install

1. Download the DMG for your Mac from the [Releases](../../releases) page:
   - Apple Silicon (M1 or newer): `GateKeep-<version>-macos-arm64.dmg`
   - Intel: `GateKeep-<version>-macos-amd64.dmg`
2. Open the DMG and drag **GateKeep** into **Applications**.
3. Launch it. The app is not signed by Apple, so macOS will block it the first time. Go to **System Settings → Privacy & Security** and click **Open Anyway**.

   Or, in a terminal:
   ```bash
   xattr -dr com.apple.quarantine /Applications/GateKeep.app
   ```

GateKeep lives in the menu bar. Click its icon to open the web UI at <http://127.0.0.1:8765>.

To check the installed version, open GateKeep's system tray menu or look beneath **GateKeep** in the web UI sidebar.

On startup, release builds check GitHub for a newer release in the background. When one is available, the tray menu shows **Get v…**; click it to open that release's download page. Development builds skip this check.

### Verify the download (optional)

Releases are built by GitHub Actions from this repo. To confirm a DMG came from that build:

```bash
gh attestation verify GateKeep-<version>-macos-arm64.dmg --repo <owner>/gatekeep
```

## Connect an agent

Point any MCP client at `http://127.0.0.1:8765/mcp`. For Claude Code:

```bash
claude mcp add --transport http gatekeep http://127.0.0.1:8765/mcp
```

Then add your databases under **Resources** in the web UI.

## Build from source

Requires Go, Node 22.12+, and Xcode Command Line Tools.

```bash
make install         # build and install to ~/Applications
make dmg             # DMG for this Mac
make release RELEASE_VERSION=v0.2.1  # DMGs from that exact tag; requires a clean checkout
```
