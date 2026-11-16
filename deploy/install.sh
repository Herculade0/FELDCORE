#!/usr/bin/env sh
# FELDCORE installer: places the binary and a systemd unit, then starts it.
# Usage: sudo ./deploy/install.sh /path/to/feldcored
set -eu

BIN_SRC="${1:-bin/feldcored}"
BIN_DST="/usr/local/bin/feldcored"
UNIT_DST="/etc/systemd/system/feldcore.service"

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run as root (sudo)." >&2
  exit 1
fi

if [ ! -f "$BIN_SRC" ]; then
  echo "Binary not found: $BIN_SRC (build it with: make build)" >&2
  exit 1
fi

echo "Installing binary -> $BIN_DST"
install -m 0755 "$BIN_SRC" "$BIN_DST"

echo "Installing unit   -> $UNIT_DST"
install -m 0644 "$(dirname "$0")/feldcore.service" "$UNIT_DST"

echo "Enabling and starting feldcore.service"
systemctl daemon-reload
systemctl enable --now feldcore.service

echo
echo "FELDCORE is running. Open http://$(hostname -I | awk "{print \$1}"):8787"
