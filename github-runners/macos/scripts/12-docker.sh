#!/usr/bin/env bash
set -euo pipefail

# Install Docker for the macOS runner. Docker containers are Linux, so on macOS
# the daemon runs inside a lightweight Linux VM. We use Colima (headless, CLI
# driven, no GUI/login/licence unlike Docker Desktop) to provide that daemon,
# plus the docker CLI and compose plugin.
#
# IMPORTANT — nested virtualization: Colima boots a Linux VM, which requires
# nested virtualization. Inside a tart guest that is ONLY available on Apple
# Silicon M3 or later, macOS 15+, AND when this VM itself is launched with nested
# virt enabled (e.g. `tart run --nested`). On M1/M2 hosts it cannot start.
#
# Because the Packer build VM is itself a tart guest (which we don't assume was
# launched with nested virt), we do NOT run `colima start` here — that would fail
# at build time. Instead we install a LaunchAgent that starts Colima on login, so
# the daemon comes up when the runner VM boots and auto-logs-in admin.

eval "$(/opt/homebrew/bin/brew shellenv)"

echo "==> Installing docker CLI, compose, and colima"
brew install docker docker-compose colima

USER_HOME="/Users/admin"
LAUNCH_AGENTS="${USER_HOME}/Library/LaunchAgents"
PLIST="${LAUNCH_AGENTS}/io.cloudeng.colima.plist"
COLIMA_BIN="$(command -v colima)"

echo "==> Installing LaunchAgent to start Colima on login (${PLIST})"
mkdir -p "${LAUNCH_AGENTS}" "${USER_HOME}/Library/Logs"

cat > "${PLIST}" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.cloudeng.colima</string>
    <key>ProgramArguments</key>
    <array>
        <string>${COLIMA_BIN}</string>
        <string>start</string>
        <string>--foreground</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>HOME</key>
        <string>${USER_HOME}</string>
    </dict>
    <key>StandardOutPath</key>
    <string>${USER_HOME}/Library/Logs/colima.out.log</string>
    <key>StandardErrorPath</key>
    <string>${USER_HOME}/Library/Logs/colima.err.log</string>
</dict>
</plist>
PLIST_EOF

# LaunchAgents under ~/Library/LaunchAgents are loaded automatically at user
# login, so admin's auto-login on VM startup will start Colima. We intentionally
# do not `launchctl bootstrap` it now (that would try to start the Linux VM in
# this build guest).
chown -R admin:staff "${LAUNCH_AGENTS}"
chmod 644 "${PLIST}"

echo "==> docker version: $(docker --version)"
echo "==> colima version: $(colima version | head -n1)"
echo "==> Docker (Colima) installed; daemon will start on VM login."
echo "    Requires nested virtualization: Apple Silicon M3+, macOS 15+, and this"
echo "    VM launched with nested virt enabled."
