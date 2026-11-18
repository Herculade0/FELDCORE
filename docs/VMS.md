# Virtual machines

FELDCORE's VM Manager is a thin UI over libvirt/KVM, in the same style as Unraid's
VM manager. It does not invent a hypervisor. A guest is a libvirt domain, a disk
is a qcow2 or raw file on the domains share, and an ISO is a file in the isos share.

One-click operating-system templates are not part of this. They will come later
from a separate catalogue. **Add VM** opens the custom form directly.

## Why virsh and XML

The daemon generates a libvirt domain XML document and applies it with `virsh`.
Two other options were considered:

- The libvirt C bindings (`libvirt-go`) need cgo and the libvirt headers. That
  ties the single binary to one library version and breaks the "copy one file"
  install.
- `github.com/digitalocean/go-libvirt` is pure Go, but it speaks libvirt's XDR
  RPC. A mismatch with the host's libvirtd fails opaquely, and the API is a poor
  fit for a form whose source of truth is XML.

`virsh` is the tool an operator already uses to inspect a guest, which matches
how FELDCORE drives Samba, NFS and systemd. The form, the raw XML editor and the
tests are one pipeline. `go test` does not need libvirt installed.

Turning libvirt on under Settings installs `qemu-kvm`, `libvirt-daemon-system`,
`libvirt-clients` (virsh), `qemu-utils` (qemu-img), `ovmf` and `swtpm`. The
toggle still stays off when `/proc/cpuinfo` has no `vmx` or `svm` flag.

## What the form sets

- **CPU.** One checkbox per logical core. The ticks are the vCPU count, and
  each vCPU is pinned to the core you selected (`cputune` / `vcpupin`).
- **Memory.** Initial and maximum, in MB. The maximum is the balloon ceiling.
- **Machine.** Q35 or i440fx (`pc`).
- **Firmware.** SeaBIOS, or OVMF UEFI. A TPM 2.0 (`swtpm`, `tpm-crb` on Q35)
  requires OVMF.
- **Disks.** qcow2 or raw, on virtio, SATA, SCSI or IDE, sized in GB, stored
  under the domains path in a folder named after the VM.
- **ISOs.** An install ISO and a VirtIO driver ISO, both chosen by file name
  from the isos path. Paths are not accepted from the browser.
- **Graphics.** VNC bound to `127.0.0.1`, or a passed-through GPU as the only
  display. Video can be QXL, VirtIO, Cirrus, standard VGA or Bochs. Sound is
  ICH9, ICH6, AC97 or Sound Blaster 16.
- **Network.** NAT on libvirt's `default` network, or a bridge such as `br0`.
  Docker and `virbr` bridges are hidden. The NIC is virtio, e1000 or rtl8139.
- **Passthrough.** USB devices by vendor/product id, and PCI devices grouped
  by IOMMU group. A partial group is called out, because those devices are
  isolated together.
- **Shares.** virtio-fs mounts of this server's shares, when `virtiofsd` is
  installed. The tag is the share name.
- **Cloud-init.** Optional. A NoCloud seed labelled `cidata` is attached as a
  second CD. An optional base image already in the ISO or domains folder
  becomes the backing file of the first disk. FELDCORE does not download OS
  images. The password is written only into that seed.
- **Suggested hardware.** The operating-system menu fills the usual machine,
  firmware, disk bus and clock. It does not install anything.

The guest must be shut down before the form or the raw XML is saved. Saving
the form rewrites the domain. Devices the form does not understand are listed
as warnings, because they would be dropped. Edit XML keeps them.

## Console

A running guest's VNC display is proxied at `GET /api/v1/vms/{name}/console`.
The handler is the same gate as the host terminal: a valid admin session, and
an `Origin` that matches this host. An agent token cannot open it. The scope
check classifies the console as a shell, which no token includes, and the
handler then requires the administrator's session cookie. noVNC speaks VNC on
that WebSocket.

The proxy dials only a loopback address. Domain XML that publishes VNC on any
other address is refused, including raw XML. The display is not opened on the
LAN; the signed-in page is the only way in.

## VirtIO driver ISOs

VM Manager settings lists the **stable** and **latest** channels from
`https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/`. Those URLs
redirect to a versioned file under `archive-virtio/`. Directory listings on
that host sit behind a browser check, so the version is read from the redirect
rather than from an index page.

Download streams the ISO into the isos share with progress. It is kept only
when the byte count matches `Content-Length` and the image is ISO 9660
(`CD001` at sector 16, at least 32 MiB). Fedora publishes an MD5 `CHECKSUM`
for the RPM in that directory, not a hash of the ISO; if a line for the ISO
appears, it is checked too. A SHA-256 of the saved file is written next to it.
Old images can be deleted from the same card. The Windows field on the VM form
lists what was downloaded and offers **Download now** when the folder is empty.

Only `https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/…` is
fetched. The browser cannot supply a URL.

## KVM

The VMs page reads `/dev/kvm` rather than waiting for `virsh start` to fail.

- The device exists: nothing extra is said.
- The device is missing and `/proc/version` looks like WSL: the page explains
  `nestedVirtualization=true` under `[wsl2]` in `%UserProfile%\.wslconfig`.
- The device is missing and the CPU has no `vmx` or `svm`: the firmware
  virtualisation switch (or nested virtualisation) is off.
- The device is missing but the CPU has the flag: the kvm module is not loaded.

A guest can still be defined. Start is refused with that same explanation.

## Paths

Domains and ISOs default to the `domains` and `isos` shares (`/mnt/feldcore/data/…`
on a stock install). VM Manager settings can point them at another absolute
folder. `/etc`, `/boot`, `/usr` and the other operating-system directories are
refused. Disk files a guest already has are never shrunk. Deleting a VM
removes disk files only when asked, and only when they live in that VM's own
folder. ISOs are never deleted that way.

## Snapshots

`virsh snapshot-create-as`, revert and delete. Internal snapshots need qcow2.
A raw disk fails with libvirt's own message.
