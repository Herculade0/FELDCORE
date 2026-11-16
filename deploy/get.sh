#!/bin/sh
# Install FELDCORE from the latest GitHub release. No Go or Node on the server.
#
#   curl -fsSL https://raw.githubusercontent.com/Herculade0/FELDCORE/main/deploy/get.sh | sudo sh
#
# Override the repository with FELDCORE_REPO=owner/name.
set -eu

REPO="${FELDCORE_REPO:-Herculade0/FELDCORE}"

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: curl -fsSL https://raw.githubusercontent.com/Herculade0/FELDCORE/main/deploy/get.sh | sudo sh" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) asset=feldcored-linux-amd64 ;;
  aarch64 | arm64) asset=feldcored-linux-arm64 ;;
  *)
    echo "Unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
base="https://github.com/${REPO}/releases/latest/download"
echo "Downloading ${asset}"
curl -fsSL -o "$tmp/feldcored" "$base/$asset"
curl -fsSL -o "$tmp/feldcore.service" "$base/feldcore.service"
install -m 0755 "$tmp/feldcored" /usr/local/bin/feldcored
install -m 0644 "$tmp/feldcore.service" /etc/systemd/system/feldcore.service

install -d -m 0700 /var/lib/feldcore
if [ ! -s /var/lib/feldcore/setup-code ]; then
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
