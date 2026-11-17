---
id: setup-vm
kind: guide
title: Set up a VM
summary: Install libvirt, add a KVM guest, and open its console in the browser.
icon: monitor
order: 40
popular: false
related:
  - pass-through-gpu
references:
  - vms
  - gpu
---

A virtual machine is a libvirt/KVM guest. FELDCORE writes the domain XML and applies it with `virsh`. It does not download an operating system for you.

## Install libvirt

If the VMs page says libvirt is missing, **Install libvirt** installs QEMU, OVMF, and a software TPM, then starts libvirtd. The switch stays off when the CPU has no virtualisation flag (`vmx` or `svm`).

You can also turn the libvirt service on from Settings.

[[route:vms|Open VMs]]

[[route:settings.services|Open Services]]

## Add a guest

1. Put the install ISO in the `isos` share, or download the VirtIO driver ISO from VM Manager settings.
2. Press **Add VM**.
3. Tick the CPU threads the guest may use. Each one is pinned to that thread.
4. Set memory, machine type (Q35 or i440fx), and firmware. A TPM needs OVMF.
5. Add a disk. It is a qcow2 or raw file under the `domains` share, in a folder named after the VM.
6. Pick the ISO. The form lists file names from the ISO folder. It does not accept a path you type.
7. Save, then start the VM. The guest has to be shut down before you save the form again.

[[route:vms.settings|Open VM Manager settings]]

## Console

**VNC** is the console in this browser. The VNC server listens on `127.0.0.1` only. An agent token cannot open it.

**Physical GPU** uses a passed-through card as the only display, so there is no VNC picture. That path is [[help:pass-through-gpu|Pass through a GPU]].

> [!WARNING]
> Devices the form does not understand are listed as warnings, because saving the form would drop them. **Edit XML** keeps them. Do not point VNC at anything except loopback. Raw XML that does is refused.
