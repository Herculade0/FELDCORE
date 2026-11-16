# GPU selection

Design only. This describes how FELDCORE should find GPUs, let a container use a chosen one, and stay consistent with VM passthrough. It does not change the daemon or the UI.

The VM manager (branch `cursor/vm-manager-0192`, in progress) owns virtual machines: domain XML, the PCI / IOMMU form, and libvirt. This document owns containers, the shared inventory those screens both read, and the rule for when a GPU cannot be in both places at once.

## Recommendation

Ship a container picker first, on Debian, before dashboard graphs or a driver installer.

The first slice:

- Build one inventory keyed by PCI address, in the same form the VM spec already uses (`0000:01:00.0`). Rebuild it on every read, at boot and after a card is plugged in.
- Settings → GPUs is a read-only list: model, vendor, driver, VRAM, and assigned to (a container, a VM, or nothing).
- The container form starts with no GPU selected, whether it was opened blank or from a template. The user ticks one GPU. A hard-coded `/dev/dri` or `--gpus` is removed so Apply does not pass it on its own.
- One GPU per container. An Intel iGPU and an NVIDIA card are not combined. Several containers may still share the GPU they each picked, unless a VM owns it.
- Remember that PCI address on the container. Resolve the render node again at create time, so a renumbered `renderD128` still finds the same card.
- A GPU assigned to a VM is locked to that VM, including while the VM is stopped. The picker shows it and does not allow a tick.
- Detect an NVIDIA card whenever it is present. Settings has a button to download and install the NVIDIA Container Toolkit. On Debian the driver stays the administrator's job.

Leave these for later: live utilisation, codec columns, a second GPU on one container, a ROCm switch, Raspberry Pi and other ARM boards, and the NVIDIA driver as a systemd-sysext on FELDCORE OS.

## Decisions

1. GPUs start deselected on the container form, from a template or not. Community Applications templates rarely ask for a GPU. The user ticks what they want.
2. An NVIDIA card is detected at boot and on hotplug. Settings downloads and installs the Container Toolkit. The driver is the administrator's job on Debian.
3. A GPU assigned to a VM is locked to that VM and bound at boot, Unraid stub style, not only while the VM runs. The VM manager branch owns the bind. Containers use the same PCI address.
4. The first slice does not put an Intel iGPU and an NVIDIA card on the same container. One GPU per container.
5. The first slice includes the read-only Settings → GPUs list (model, vendor, driver, VRAM, assigned to).
6. Raspberry Pi and other ARM boards wait.

## What the product does today

Containers already have a boolean GPU flag.

- `DetectGPUs` in `internal/docker/gpu.go` reads `/sys/class/drm/cardN/device/vendor` and keeps AMD (`0x1002`), Intel (`0x8086`) and NVIDIA (`0x10de`). Connector entries such as `card0-HDMI-A-1` are skipped.
- AMD and Intel are marked usable when any `/dev/dri/renderD*` node exists. NVIDIA is detected and marked "NVIDIA sharing isn't supported yet."
- Turning the switch on passes the whole `/dev/dri` directory into the container and adds the numeric group ids that own those nodes (`HostConfig.GroupAdd`). The labels are `feldcore.gpu=true` and `feldcore.gpu.groups`.
- The container form (`GpuRow` in `web/src/components/docker/ContainerDialog.tsx`) is one toggle. The choice list comes from `GET /api/v1/docker/options`.
- A create that asks for a GPU when none is usable fails before Docker is called, with "This server has no GPU that FELDCORE can share with containers."
- An App Store template that maps the device `/dev/dri` keeps that as an ordinary Device row. `--gpus …` in ExtraParams is kept on the spec and is not applied. The setup notes say so.

That switch is the right idea for a machine with one Intel or AMD GPU. It cannot name a card, it cannot see a GPU that has no DRM node (NVIDIA without `nvidia-drm`, or a card on `vfio-pci`), and a missing GPU becomes a create error.

## How this lines up with VMs

The VM manager (branch `cursor/vm-manager-0192`) owns virtual machines. This document does not redefine domain XML, the IOMMU form, or libvirt.

What that branch already does, and what container selection follows:

- A VM is KVM through `virsh` and hand-written domain XML. The XML is the source of truth for which PCI address a VM has.
- The display GPU is one PCI address on `spec.gpu`. Extra PCI devices are `spec.pcis`. The address is `0000:01:00.0` (lowercase, domain included).
- Graphics are VNC or the physical GPU, not both as the console.
- IOMMU groups come from `/sys/kernel/iommu_groups` on the VM form. Passing a whole group stays on that form.
- HDMI audio (`0000:01:00.1`, class `0x0403`) is not a GPU in the container list. The VM form can still pass it with the group.

The owner wants Unraid's lock, which that branch does not do yet. Assigning a GPU to a VM locks it to that VM, including while the VM is stopped, by binding the card to `vfio-pci` at boot. The branch today writes a hostdev with `managed='yes'`, so libvirt detaches the host driver only while the VM runs, and it does not write a boot stub. Container selection still keys off the address that branch already stores. The stub is added on that branch, not here.

Container code does not write `vfio-pci` ids, modprobe config, or domain XML. It reads the lock:

- Locked when the address is `spec.gpu` on any defined domain, or a display-class address in that domain's `spec.pcis`. Stopped counts.
- Also locked when the kernel driver is already `vfio-pci`.
- Assigned to shows the VM name in both cases.
- One GPU belongs to one VM. The VM form should refuse an address already stored on another domain. Containers refuse it with "Assigned to virtual machine windows."
- A new assignment, and removing one, takes effect at the next boot, because the stub is applied then. The picker follows the domain XML immediately, so a new container cannot take the card while the reboot is still pending. After the GPU is removed from the VM and the machine has booted without the stub, containers can use the card.
- The VM side refuses to assign a GPU that running containers hold. It reads the label `feldcore.gpu.ids`. Containers are not stopped automatically.

Until `virsh` is installed, driver `vfio-pci` is still enough for the container picker to refuse the card.

| Piece | Owner |
| --- | --- |
| PCI display inventory, render nodes, driver, VRAM | New `internal/gpu`, called from Docker |
| Domain XML, IOMMU groups, boot-time `vfio-pci` stub for an assigned GPU | `internal/vm` on `cursor/vm-manager-0192` |
| Which containers hold this address | `feldcore.gpu.ids`, shown on `GET /api/v1/gpus` |
| Which VM is assigned this address, running or stopped | That branch's domain XML, read when `virsh` is present |

## 1. Detection

Start from PCI display devices, then attach DRM nodes, then ask vendor tools for the fields sysfs does not have. A card with no DRM node must still appear. Today's scan misses it. Raspberry Pi, Mali, and other platform GPUs with no PCI address are a later slice. The first slice is PCI only: Intel, AMD, NVIDIA, and virtio-gpu.

The inventory is rebuilt on every `GET /api/v1/gpus`. Nothing is frozen at daemon start. Opening Settings or the container form, and a Rescan button on Settings → GPUs, see a card that was plugged in after boot. A udev signal for the `drm` or `pci` subsystem triggers the same rebuild so an open Settings page can refresh. `nvidia-smi` is part of that rebuild when the binary exists, so a proprietary driver that binds after boot is picked up without restarting FELDCORE.

### Where to look

| Source | What it is for |
| --- | --- |
| `/sys/bus/pci/devices/*/class` | Find display devices. Class `0x0300` VGA, `0x0302` 3D, `0x0380` other display. |
| `/sys/class/drm/cardN` | Join a PCI device to its DRM card. Skip names containing `-` (those are connectors). |
| `…/device/vendor`, `…/device/device` | Vendor and device ids, for the model name. |
| `…/device/uevent` (`PCI_SLOT_NAME`) | PCI address when the DRM tree is the starting point. |
| `…/device/driver` symlink | Driver in use: `i915`, `xe`, `amdgpu`, `radeon`, `nvidia`, `nouveau`, `virtio-gpu`, `v3d`, `vc4`, `panfrost`, `vfio-pci`, or none. |
| `/dev/dri/by-path/` | Stable device paths. `pci-0000:00:02.0-render` and `pci-0000:00:02.0-card` point at the current `renderD*` and `card*` nodes. |
| `/usr/share/misc/pci.ids` | Model string ("HD Graphics 4000", "Navi 21"). |
| `nvidia-smi` | NVIDIA name, UUID, VRAM, driver version, utilisation, encoder sessions. Query with an argument list, never a shell. |
| `rocm-smi` | Optional AMD marketing name and compute id. Detection works without it. |
| `vainfo --display drm --device <render node>` | VA-API encode and decode profiles for Intel and AMD. |
| `lspci -mm` | Fallback model string when `pci.ids` is missing. |

`/dev/dri/renderD128` is recorded so the UI can show it. It is not the id FELDCORE stores. The kernel assigns those numbers in probe order, and a kernel or slot change can swap them.

### Stable id

- PCI GPU: the address `0000:01:00.0`, lowercase, same text the VM spec stores.
- Platform GPU with no PCI address (Raspberry Pi V3D, many Mali parts): later. The id will be the `/dev/dri/by-path` filename with `-render` or `-card` removed. Those boards are not in the first slice, and they never appear in the VM PCI picker.
- NVIDIA also has a UUID (`GPU-…` from `nvidia-smi`). Store it beside the PCI address. The container toolkit wants the UUID. The inventory key stays the PCI address so a VM and a container refer to one card.

Normalise NVIDIA's bus id before comparing. `nvidia-smi` sometimes prints `00000000:01:00.0`. Store `0000:01:00.0`.

### Fields to show

| Field | How |
| --- | --- |
| Model | `pci.ids`, else `vendorId:deviceId`. NVIDIA overrides with the `nvidia-smi` name when that works. |
| Vendor | `intel`, `amd`, `nvidia`, `virtio`, or `other`, from the PCI vendor id or the DRM driver. |
| VRAM | AMD: `mem_info_vram_total` under the PCI device. Intel Arc and other discrete parts: the same file when it exists. Otherwise **shared with system RAM** (Intel iGPU, AMD APU with only GTT, virtio-gpu). A missing file is shared memory, not a failed card. |
| Driver | The `driver` symlink name, plus `nvidia-smi`'s driver version for NVIDIA. Empty means the driver is not loaded. |
| Render node | Current `renderD*` via the by-path symlink. |
| Card node | Current `card*` via the by-path `-card` symlink. Some apps open this as well as the render node. |
| PCI address | The stable id above. |
| Codecs | Later. The first slice does not show them. How to collect them is below, so the next slice has a source. |
| Primary display | Sysfs `boot_vga` is `1`. Useful on the detail row. The first-slice Settings list does not add a column for it. |
| IOMMU group | The group number, for the link to the VM form. The container picker does not ask the user to pass the whole group. |

### Codecs (later)

Not in the first slice. When they are added, probe per render node and keep going when the tool is missing. An empty list with "Codec list unavailable" is a complete answer.

**Intel Quick Sync.** On Linux this is VA-API. There is no extra device node. A container that uses Quick Sync opens the render node. The codec list is what `vainfo` reports.

- Broadwell and newer, including Arc, use the iHD driver (`intel-media-va-driver` inside the app's image). The kernel driver is `i915`, or `xe` for Arc on kernel 6.8 and newer.
- The 2012 Mac mini's HD Graphics 4000 (Ivy Bridge, PCI `8086:0166`) and the other HD 2000–HD 5000 parts use the older i965 VA-API driver. iHD does not support them. If `vainfo` fails on one of these, retry with `LIBVA_DRIVER_NAME=i965`. Expect H.264 encode and MPEG-2 decode. HEVC is not on this chip. Memory is shared. The card is usually the console (`boot_vga`). Containers are the useful path. Passing it through to a VM takes the display away.

**AMD AMF.** AMF is not a device node on Linux. Containers encode through VA-API on the same render node. The codec list comes from `vainfo` (Mesa). `rocm-smi` is only a nicer name and a compute id.

**NVIDIA NVENC / NVDEC.** These exist when the proprietary `nvidia` driver is loaded. They are not a node under `/dev/dri`. `nvidia-smi -q` reports encoder capability and active sessions. `nouveau` exposes a render node and does not expose NVENC. The row should say that hardware encode needs the NVIDIA driver.

**virtio-gpu and WSL.** Vendor `0x1af4` or driver `virtio-gpu`: label it "Virtual GPU". Pass the render node when one exists. Hardware encode is not available. WSL also exposes `/dev/dxg`. Show that as a virtual GPU. NVIDIA GPU-PV inside WSL can make `nvidia-smi` work via the Windows driver. Show the card and leave the WSL toolkit path alone.

**ARM boards (later).** Driver `v3d`, `vc4`, `panfrost`, `panthor`, or a vendor Mali module, identified from `/dev/dri/by-path`. Raspberry Pi encode is usually a V4L2 node (`/dev/video10`, `/dev/video11`, and similar), not VA-API. The first slice does not list or pass these devices.

### Driver not loaded

The PCI device is present, `driver` is empty, and there is no render node. Typical cases: Intel stuck on the firmware framebuffer (`efifb`) because `i915` never bound, NVIDIA with no driver installed, AMD firmware missing so `amdgpu` gave up. The row is visible and not selectable. The note is "Driver not loaded." For the framebuffer case, add "The display is on a firmware framebuffer, so containers cannot use this GPU."

FELDCORE does not load or unbind kernel modules from the container form.

## 2. Assigning a GPU to a container

The client sends one PCI address, or an empty list. The server resolves device paths. The GPU field never accepts a raw path. Arbitrary devices stay on the existing Device row, which already requires a `/dev/` path. The first slice rejects a second id.

### Intel and AMD

Pass the chosen card's render node and card node, using the by-path paths as `PathOnHost` so Docker opens the current node when the container starts:

```text
/dev/dri/by-path/pci-0000:00:02.0-render  →  /dev/dri/renderD128
/dev/dri/by-path/pci-0000:00:02.0-card    →  /dev/dri/card0
```

Inside the container, keep the host's current `renderD*` and `card*` names, and mount the by-path symlink at the same path. Apps that scan `/dev/dri` then see the node they expect. A second GPU on the same container is later.

Permissions stay as they are now. Render nodes are group `render`, card nodes are group `video`, mode `crw-rw----`. The container gets those numeric group ids via `GroupAdd`, because the names `render` and `video` may not exist in the image, and the device cgroup permission is `rwm`. If a node is `root:root` mode `600`, group add cannot help. Show "This GPU's device node is not open to the render or video group" and leave the host permissions unchanged.

`/dev/kfd` is the AMD compute node. It is one node for every AMD GPU. The device row is removed with the other GPU devices, so a template cannot pass it while the picker is empty. If that template had `/dev/kfd` and the user then selects an AMD GPU, pass `/dev/kfd` with the render node. Selecting an Intel or NVIDIA GPU does not pass it. `ROCR_VISIBLE_DEVICES` waits for the ROCm switch. VA-API transcoding does not need `/dev/kfd`. Whether every selected AMD GPU should get `/dev/kfd` later is still open.

### NVIDIA

Passing `/dev/dri` does not give a container NVENC or CUDA. The proprietary driver needs the NVIDIA Container Toolkit, which mounts userspace libraries that match the kernel module.

When the toolkit is installed and the `nvidia` driver is loaded, create the container with the Engine API device request:

```json
"DeviceRequests": [{
  "Driver": "nvidia",
  "DeviceIDs": ["GPU-uuid-from-nvidia-smi"],
  "Capabilities": [["gpu", "compute", "utility", "video"]]
}]
```

`video` is NVENC/NVDEC. `compute` and `utility` are CUDA and `nvidia-smi`. Add `graphics` only when the user asks for OpenGL. The first slice sends one UUID in `DeviceIDs`. `Count: -1` (every NVIDIA GPU) is not used.

Docker 25 and newer can take the same selection as a CDI device (`nvidia.com/gpu=GPU-uuid`). Prefer CDI when `nvidia-ctk` has generated the spec and the engine accepts it. Fall back to `DeviceRequests` on older Docker. Both come from the toolkit. FELDCORE does not mount `libcuda.so` itself.

The card is selectable only when the driver is `nvidia` and the toolkit is installed. Otherwise the row is visible and not selectable. The note is "NVIDIA Container Toolkit is not installed" or "Driver not loaded. Installing the NVIDIA driver is done outside FELDCORE."

Settings → GPUs shows **Install NVIDIA Container Toolkit** whenever the inventory contains a PCI device with vendor `0x10de`, including a card that appeared after boot and a card whose driver is not loaded yet. The button downloads and installs `nvidia-container-toolkit` (NVIDIA's apt repository, then the package), runs `nvidia-ctk runtime configure`, and restarts Docker. The confirm step says containers with a restart policy will stop and start. The button does not install the kernel driver. On FELDCORE OS there is no apt button. The toolkit arrives with the driver sysext in section 5.

`nouveau` is still detected. The row says NVENC needs the NVIDIA driver. The toolkit button can still be used. FELDCORE does not replace `nouveau` with the proprietary driver.

### What Unraid does

Unraid splits discovery, stats, and assignment across plugins. FELDCORE can do all three in one inventory.

| Unraid piece | Behaviour | FELDCORE equivalent |
| --- | --- | --- |
| Nvidia-Driver plugin | Installs a proprietary driver built for Unraid's kernel. The template editor then shows `NVIDIA_VISIBLE_DEVICES` as a dropdown of UUIDs from `nvidia-smi -L`, plus `NVIDIA_DRIVER_CAPABILITIES` (often `all`). | Toolkit device request, capabilities `compute,utility,video` by default. The driver itself is a host or OS-image concern. |
| Intel-GPU-TOP plugin | Installs `intel_gpu_top` for statistics. Assignment is still a Device row. Templates ship `/dev/dri`. A second GPU is a hand-edited `/dev/dri/renderD128`. | Picker resolves the PCI address to the render and card nodes. `intel_gpu_top` is only for later stats. |
| GPU Statistics, RadeonTOP | Dashboard utilisation. They do not assign devices. | Dashboard card in the later stats slice. AMD busy percent is already in sysfs (`gpu_busy_percent`). |
| VFIO-PCI config | A GPU listed there binds to `vfio-pci` at boot and stays unavailable to Docker while that assignment exists, including when no VM is running. A reboot applies the change. | Same lock. The VM branch records the address on the domain and is the place that writes the boot stub. Containers treat that address as assigned to the VM. |

### App Store prefill

`TemplateToSpec` should recognise a GPU request and move it onto the picker, the same way it already lifts ports, paths, and devices out of ExtraParams.

Treat these as a GPU request:

- A Device whose host path is `/dev/dri`, `/dev/dri/renderD*`, `/dev/dri/card*`, or `/dev/dri/by-path/*`.
- A Device `/dev/kfd`.
- ExtraParams `--gpus all`, `--gpus device=…`, or `--runtime=nvidia`.
- Variables `NVIDIA_VISIBLE_DEVICES` and `NVIDIA_DRIVER_CAPABILITIES`.

Remove those rows from the visible Device and Variable lists so a template cannot pass a GPU behind the picker. Leave unrelated devices (`/dev/net/tun`, a Coral USB) alone.

The picker still starts with nothing selected. Community Applications templates rarely include a GPU device. When one of the rows above was removed, add a note: "This template included a GPU device. Tick a GPU below if this app should use one." Do not tick a card because the template named `/dev/dri`, `renderD128`, `--gpus all`, or an NVIDIA UUID.

If the inventory is empty, the section says **No GPU found** and Apply creates the container with no GPU. If the host has GPUs and the template named one this host does not have, the list is still deselected. There is no substitute card, and there is no "No GPU found" while other GPUs are listed.

`--gpus` leaves ExtraParams, so it is not applied on its own and it no longer shows up as "kept for review but not applied."

## 3. More than one GPU

### Picking one container's GPU

The spec stores a list so a later slice can hold more than one id. The first slice allows zero or one:

```json
"gpus": ["0000:00:02.0"]
```

An empty list means the container has no GPU. The old boolean `gpu` stays as "the list is non-empty" so the Docker table badge keeps working. A request with two ids is rejected.

Labels on the container:

- `feldcore.gpu=true` when the list is non-empty
- `feldcore.gpu.ids=0000:00:02.0`
- `feldcore.gpu.groups` as today, the gids actually added

A new form starts deselected, including Edit of a container that has never stored an id. Edit of a container that already has one id shows that id selected.

Existing containers have `feldcore.gpu=true` and a `/dev/dri` device, from the current switch, and no `feldcore.gpu.ids`. Opening Edit leaves the picker deselected and notes "This container was given every GPU under /dev/dri. Pick the one it should keep." The next save writes the single id the user ticked, or clears the GPU if they save with nothing ticked. A missing id is never replaced with whichever card happened to become `renderD128`.

### Sharing

Containers share a GPU. Jellyfin and Plex can both hold the same render node. The kernel allows it, and the UI says "Other containers can use this GPU at the same time." There is no "only this container" lock in the first slice. A lock would be FELDCORE policy on top of the kernel, and it can wait until someone asks.

A VM does not share. If any defined domain lists the address, the picker shows the GPU and does not allow a tick, whether that VM is running or stopped. The note is "Assigned to virtual machine windows." Create is refused with the same sentence, before Docker is called. A container that already has the id stored, from before the assignment, is refused on the next FELDCORE start or edit. Docker's own restart policy can still start it until the boot stub removes the device node. That gap closes on the reboot that applies the stub.

The VM branch owns the other direction: before saving an assignment, read `feldcore.gpu.ids` on running containers. If one holds the address, refuse and name the containers. Do not stop them, and do not unbind the host driver until the next boot.

A GPU whose driver is `vfio-pci` is unavailable to containers even when `virsh` cannot be queried. The note is "Assigned to virtual machines. The host driver is not loaded." When the domain name is known, use that name instead.

### One GPU on a container

The first slice is single-select. The host config is either DRM nodes for an Intel, AMD, or virtio GPU, or one NVIDIA device request. It is never both. An Intel iGPU and an NVIDIA card on one container waits. Two cards of the same vendor on one container waits as well. Several containers may each select the same shareable GPU.

iGPU and dGPU are two rows. The form does not prefer one. The primary GPU (`boot_vga`) can still be shared with containers when no VM is assigned it. Assigning that card to a VM takes the console away on the reboot that stubs it. The VM form is the place that warning belongs. The container picker does not block `boot_vga` on its own.

## 4. A GPU that is missing or has moved

| Situation | What the user sees | What Apply does |
| --- | --- | --- |
| Host has no GPU | **No GPU found.** Nothing to tick. | Creates the container with an empty GPU list. Docker is not asked for a device. |
| Template named a GPU device, including one this host does not have | Picker deselected. Note that the template included a GPU device. | Same, unless the user ticks a GPU that is actually selectable. |
| User ticked a GPU, and it vanished between page load and Apply | Error naming the PCI address. The form reloads the inventory. | Create does not run. |
| Saved container, GPU address gone after reboot | Edit form shows "Missing: Intel HD Graphics 4000 (0000:00:02.0)". The Docker list can show the same on the row. | Saving with the GPU cleared removes the device. Saving with it still selected returns the error above. |
| Render node number changed, PCI address still there | Nothing. The by-path path still opens the card. | No edit required. |
| Card moved to a new PCI slot, so the address changed | The old id is missing. The new address is a new row, unticked. | The user ticks the new row. FELDCORE does not match "same vendor, similar name." |
| Driver not loaded | Row visible, not selectable, note "Driver not loaded." | A tick is rejected by the server as well as the form. |

Docker's own restart policy can still start a container while FELDCORE is not in the path. Baking the by-path device path means a renumbered render node keeps working. A card that is actually gone makes Docker fail that start. FELDCORE reports it from the label versus the inventory. The first slice does not rewrite containers on boot.

The current error "This server has no GPU that FELDCORE can share with containers." goes away for the empty selection. A selected id that does not resolve still fails in FELDCORE, with the id in the message, and Docker's "no such device" is not the message the user sees.

## 5. Drivers on FELDCORE OS

FELDCORE OS boots a read-only Debian image with systemd-boot. The running system has no apt. Extra software is a systemd-sysext on the config partition, merged at boot. Container apps already bring their own userspace. The host only needs the kernel driver, firmware, device nodes, and, for NVIDIA, the container toolkit.

### NVIDIA sysext

The proprietary kernel module has to be built for the kernel version and config inside that OS image (`uname -r` and vermagic). A sysext built for the other A/B slot will not load after an update.

One extension image, built in CI against that kernel, containing:

- `nvidia.ko` and the companion modules, under the image's module path
- `nvidia-smi` and the matching userspace libraries
- `nvidia-container-toolkit` and a CDI spec from `nvidia-ctk cdi generate`

Activate it only when its version matches the booted slot. On a mismatch the GPU row says "NVIDIA driver does not match this kernel" and the picker stays disabled. Refresh with `systemd-sysext merge` at boot. There is no DKMS and no compiler on the running system.

The Debian product can keep using the distribution's NVIDIA packages, installed by the administrator. The container UI installs the toolkit package only, and only on Debian.

### AMD and Intel

`amdgpu`, `i915`, and `xe` are in the kernel that ships in the OS image. They do not need a per-machine sysext.

GPU firmware (the `linux-firmware` blobs those drivers request) has to be in the OS image. Missing firmware looks like a driver that bound and then failed, or a card with no render node. That is an image-build requirement, not a picker feature.

Codec userspace (`intel-media-va-driver`, `i965-va-driver`, Mesa) lives in the app container. Jellyfin does not use the host's `vainfo` stack to transcode. The host copies of those libraries exist so FELDCORE can fill the codec column. They are not a dependency of "pass the device node."

`intel_gpu_top` and `radeontop` are optional stats tools. AMD utilisation and VRAM used are in sysfs and need no extra package. NVIDIA stats need `nvidia-smi`, which is in the driver extension.

### Out of scope

- Building or publishing the NVIDIA sysext (it waits on the OS image build)
- CUDA or ROCm installed on the host
- DKMS, kernel headers, or a compiler on the running OS
- vGPU, MIG, and SR-IOV splitting
- Loading, blacklisting, or unbinding kernel modules from the container screens. The boot-time vfio stub for a GPU assigned to a VM belongs to the VM manager
- Looking Glass, GPU reset quirks, and the NVIDIA guest "error 43" workaround (VM document, if they are done at all)
- Apple Silicon. "Apple-era" here means an Intel Mac running Debian, such as the 2012 Mac mini's HD 4000

## 6. Screens

No new top-level nav item. GPUs are a Settings section. The container form links to it. The dashboard card waits for the stats slice.

### Settings → GPUs

Read-only. The first slice shows model, vendor, driver, VRAM, and assigned to. Nothing in the table is editable. The toolkit button is the one action, and it is separate from the rows.

```text
GPUs                                                    [ Rescan ]
Containers can share a GPU. A GPU assigned to a virtual machine
is reserved for that machine, including while it is stopped.

Model                    Vendor   Driver     VRAM      Assigned to
HD Graphics 4000         Intel    i915       shared    plex
GeForce RTX 3060         NVIDIA   nvidia     12 GB     —
Radeon RX 6800           AMD      vfio-pci   16 GB     VM windows
(empty host)
No GPU found on this server.

[ Install NVIDIA Container Toolkit ]
Shown when an NVIDIA card is present. Downloads and installs the
toolkit, then restarts Docker. Containers set to restart come back.
The NVIDIA driver is installed separately, by the administrator.
```

Assigned to is the container name, `VM windows` when the address is on a domain, or blank. A card on `vfio-pci` with no domain name yet still shows as assigned to a VM. FELDCORE OS has no install button. It shows the sysext note from section 5.

### Container form

One choice, and it starts empty. Edit of a container that already stores one id shows that id selected.

```text
+------------------------------------------------------------------+
| GPU                                                              |
| Other containers can use the same GPU at the same time.          |
|                                                                  |
| ( ) None                                                         |
| ( ) Intel HD Graphics 4000    Intel   i915      shared           |
| ( ) NVIDIA GeForce RTX 3060   NVIDIA  no toolkit                 |
| ( ) AMD Radeon RX 6800        AMD     vfio-pci   VM windows      |
+------------------------------------------------------------------+
```

The NVIDIA row and the VM row cannot be selected. Their text is the reason. A host with no GPU replaces the list with **No GPU found**. Apply stays enabled.

A template that included `/dev/dri` or `--gpus` adds one line under the heading: "This template included a GPU device. Tick a GPU below if this app should use one." The radios stay on None.

### Dashboard card (later slice)

Place it with the CPU and memory cards. Hide it when the inventory is empty.

```text
+----------------------------------+
| Graphics            2 devices    |
| RTX 3060                 34%     |
| VRAM  4.1 / 12 GB                |
| Encoder  2 sessions              |
| HD 4000                  idle    |
| Encoder  busy                    |
+----------------------------------+
```

NVIDIA session count comes from `nvidia-smi -q` ("Active Sessions"). Intel and AMD do not publish a session count. For those, show the video engine busy or idle (`intel_gpu_top` when it is installed, otherwise the AMD sysfs busy file, otherwise omit the encoder line). The card links to Settings → GPUs.

## 7. API and data model

`GET /api/v1/gpus` is the shared read. `GET /api/v1/docker/options` can keep returning a short copy for the container form (id, name, vendor, selectable, note) so the dialog stays one request.

```json
{
  "gpus": [
    {
      "id": "0000:00:02.0",
      "vendor": "intel",
      "name": "Intel HD Graphics 4000",
      "vendorId": "8086",
      "deviceId": "0166",
      "pciClass": "0300",
      "driver": "i915",
      "driverVersion": "",
      "renderNode": "/dev/dri/renderD128",
      "cardNode": "/dev/dri/card0",
      "byPath": "/dev/dri/by-path/pci-0000:00:02.0-render",
      "nvidiaUuid": "",
      "vramBytes": 0,
      "memoryKind": "shared",
      "bootVga": true,
      "iommuGroup": 1,
      "container": {
        "mode": "dri",
        "selectable": true,
        "note": ""
      },
      "vm": {
        "driverVfio": false,
        "domain": ""
      },
      "holders": {
        "containers": ["plex"],
        "vm": ""
      }
    }
  ]
}
```

`container.mode` is `dri` (render and card nodes), `nvidia` (toolkit device request), or `none` (not selectable). `memoryKind` is `dedicated`, `shared`, or `none`. Codec fields are left off until that slice. `vm.domain` is set when any defined domain lists the address, including a stopped VM. `holders.containers` is which containers currently have the id. The Settings "assigned to" cell is the VM name when `vm.domain` is set, otherwise the container names.

`POST /api/v1/gpus/rescan` rebuilds the inventory and returns the same body. The udev path calls the same rebuild. Toolkit install is `POST /api/v1/gpus/nvidia-toolkit` on Debian, refused when no NVIDIA PCI device is present, and refused on FELDCORE OS.

Container spec, the part that changes:

```json
{
  "gpu": true,
  "gpus": ["0000:00:02.0"]
}
```

`gpu` remains a boolean mirror of `len(gpus) > 0` so older clients still parse the Docker list. The first slice allows at most one id.

Server checks on create and on update, before any Docker call:

| Request | Result |
| --- | --- |
| `"gpus": []` | Container with no GPU device and no NVIDIA device request. |
| More than one id | 400, "Choose one GPU." |
| Id not in the inventory | 400, "GPU 0000:01:00.0 is not on this server." |
| `selectable` false because the driver is missing or the toolkit is missing | 400, the same note the row shows. |
| Address is on a defined VM, or the driver is `vfio-pci` | 400, "GPU 0000:01:00.0 is assigned to virtual machine windows." |
| Id present and selectable | Resolve by-path, add the device mappings or the single NVIDIA device request, add group ids, write the labels. |

Template preview returns `gpus: []` plus a note in the same note list that already explains PUID 99, when a GPU device was stripped. The note is the one in the prefill section.

Usage, when the stats slice exists, is a separate poll or a field on the system stream. It does not block the inventory. Suggested shape per id: `gpuPercent`, `vramUsedBytes`, `encoderPercent`, `encoderSessions` (NVIDIA only; omit otherwise).

## 8. Phases

### First slice (this is the MVP)

On Debian, with the current Docker form:

1. `internal/gpu` inventory from PCI class, DRM, by-path, and `pci.ids`, rebuilt on every read, with the NVIDIA UUID when `nvidia-smi` runs. Rescan button and a udev refresh.
2. Read-only Settings → GPUs: model, vendor, driver, VRAM, assigned to.
3. Container form starts deselected. Zero or one GPU. Resolve to by-path at create, pass render and card nodes, keep group ids. One NVIDIA `DeviceRequests` entry when the toolkit is already working. CDI when the engine supports it.
4. Settings button to download and install `nvidia-container-toolkit` when any NVIDIA PCI device is present, with the Docker-restart warning. No driver install.
5. Template prefill strips `/dev/dri` and `--gpus`, leaves the picker empty, and still creates the container. **No GPU found** when the inventory is empty.
6. A GPU whose address is on any defined VM, or whose driver is `vfio-pci`, is shown as assigned to that VM and is not selectable. The boot stub itself is work for `cursor/vm-manager-0192`.

### Next

- Dashboard card: AMD sysfs busy and VRAM, `nvidia-smi` utilisation and encoder sessions, Intel video-engine busy when `intel_gpu_top` is present.
- Codec column, including the i965 retry for HD 4000-class Intel. Do not apt-install those tools for the column.
- More than one GPU on a container, still without mixing an Intel iGPU and an NVIDIA card until that is explicitly wanted.
- ROCm switch: `/dev/kfd` plus `ROCR_VISIBLE_DEVICES` for the selected AMD GPU.
- Raspberry Pi and other ARM boards, including V4L2 codec nodes.
- WSL `/dev/dxg` labelled, still without an automated WSL CUDA path.
- On the VM branch: write the boot-time `vfio-pci` stub for addresses stored on a domain, and refuse an assignment that running containers already hold.

### With FELDCORE OS

- NVIDIA sysext matched to the booted kernel, and firmware in the OS image.
- The toolkit apt button hidden. The mismatch note shown.
- Stats helpers that are not in the kernel sysfs go into the image or a small tools sysext, not apt.

## 9. Risks

- **Apps that open every node in `/dev/dri`.** Passing the chosen render node and card node covers Jellyfin, Plex, Frigate, and Immich. An app that needs the whole directory can still add a Device row for `/dev/dri`. The picker does not also pass the directory, or a second card would slip in.
- **Apps hard-coded to `renderD128`.** Mounting the real render number preserves what the kernel assigned. A container given a card that is currently `renderD129` sees that name.
- **Toolkit install restarts Docker.** The confirm step has to say that containers with a restart policy will stop and start. Skip the offer when the toolkit is already configured.
- **`pci.ids` missing.** Show `Intel 8086:0166`. Detection still succeeds.
- **Stub lags the assignment.** The picker locks a GPU as soon as a domain lists it. The device node stays on the host driver until the reboot that applies `vfio-pci`. A container Docker restarts by itself can still open that node until then. New creates go through FELDCORE and are refused.
- **Device access is root-equivalent on the host GPU.** The API is already behind the administrator session. The server resolves ids to `/dev/dri/by-path/…` or a toolkit UUID. It does not pass a client-supplied path through the GPU field.
- **Two inventories.** If `internal/vm` grows its own GPU names, the Settings list and the VM form will disagree. Display GPUs should be read from `internal/gpu`. The VM branch keeps the assignment in domain XML.

## 10. Open questions

1. For the 2012 Mac mini HD 4000, passing the render node is enough for containers. When the codec column is built, is probing with the i965 driver if it happens to be installed enough? Installing `i965-va-driver` on the host would only fill that column. App containers ship their own driver.
2. Pass `/dev/kfd` only when the template mapped it, or with every selected AMD GPU once the ROCm switch exists? The first slice does not pass it unless the template had that device and the user then selects an AMD GPU. Confirm that is right.
