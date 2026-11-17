---
id: settings
kind: reference
title: Settings
summary: Where appearance, the host, publishing, and remote access live.
icon: sliders
order: 350
popular: false
page: settings
related:
  - getting-started
  - secure-your-box
references:
  - remote-access
  - publish
  - vpn
  - api-tokens
---

Settings has four tabs. The `?` on this page opens this article. Each card has its own help topic when the behaviour is bigger than a switch.

[[route:settings|Open Settings]]

## General

**Appearance** is theme and accent, saved in this browser only.

**System** is the host name, time zone, and optional NTP servers. Blank NTP keeps the system's own servers. systemd-timesyncd is what gets used.

**Services** turns Samba, NFS, Docker, and libvirt on or off. On a Debian or Ubuntu host, turning one on installs it when it is missing, then starts it. On FELDCORE OS, Docker, Samba, and NFS are already in the image, and libvirt is installed as an extension. Turning a service off stops it. Packages are not removed. Libvirt stays disabled when the CPU cannot virtualise. When this machine is FELDCORE OS, or an NVIDIA device is present, **NVIDIA** installs the container toolkit. That does not install the kernel driver.

**Security** is the ufw firewall, your password, and the session length. [[help:secure-your-box|Secure your box]].

**Agent access** is API tokens. [[help:api-tokens|API tokens and scopes]].

**Updates** has two switches. **Security updates** enables Debian's unattended-upgrades, limited to security updates. FELDCORE OS refuses that switch, because the image is replaced instead. **Check for FELDCORE updates** looks for a newer `feldcored` release and tells you. It does not download or install one. Installing an OS image, and rolling back to the previous slot, are on [[help:updates-rollback|Updates]].

**App Store** is the feed URL for the Community Applications JSON.

**Defaults** includes whether removing a container also removes its image.

**Snapshots** is retention for container copies. [[help:snapshots|Snapshots]].

**About** is the version, hostname, OS, kernel, and CPU. The version string says pre-alpha.

## Network

The address card is DHCP or static. A wrong static address drops this page and is not restored automatically.

**Web addresses** is Caddy. [[help:remote-access|Cloudflare Tunnel and HTTPS]].

**VPN**, **Proton Drive backups**, and **Notifications** are on this tab. [[help:vpn|VPN]] and [[help:proton|Proton Drive and Mail]].

## Publishing

Published apps, and the protection rules in front of them. [[help:publish|Publish and protection]].

## Remote access

The Cloudflare Tunnel. [[help:remote-access|Cloudflare Tunnel and HTTPS]].
