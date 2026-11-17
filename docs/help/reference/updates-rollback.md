---
id: updates-rollback
kind: reference
title: Updates and rollback
summary: The Updates page checks a signed OS image, stages it on the other slot, and can select the previous image.
icon: refresh
order: 860
page: updates
related:
  - secure-your-box
references:
  - settings
---

Updates replaces a whole FELDCORE OS disk slot. Nothing is installed with apt onto the running system. The `?` on this page opens this article.

[[route:updates|Open Updates]]

A machine that is not running the FELDCORE OS image can still open the page and check a signed release. Staging, rollback, restart, and extensions stay disabled, and the page says so.

## This image

The card shows the running version and slot, the next boot, how many tries that boot has left, and the kernel. A boot that has been marked good shows **Good** instead of a try count. The card title reports Secure Boot. This image leaves it off.

Each slot is listed under the card. An empty slot stays empty until an update is staged onto it.

## Release

**Check for updates** downloads the signed release and shows its version and notes.

**Update now** writes that image onto the inactive slot. It stays disabled when this machine is not the appliance, when the signed release is the version already running, or when no release has been checked. The progress panel says **Staged** when it finishes. The new image gets three boots. If those boots do not come up healthy, the previous image is selected.

**Roll back** selects the earlier image for the next start. It does not restart the machine. The button names that version when one is available. **Restart into slot A** or **Restart into slot B** appears when the next boot is not the slot that is running, and it reboots the appliance.

**Install at** schedules one update. The time is cleared after the job starts, so a missed clock does not keep starting it. **Clear** removes a schedule that has not started.

**Release address** is where the signed manifest is fetched. When the system sets that address, the field cannot be changed. The hint names the public key when one is found. **Save address** stores an address you type.

## Extensions

Extensions are signed systemd-sysext images. The list shows what is installed and what the signed release offers. **Install** and **Reinstall** download one onto the state partition. **Remove** deletes one that was installed there. An extension shipped inside the image cannot be removed. An extension built for a different kernel cannot be installed.

NVIDIA, the container toolkit, and libvirt show up here when a release offers them. The page does not install them with apt.

## Settings

Settings → General → **Updates** is a different card. **Security updates** turns on Debian's unattended-upgrades, limited to security updates. **Check for FELDCORE updates** looks for a newer `feldcored` release and can email you. It does not write an OS slot.

[[route:settings.updates|Open the Settings card]]
