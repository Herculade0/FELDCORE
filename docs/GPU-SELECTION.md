# GPU selection

Design only. This describes how FELDCORE should find GPUs, let a container use a chosen one, and stay consistent with VM passthrough. It does not change the daemon or the UI.

The VM manager (branch `cursor/vm-manager-0192`, in progress) owns virtual machines: domain XML, the PCI / IOMMU form, and libvirt. This document owns containers, the shared inventory those screens both read, and the rule for when a GPU cannot be in both places at once.

## Recommendation

Ship a container picker first, on Debian, before dashboard graphs or a driver installer.

The first slice:

- Build one inventory keyed by PCI address, in the same form the VM spec already uses (`0000:01:00.0`).
- Show model, vendor, driver, VRAM when the kernel reports it, the render node, and codecs when `vainfo` or `nvidia-smi` is already installed.
- Replace the single "Use the GPU" switch with a picker. One usable GPU can stay a single switch. Two or more are checkboxes.
- When an App Store template contains `/dev/dri` or `--gpus`, fill the picker from it and drop the hard-coded device.
- When the template asks for a GPU the host does not have, leave the picker unticked, show **No GPU found**, and let Apply create the container without a GPU.
- Remember the PCI address on the container. Resolve the render node again at create time, so a renumbered `renderD128` still finds the same card.
- Show a GPU bound to `vfio-pci`, or held by a running VM, in the list and refuse it for containers.

Leave these for later: live utilisation on the dashboard, a ROCm switch, Raspberry Pi codec nodes, and the NVIDIA driver as a systemd-sysext on FELDCORE OS.

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

The VM branch is not duplicated here. Its choices that GPU selection must follow:

- A VM is KVM through `virsh` and hand-written domain XML. FELDCORE does not bind `vfio-pci` itself.
- The display GPU is one PCI address. Extra PCI devices are a separate list. The address form is four hex digits, colon, two, colon, two, dot, function `0`–`7`, stored lowercase: `0000:01:00.0`.
- Passthrough XML is a managed PCI hostdev (`managed='yes'`). Libvirt detaches the host driver when the VM starts and puts it back when the VM stops.
- Graphics are VNC or the physical GPU, not both as the console.
- IOMMU groups are read from `/sys/kernel/iommu_groups` for the VM form. A group that contains more than the GPU has to be passed together. That constraint belongs on the VM form.

Container selection uses the same address for the display function and does not grow a second copy of the IOMMU UI.

HDMI audio on the same card (`0000:01:00.1`, PCI class `0x0403`) is not a GPU. The container list omits it. The VM form can still pass it as an extra PCI device because it sits in the GPU's IOMMU group.

Suggested split of code, when this is built:

| Piece | Owner |
| --- | --- |
| PCI display inventory, render nodes, driver, VRAM, codecs | New `internal/gpu`, called from Docker |
| IOMMU groups, USB, non-GPU PCI, domain XML | `internal/vm` on the VM branch |
| "Which running containers hold this address?" | `GET /api/v1/gpus`, from the `feldcore.gpu.ids` label |
| "Which running VM holds this address?" | VM branch, read by the same endpoint when `virsh` is present |

Until `virsh` exists, the container side still treats driver `vfio-pci` as unavailable. That covers a GPU libvirt has already detached.

## 1. Detection

Start from PCI display devices, then attach DRM nodes, then ask vendor tools for the fields sysfs does not have. A card with no DRM node must still appear. Today's scan misses it.

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
- Platform GPU with no PCI address (Raspberry Pi V3D, many Mali parts): the `/dev/dri/by-path` filename with `-render` or `-card` removed, for example `platform-fec00000.v3d`. These never appear in the VM PCI picker.
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
| Codecs | See below. |
| Primary display | Sysfs `boot_vga` is `1`. The UI says this GPU is driving the display. |
| IOMMU group | The group number, for the link to the VM form. The container picker does not ask the user to pass the whole group. |

### Codecs

Probe per render node, cache the result, and keep going when the tool is missing. An empty codec list with the note "Codec list unavailable" is a complete answer.

**Intel Quick Sync.** On Linux this is VA-API. There is no extra device node. A container that uses Quick Sync opens the render node. The codec list is what `vainfo` reports.

- Broadwell and newer, including Arc, use the iHD driver (`intel-media-va-driver` inside the app's image). The kernel driver is `i915`, or `xe` for Arc on kernel 6.8 and newer.
- The 2012 Mac mini's HD Graphics 4000 (Ivy Bridge, PCI `8086:0166`) and the other HD 2000–HD 5000 parts use the older i965 VA-API driver. iHD does not support them. If `vainfo` fails on one of these, retry with `LIBVA_DRIVER_NAME=i965`. Expect H.264 encode and MPEG-2 decode. HEVC is not on this chip. Memory is shared. The card is usually the console (`boot_vga`). Containers are the useful path. Passing it through to a VM takes the display away.

**AMD AMF.** AMF is not a device node on Linux. Containers encode through VA-API on the same render node. The codec list comes from `vainfo` (Mesa). `rocm-smi` is only a nicer name and a compute id.

**NVIDIA NVENC / NVDEC.** These exist when the proprietary `nvidia` driver is loaded. They are not a node under `/dev/dri`. `nvidia-smi -q` reports encoder capability and active sessions. `nouveau` exposes a render node and does not expose NVENC. The row should say that hardware encode needs the NVIDIA driver.

**virtio-gpu and WSL.** Vendor `0x1af4` or driver `virtio-gpu`: label it "Virtual GPU". Pass the render node when one exists. Hardware encode is not available. WSL also exposes `/dev/dxg`. Show that as a virtual GPU. NVIDIA GPU-PV inside WSL can make `nvidia-smi` work via the Windows driver. Show the card and leave the WSL toolkit path alone.

**ARM boards.** Driver `v3d`, `vc4`, `panfrost`, `panthor`, or a vendor Mali module. Use the platform id from by-path. Pass the render node the same way as Intel and AMD. Raspberry Pi encode is usually a V4L2 node (`/dev/video10`, `/dev/video11`, and similar), not VA-API. The first slice can name those nodes in the note and does not pass them yet.

### Driver not loaded

The PCI device is present, `driver` is empty, and there is no render node. Typical cases: Intel stuck on the firmware framebuffer (`efifb`) because `i915` never bound, NVIDIA with no driver installed, AMD firmware missing so `amdgpu` gave up. The row is visible and not selectable. The note is "Driver not loaded." For the framebuffer case, add "The display is on a firmware framebuffer, so containers cannot use this GPU."

FELDCORE does not load or unbind kernel modules from the container form.

## 2. Assigning a GPU to a container

The client sends PCI addresses (or a platform id). The server resolves device paths. The GPU field never accepts a raw path. Arbitrary devices stay on the existing Device row, which already requires a `/dev/` path.

### Intel and AMD

Pass the chosen card's render node and card node, using the by-path paths as `PathOnHost` so Docker opens the current node when the container starts:

```text
/dev/dri/by-path/pci-0000:00:02.0-render  →  /dev/dri/renderD128
/dev/dri/by-path/pci-0000:00:02.0-card    →  /dev/dri/card0
```

Inside the container, keep the host's current `renderD*` and `card*` names, and mount the by-path symlink at the same path. Apps that scan `/dev/dri` then see the nodes they expect. A second GPU in the same container keeps its own render number (`renderD129`).

Permissions stay as they are now. Render nodes are group `render`, card nodes are group `video`, mode `crw-rw----`. The container gets those numeric group ids via `GroupAdd`, because the names `render` and `video` may not exist in the image, and the device cgroup permission is `rwm`. If a node is `root:root` mode `600`, group add cannot help. Show "This GPU's device node is not open to the render or video group" and leave the host permissions unchanged.

`/dev/kfd` is the AMD compute node. It is one node for every AMD GPU. The first slice passes it when the template already maps `/dev/kfd`, together with the selected render node. Choosing which AMD GPU a ROCm process sees is the env var `ROCR_VISIBLE_DEVICES` (a render index or the id `rocm-smi` prints), added in the later ROCm switch. VA-API transcoding does not need `/dev/kfd`.

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

`video` is NVENC/NVDEC. `compute` and `utility` are CUDA and `nvidia-smi`. Add `graphics` only when the template or the user asks for OpenGL. An explicit list of UUIDs selects the cards. `Count: -1` with an empty `DeviceIDs` means every NVIDIA GPU, which is today's `--gpus all`.

Docker 25 and newer can take the same selection as a CDI device (`nvidia.com/gpu=GPU-uuid`). Prefer CDI when `nvidia-ctk` has generated the spec and the engine accepts it. Fall back to `DeviceRequests` on older Docker. Both come from the toolkit. FELDCORE does not mount `libcuda.so` itself.

If the driver is `nvidia` and the toolkit is missing, the row is detected and not selectable. The note is "NVIDIA Container Toolkit is not installed."

On Debian, offer to install the `nvidia-container-toolkit` package the same way the UI already offers Docker: only when a proprietary NVIDIA driver is already loaded, and only after the dialog says Docker will restart and containers with a restart policy will come back. The toolkit's `nvidia-ctk runtime configure` rewrites the Docker daemon config. That restart is the sharp edge. Do not install the NVIDIA kernel driver from this button. Driver install needs a reboot, matching kernel headers, and on FELDCORE OS it is a sysext (section 5).

`nouveau` stays a DRM render-node GPU. The note explains that NVENC needs the NVIDIA driver. Do not install the proprietary driver over `nouveau` from the picker.

### What Unraid does

Unraid splits discovery, stats, and assignment across plugins. FELDCORE can do all three in one inventory.

| Unraid piece | Behaviour | FELDCORE equivalent |
| --- | --- | --- |
| Nvidia-Driver plugin | Installs a proprietary driver built for Unraid's kernel. The template editor then shows `NVIDIA_VISIBLE_DEVICES` as a dropdown of UUIDs from `nvidia-smi -L`, plus `NVIDIA_DRIVER_CAPABILITIES` (often `all`). | Toolkit device request, capabilities `compute,utility,video` by default. The driver itself is a host or OS-image concern. |
| Intel-GPU-TOP plugin | Installs `intel_gpu_top` for statistics. Assignment is still a Device row. Templates ship `/dev/dri`. A second GPU is a hand-edited `/dev/dri/renderD128`. | Picker resolves the PCI address to the render and card nodes. `intel_gpu_top` is only for later stats. |
| GPU Statistics, RadeonTOP | Dashboard utilisation. They do not assign devices. | Dashboard card in the later stats slice. AMD busy percent is already in sysfs (`gpu_busy_percent`). |
| VFIO-PCI config | A GPU listed there binds to `vfio-pci` at boot and disappears from Docker even when no VM is running. | Not copied. The VM branch uses libvirt `managed='yes'`, so the host driver stays until a VM that uses the card is started. |

### App Store prefill

`TemplateToSpec` should recognise a GPU request and move it onto the picker, the same way it already lifts ports, paths, and devices out of ExtraParams.

Treat these as a GPU request:

- A Device whose host path is `/dev/dri`, `/dev/dri/renderD*`, `/dev/dri/card*`, or `/dev/dri/by-path/*`.
- A Device `/dev/kfd`.
- ExtraParams `--gpus all`, `--gpus device=…`, or `--runtime=nvidia`.
- Variables `NVIDIA_VISIBLE_DEVICES` and `NVIDIA_DRIVER_CAPABILITIES`.

Remove those rows from the visible Device and Variable lists so the GPU is edited in one place. Leave unrelated devices (`/dev/net/tun`, a Coral USB) alone.

Fill the picker:

| Template asked for | Host has | Picker |
| --- | --- | --- |
| `/dev/dri` | One shareable Intel or AMD GPU | That GPU ticked |
| `/dev/dri` | Several shareable Intel or AMD GPUs | All of them ticked, with the note "This app asked for every GPU under /dev/dri. Untick any it should not use." |
| `/dev/dri/renderD128` or a by-path render symlink | That node exists | The GPU that owns the node |
| `--gpus all` or `NVIDIA_VISIBLE_DEVICES=all` | One or more NVIDIA GPUs the toolkit can use | Those NVIDIA GPUs ticked |
| `NVIDIA_VISIBLE_DEVICES=<uuid>` | That UUID is present | That GPU |
| Any of the above | Nothing that matches | Nothing ticked. Note **No GPU found.** Apply still works. |

Do not swap an NVIDIA request onto an Intel GPU, or the reverse. The user can tick a different vendor themselves when the app can use it.

A ticked set is what gets stored. Applying a template that had `/dev/dri` and two ticked GPUs passes those two GPUs, not the whole directory. That matches today's "pass `/dev/dri`" result for Intel and AMD, and it stays right after a third card appears.

`--gpus` leaves ExtraParams, so it is not applied twice and it no longer shows up as "kept for review but not applied."

## 3. More than one GPU

### Picking one container's GPU

The spec stores a list of stable ids:

```json
"gpus": ["0000:00:02.0"]
```

An empty list means the container has no GPU. The old boolean `gpu` stays as "the list is non-empty" so the Docker table badge keeps working.

Labels on the container:

- `feldcore.gpu=true` when the list is non-empty
- `feldcore.gpu.ids=0000:00:02.0,0000:01:00.0`
- `feldcore.gpu.groups` as today, the gids actually added

Existing containers have `feldcore.gpu=true` and a `/dev/dri` device, from the current switch. On read, when `feldcore.gpu.ids` is absent, treat that as every shareable Intel and AMD GPU currently on the host, and show them ticked. The next save writes the explicit list. A missing id is never replaced with whichever card happened to become `renderD128`.

### Sharing

Containers share a GPU. Jellyfin and Plex can both hold the same render node. The kernel allows it, and the UI says "Other containers can use this GPU at the same time." There is no "only this container" lock in the first slice. A lock would be FELDCORE policy on top of the kernel, and it can wait until someone asks.

A VM does not share. While a domain is running with that PCI address in its hostdev set, the container picker shows the GPU and does not allow a new tick. The note names the VM: "In use by virtual machine windows." Create and start of a container that already has the id stored are refused with the same sentence, before Docker is called.

The other direction belongs to the VM branch, and this is the contract to implement there: before a VM starts, read `feldcore.gpu.ids` on running containers. If one holds the address, refuse to start and name the containers. Do not stop them automatically. A stopped VM that merely lists the GPU in its XML does not reserve it, because `managed='yes'` binds `vfio-pci` only while the VM runs.

A GPU whose driver is already `vfio-pci` (the VM is running, or the card was left unbound) is unavailable to containers even when `virsh` cannot be queried. The note is "Passed through to virtual machines. The host driver is not loaded."

### Mixing vendors, and iGPU plus dGPU

One container may receive an Intel render node and an NVIDIA device request together. The host config is the union: device mappings for the DRM nodes, plus one NVIDIA `DeviceRequests` entry for the UUIDs. This is how some media apps use an iGPU for one job and a discrete card for another.

iGPU and dGPU are two rows. Nothing is preferred automatically. A template that said `/dev/dri` ticks each shareable Intel and AMD GPU and leaves NVIDIA to the `--gpus` line.

The primary GPU (`boot_vga`) can still be shared with containers. The row says it is driving the display, so a later VM passthrough of that card is an obvious console loss. The container picker does not block it.

## 4. A GPU that is missing or has moved

| Situation | What the user sees | What Apply does |
| --- | --- | --- |
| Template wants a GPU, host has none, or none of the requested vendor | Picker unticked. **No GPU found.** | Creates the container with an empty GPU list. Docker is not asked for a device. |
| Template names `renderD128` or a UUID that is not on this host | Same note, naming what the template asked for | Same. No substitute card. |
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
- Loading, blacklisting, or unbinding kernel modules from the UI
- Looking Glass, GPU reset quirks, and the NVIDIA guest "error 43" workaround (VM document, if they are done at all)
- Apple Silicon. "Apple-era" here means an Intel Mac running Debian, such as the 2012 Mac mini's HD 4000

## 6. Screens

No new top-level nav item. GPUs are a Settings section. The container form links to it ("GPUs"). The dashboard gains a card in the stats slice, and that card links to the same section.

### Settings → GPUs

```text
GPUs
The graphics devices on this server. Containers share a GPU.
A virtual machine takes it over while that machine is running.

+------------------------------------------------------------------+
| Intel HD Graphics 4000                          Driving display  |
| Intel  ·  i915  ·  0000:00:02.0  ·  shared memory                |
| Render  /dev/dri/renderD128                                      |
| Encode  H.264          Decode  H.264, MPEG-2                     |
| Containers  plex                                                 |
+------------------------------------------------------------------+
| NVIDIA GeForce RTX 3060                         12 GB            |
| NVIDIA  ·  nvidia 535.183  ·  0000:01:00.0                       |
| UUID  GPU-1a2b…          Render  (none, nvidia-drm modeset off)  |
| Encode  H.264, HEVC, AV1                                        |
| NVIDIA Container Toolkit is not installed.          [ Install ]  |
| Install restarts Docker. Containers set to restart come back.   |
+------------------------------------------------------------------+
| AMD Radeon RX 6800                              16 GB            |
| Passed through to virtual machines. The host driver is not       |
| loaded (vfio-pci). In use by virtual machine windows.            |
| 0000:02:00.0    IOMMU group 12                                   |
+------------------------------------------------------------------+
| (empty host)                                                     |
| No GPU found on this server.                                     |
+------------------------------------------------------------------+
```

The Install action is Debian-only and only for the toolkit. FELDCORE OS shows the sysext mismatch note from section 5 and has no install button.

### Container form

One usable GPU, same shape as today's switch:

```text
+------------------------------------------------------------------+
| Use the GPU                                                      |
| Intel HD Graphics 4000 (0000:00:02.0). Other containers can use  |
| it at the same time. Encode: H.264.                         [on] |
+------------------------------------------------------------------+
```

Several GPUs:

```text
+------------------------------------------------------------------+
| GPUs                                                             |
| This app asked for every GPU under /dev/dri. Untick any it      |
| should not use.                                                  |
|                                                                  |
| [x] Intel HD Graphics 4000   0000:00:02.0   i915    shared       |
| [x] AMD Radeon RX 6800       0000:02:00.0   amdgpu  16 GB        |
| [ ] NVIDIA GeForce RTX 3060  0000:01:00.0   toolkit not installed|
| [ ] Intel Arc A380           0000:03:00.0   in use by VM "win"   |
+------------------------------------------------------------------+
```

Template on a host with no match:

```text
+------------------------------------------------------------------+
| GPUs                                                             |
| No GPU found. The template asked for /dev/dri. This container    |
| will start without a GPU.                                        |
+------------------------------------------------------------------+
```

There is nothing to tick, and Apply is enabled.

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
      "codecs": {
        "source": "vainfo",
        "decode": ["h264", "mpeg2"],
        "encode": ["h264"]
      },
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

`container.mode` is `dri` (render and card nodes), `nvidia` (toolkit device request), or `none` (not selectable). `memoryKind` is `dedicated`, `shared`, or `none`. `codecs.source` is `vainfo`, `nvidia-smi`, or `none`.

Container spec, the part that changes:

```json
{
  "gpu": true,
  "gpus": ["0000:00:02.0", "0000:01:00.0"]
}
```

`gpu` remains a boolean mirror of `len(gpus) > 0` so older clients still parse the Docker list.

Server checks on create and on update, before any Docker call:

| Request | Result |
| --- | --- |
| `"gpus": []` | Container with no GPU device and no NVIDIA device request. |
| Id not in the inventory | 400, "GPU 0000:01:00.0 is not on this server." |
| `selectable` false because the driver is missing or the toolkit is missing | 400, the same note the row shows. |
| Running VM holds the id, or driver is `vfio-pci` | 400, "GPU 0000:01:00.0 is in use by virtual machine windows." |
| Id present and selectable | Resolve by-path, add the device mappings or the NVIDIA device request, add group ids, write the labels. |

Template preview (`POST` the existing app-spec route) returns the rewritten spec plus a note in the same note list that already explains PUID 99. The note text is the one in the prefill table.

Usage, when the stats slice exists, is a separate poll or a field on the system stream. It does not block the inventory. Suggested shape per id: `gpuPercent`, `vramUsedBytes`, `encoderPercent`, `encoderSessions` (NVIDIA only; omit otherwise).

## 8. Phases

### First slice (this is the MVP)

On Debian, with the current Docker form:

1. `internal/gpu` inventory from PCI class, DRM, by-path, and `pci.ids`, with the NVIDIA UUID when `nvidia-smi` runs.
2. Codecs when `vainfo` or `nvidia-smi` is already on the host, including the i965 retry for HD 4000-class Intel. Do not apt-install those tools.
3. Spec list `gpus`, labels above, resolve to by-path at create, pass render and card nodes, keep group ids.
4. NVIDIA selection via `DeviceRequests` when the toolkit is already working. CDI when the engine supports it.
5. Debian-only offer to install `nvidia-container-toolkit` when the `nvidia` driver is loaded, with the Docker-restart warning.
6. Template prefill rules in section 2, including **No GPU found** and a successful create.
7. Read-only Settings → GPUs section with the rows in section 6, minus live percentages.
8. `vfio-pci` and, when `virsh` is installed, a running domain, shown as unavailable.

### Next

- Dashboard card: AMD sysfs busy and VRAM, `nvidia-smi` utilisation and encoder sessions, Intel video-engine busy when `intel_gpu_top` is present.
- ROCm switch: `/dev/kfd` plus `ROCR_VISIBLE_DEVICES` for the ticked AMD GPU.
- Pass Raspberry Pi / V4L2 codec nodes with the platform GPU.
- WSL `/dev/dxg` labelled, still without an automated WSL CUDA path.
- VM start checks `feldcore.gpu.ids` (implemented on the VM branch, against this label).

### With FELDCORE OS

- NVIDIA sysext matched to the booted kernel, and firmware in the OS image.
- The toolkit apt button hidden. The mismatch note shown.
- Stats helpers that are not in the kernel sysfs go into the image or a small tools sysext, not apt.

## 9. Risks

- **Apps that open every node in `/dev/dri`.** Passing two specific nodes covers Jellyfin, Plex, Frigate, and Immich. An app that needs the directory itself can still add a Device row for `/dev/dri`. The picker does not also pass the whole directory, or a third card would slip in.
- **Apps hard-coded to `renderD128`.** Mounting the real render number preserves what the kernel assigned. A container given only the second GPU sees `renderD129`. The row shows that name so the failure is explainable.
- **Toolkit install restarts Docker.** The confirm step has to say that containers with a restart policy will stop and start. Skip the offer when the toolkit is already configured.
- **`pci.ids` or `vainfo` missing.** Show `Intel 8086:0166` and an empty codec list. Detection still succeeds.
- **iHD on HD 4000.** A failed default `vainfo` would look like "this GPU cannot encode." The i965 retry is part of the first slice for that reason.
- **Race with VM start.** Both sides check at the moment of start. A container and a VM started together can still collide once. Libvirt then loses the device node, and the container loses the GPU on its next start. That window is acceptable for the first slice. A permanent reservation table can wait.
- **Device access is root-equivalent on the host GPU.** The API is already behind the administrator session. The server resolves ids to `/dev/dri/by-path/…` or a toolkit UUID. It does not pass a client-supplied path through the GPU field.
- **Two inventories.** If `internal/vm` grows its own PCI scan, the Settings list and the VM form will disagree about names and about HDMI audio. Display GPUs should be read from `internal/gpu` only.

## 10. Open questions

1. A template that says `/dev/dri` on a host with two shareable GPUs ticks both, so Apply matches today's "pass the whole directory." Should the form instead wait, with nothing ticked, until the user chooses one?
2. On Debian, should the NVIDIA kernel driver stay a manual install, with FELDCORE only offering the container toolkit? The OS image path is a sysext either way.
3. A stopped VM lists a GPU in its XML and does not reserve it, because libvirt binds `vfio-pci` only while that VM is running. Unraid does the opposite and stubs the card at boot. Is the libvirt behaviour what you want for FELDCORE?
4. May one container tick an Intel iGPU and an NVIDIA card together in the first slice, or is the picker single-select until someone needs both?
5. For the 2012 Mac mini HD 4000, is "pass the render node, and probe codecs with the i965 driver if it is installed" enough? Installing `i965-va-driver` on the host would only fill the codec column. App containers ship their own driver.
6. Is the read-only Settings list part of the first slice, or is the container picker the only screen until stats exist?
7. Pass `/dev/kfd` only when the template mapped it (the recommendation), or with every AMD GPU automatically?
8. Are Raspberry Pi and other ARM boards in the first slice as "render node, platform id, codec note," or later with the V4L2 nodes?
