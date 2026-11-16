---
id: pass-through-gpu
kind: guide
title: Pass through a GPU
summary: Give a container the Intel or AMD GPU, or hand a whole card to a virtual machine.
icon: cpu
order: 50
popular: false
related:
  - setup-vm
  - run-docker
references:
  - gpu
  - vms
  - docker
---

There are two different switches. A container can share the render device. A VM can take a PCI card as its display. They are not the same thing, and this build does not yet lock a card so that only one of them can have it.

## Share it with a container

1. Open the container and find **Use the GPU**.
2. Turn it on when the line names an Intel or AMD GPU that is supported.
3. Apply.

[[route:docker|Open Docker]]

That passes `/dev/dri` into the container and adds the groups that own those devices. Any number of containers can share that device. NVIDIA is listed with "NVIDIA sharing isn't supported yet." There is no per-card picker and no Settings page that installs the NVIDIA Container Toolkit. Those are not in this build. [[help:gpu|GPU]] has the detail.

> [!WARNING]
> If no shareable GPU is present, creating the container fails with "This server has no GPU that FELDCORE can share with containers." Turn the switch off, or fix the driver, and try again.

## Hand a card to a VM

1. Shut the guest down.
2. Edit it and open **Graphics and sound**.
3. Choose **Physical GPU**, then the card. The address looks like `0000:01:00.0`.
4. Pass the rest of that IOMMU group as well. A partial group is called out, because those devices are isolated together.
5. Save and start. The monitor plugged into the card is the console. This browser will not show a picture.

[[route:vms|Open VMs]]

IOMMU groups come from the kernel. If the page says groups are not available, enable VT-d or AMD-Vi in the firmware and boot with `intel_iommu=on` or `amd_iommu=on`. Until groups exist, passing a device through will fail.

HDMI audio next to the card is not treated as a GPU for containers. The VM form can still pass it with the group.
