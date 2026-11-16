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
install -d -m 0700 /var/lib/feldcore
if [ ! -s /var/lib/feldcore/setup-code ]; then
  # Same alphabet the daemon uses. The daemon keeps this file until the
  # administrator account is created, and prints it again in the journal.
  code=$(od -An -N8 -tu1 /dev/urandom | awk 'BEGIN{c="ABCDEFGHJKLMNPQRSTUVWXYZ23456789"} {for(i=1;i<=NF;i++) printf substr(c,($i%32)+1,1)}')
  (umask 077 && printf '%s\n' "$code" > /var/lib/feldcore/setup-code)
fi
systemctl daemon-reload
systemctl enable --now feldcore.service

ip=$(hostname -I 2>/dev/null | awk '{print $1}')
echo
if [ -s /var/lib/feldcore/setup-code ]; then
  code=$(tr -d '[:space:]' < /var/lib/feldcore/setup-code)
  echo "FELDCORE setup code: $(printf '%s' "$code" | cut -c1-4)-$(printf '%s' "$code" | cut -c5-8)"
  echo "Open http://${ip:-this-server}:8787 and enter that code. It works once."
else
  echo "FELDCORE is already set up. Open http://${ip:-this-server}:8787"
fi
