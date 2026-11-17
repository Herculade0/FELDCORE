---
id: first-run-wizard
kind: reference
title: First-run wizard
summary: The setup that opens before the normal pages, and the shorter guided setup you can run again from Settings.
icon: spark
order: 195
popular: false
related:
  - getting-started
references:
  - settings
  - disk-management
  - remote-access
---

After the administrator account exists, the wizard can open before Dashboard and the other pages. You can close the browser and come back. It continues at the same step. Optional steps can be skipped and changed later.

The steps are Welcome, This server, Network, Administrator or Sign-in, Storage, Remote access, Notifications, Backups, and Summary.

On Storage the choices are:

- **Keep** a folder. Nothing is formatted.
- **Use a disk** that is already mounted, or tick **Erase this disk and format it as ext4**. The page then asks you to type a confirmation phrase exactly. The phrase looks like `ERASE sdb`. The operating-system disk is refused.
- **Use a pool** that already exists. It is mounted and used for shares. A new name is only recorded. Assembling several disks still happens on the Storage page, after each disk serial is typed. [[help:disk-management|Storage]].

Creating another administrator, and formatting a disk, stay on this first run. They are not offered again.

## Run it later

Settings → General has **Guided setup**. It says it does not erase disks. It walks the host, network, sign-in, remote access, notification, and backup steps again.

[[route:settings|Open Settings]]

If you never see a wizard, this install was started with the wizard skipped, or that first run is already finished. Use Settings, Storage, Backups, and Health directly.

[[help:getting-started|Get started]] is the setup code and the account, which come before the wizard.
