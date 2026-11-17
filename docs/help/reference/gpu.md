---
id: gpu
kind: reference
title: GPU
summary: The container GPU switch, and PCI passthrough on a virtual machine.
icon: cpu
order: 310
popular: false
related:
  - pass-through-gpu
references:
  - docker
  - vms
---

[[help:pass-through-gpu|Pass through a GPU]] is the task. This page is what the code actually detects.

## Containers

**Use the GPU** is one switch on the container form. The list comes from the graphics devices on this machine.

Detection reads `/sys/class/drm/cardN/device/vendor` and keeps AMD, Intel, and NVIDIA. Connector names such as `card0-HDMI-A-1` are skipped.

- AMD and Intel are usable when a `/dev/dri/renderD*` node exists.
- NVIDIA is detected. Settings offers **Install NVIDIA Container Toolkit**. On FELDCORE OS that is a signed extension. On Debian it uses apt, and NVIDIA's repository has to be configured already. The install does not add the kernel driver.
- If there is a render node but no matching sysfs card, it is shown as a generic graphics device and can still be shared.

Turning the switch on mounts `/dev/dri` into the container and adds the numeric group ids that own those device nodes. The labels stored on the container are `feldcore.gpu=true` and `feldcore.gpu.groups`. Several containers may share the same device. The switch cannot name a card, and it cannot see a GPU that has no DRM node.

A create that asks for a GPU when none is usable fails before Docker is called: "This server has no GPU that FELDCORE can share with containers."

An App Store template that maps the device `/dev/dri` keeps that as an ordinary Device row. `--gpus` in extra parameters is kept and not applied.

There is no Settings → GPUs list in this build and no per-card picker. Settings does have a button that installs the NVIDIA Container Toolkit when this machine is FELDCORE OS or an NVIDIA device is present. A fuller design exists. It is not what the form does today.

[[route:docker|Open Docker]]

## Virtual machines

On the VM form, **Physical GPU** selects one PCI address as the only display. VNC is not available at the same time. Extra PCI devices, including the rest of an IOMMU group, are selected with the passthrough list. A partial group is called out because the devices are isolated together.

The address looks like `0000:01:00.0`. HDMI audio (`0000:01:00.1`) is not a GPU in the container list. The VM form can still pass it with the group.

This build does not bind the card to `vfio-pci` at boot, and it does not refuse the card to containers while a VM has it assigned. Those locks are not in the running app. Passing the card through still detaches the host driver while the guest is running, which is libvirt's managed behaviour.

[[route:vms|Open VMs]]
