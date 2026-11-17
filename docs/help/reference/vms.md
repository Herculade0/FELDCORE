---
id: vms
kind: reference
title: Virtual machines
summary: libvirt and KVM, the form, the browser console, and VirtIO drivers.
icon: monitor
order: 290
popular: true
page: vms
related:
  - setup-vm
  - pass-through-gpu
references:
  - gpu
---

The VM manager is a form over libvirt. A guest is a domain, a disk is a file on the domains share, and an ISO is a file in the isos share. One-click OS templates are not part of this. **Add VM** opens the custom form.

The task version is [[help:setup-vm|Set up a VM]]. GPU passthrough is [[help:pass-through-gpu|Pass through a GPU]].

[[route:vms|Open VMs]]

## Install

**Install libvirt** on the VMs page, or the libvirt row under Services, installs QEMU, OVMF, and a software TPM, then starts libvirtd. On a Debian or Ubuntu host those are apt packages. On FELDCORE OS they are the libvirt extension. It stays off when `/proc/cpuinfo` has no `vmx` or `svm` flag. The browser console is noVNC, which is part of the web UI. There is no separate websockify program.

The page also reads `/dev/kvm` so Start can fail with a clear reason:

- The device exists: nothing extra is said.
- Missing, and the kernel looks like WSL: nested virtualisation is off in `%UserProfile%\.wslconfig`.
- Missing, and the CPU has no flag: the firmware virtualisation switch is off.
- Missing, but the CPU has the flag: the kvm module is not loaded.

A guest can still be defined. Start is refused with that explanation.

## The form

- **CPU.** One tick per logical core. The ticks are the vCPU count, and each vCPU is pinned to the core you selected.
- **Memory.** Initial and maximum, in MB. The maximum is the balloon ceiling.
- **Machine.** Q35 or i440fx.
- **Firmware.** SeaBIOS, or OVMF. TPM 2.0 needs OVMF.
- **Disks.** qcow2 or raw, on virtio, SATA, SCSI, or IDE, sized in GB, under the domains path in a folder named after the VM. Existing disks are never shrunk.
- **ISOs.** Chosen by file name from the isos path. A path typed in the browser is not accepted.
- **Graphics.** VNC on `127.0.0.1`, or a physical GPU as the only display. Video can be QXL, VirtIO, Cirrus, VGA, or Bochs. Sound is ICH9, ICH6, AC97, or Sound Blaster 16.
- **Network.** NAT on libvirt's `default` network, or a bridge such as `br0`. Docker and `virbr` bridges are hidden from the bridge list. The NIC is virtio, e1000, or rtl8139.
- **Passthrough.** USB by vendor and product id, and PCI devices by IOMMU group.
- **Shares.** virtio-fs mounts of this server's shares, when `virtiofsd` is installed. The tag is the share name.
- **Cloud-init.** Optional. A NoCloud seed labelled `cidata` is a second CD. An optional base image already in the ISO or domains folder becomes the backing file of the first disk. FELDCORE does not download OS images. The password is written only into that seed.
- **Suggested hardware.** The operating-system menu fills machine, firmware, disk bus, and clock. It does not install anything.

The guest must be shut down before the form or the raw XML is saved. Saving the form rewrites the domain. Unknown devices are listed, because they would be dropped. Edit XML keeps them.

Deleting a VM removes disk files only when you ask, and only when they live in that VM's own folder. ISOs are never deleted that way.

[[route:vms.settings|Open VM Manager settings]]

VM Manager settings can point the domains and ISOs folders somewhere else. `/etc`, `/boot`, `/usr`, and the other operating-system directories are refused. Blank means "use the share."

## Console

A running guest's VNC display is proxied to the browser over an authenticated WebSocket. The proxy dials only a loopback address. XML that publishes VNC anywhere else is refused, including raw XML. An agent token cannot open the console.

## VirtIO drivers

VM Manager lists the **stable** and **latest** channels from Fedora's virtio-win downloads. **Download** streams the ISO into the isos share and keeps it only when the size matches `Content-Length` and the image is ISO 9660. A SHA-256 is written next to the file. Old images can be deleted from the same card. The Windows field on the form lists what was downloaded and offers **Download now** when the folder is empty. The browser cannot supply a URL.

## Snapshots

The VM row can create, revert, and delete libvirt snapshots (`virsh snapshot-create-as`). Internal snapshots need qcow2. A raw disk fails with libvirt's own message. These are not the container snapshots in [[help:snapshots|Snapshots]].
