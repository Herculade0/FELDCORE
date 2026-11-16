export interface VM {
  name: string;
  state: string;
  cpus: number;
  memoryMB: number;
  maxMemoryMB: number;
  autostart: boolean;
}

export interface Disk {
  path?: string;
  sizeGB: number;
  format: string;
  bus: string;
}

export interface USB {
  vendor: string;
  product: string;
  name?: string;
}

export interface CloudInit {
  enabled: boolean;
  user?: string;
  password?: string;
  sshKey?: string;
  baseImage?: string;
}

export interface Spec {
  name: string;
  description?: string;
  os?: string;
  cpus: number[];
  memoryMB: number;
  maxMemoryMB: number;
  machine: string;
  bios: string;
  tpm: boolean;
  disks: Disk[];
  installISO?: string;
  virtioISO?: string;
  bootISO: boolean;
  graphics: string;
  video?: string;
  gpu?: string;
  sound?: string;
  nic?: string;
  network: string;
  bridge?: string;
  usbs?: USB[];
  pcis?: string[];
  shares?: string[];
  clock?: string;
  hyperv: boolean;
  autostart: boolean;
  cloudInit: CloudInit;
}

export interface Core {
  id: number;
  core: number;
  thread: number;
  sibling: number;
}

export interface PCIDevice {
  address: string;
  vendor: string;
  device: string;
  class: string;
  classID: string;
  name: string;
  group: number;
}

export interface IOMMUGroup {
  id: number;
  devices: PCIDevice[];
}

export interface USBInfo {
  vendor: string;
  product: string;
  name: string;
}

export interface Media {
  name: string;
  size: number;
  virtio?: boolean;
}

export interface ShareInfo {
  name: string;
  path: string;
}

export interface Host {
  kvm: boolean;
  kvmMessage?: string;
  virsh: boolean;
  libvirt: boolean;
  qemuImg: boolean;
  ovmf?: string;
  swtpm: boolean;
  virtiofsd: boolean;
  root: boolean;
  cores: Core[];
  memoryMB: number;
  bridges: string[];
  iommu: IOMMUGroup[];
  iommuMessage?: string;
  usb: USBInfo[];
  isos: Media[];
  images: Media[];
  shares: ShareInfo[];
  domainsPath: string;
  isosPath: string;
  message?: string;
}

export interface Detail {
  vm: VM;
  spec: Spec;
  warnings?: string[];
  xml: string;
}

export interface VirtioRelease {
  channel: string;
  version?: string;
  filename?: string;
  url?: string;
  present?: boolean;
  error?: string;
}

export interface VirtioFile {
  name: string;
  size: number;
  sha256?: string;
}

export interface VirtioView {
  releases?: VirtioRelease[];
  installed?: VirtioFile[];
}

export interface Job {
  id: string;
  label: string;
  done: number;
  total: number;
  percent: number;
  finished: boolean;
  error?: string;
  filename?: string;
}

export interface Snapshot {
  name: string;
  state?: string;
}

export interface VMSettings {
  domainsPath: string;
  isosPath: string;
  domainsDefault: string;
  isosDefault: string;
}

export function emptySpec(host?: Host | null): Spec {
  const spec: Spec = {
    name: "",
    os: "linux",
    cpus: [],
    memoryMB: 2048,
    maxMemoryMB: 4096,
    machine: "q35",
    bios: "ovmf",
    tpm: false,
    disks: [{ sizeGB: 20, format: "qcow2", bus: "virtio" }],
    bootISO: true,
    graphics: "vnc",
    video: "virtio",
    sound: "ich9",
    nic: "virtio",
    network: "nat",
    clock: "utc",
    hyperv: false,
    autostart: false,
    cloudInit: { enabled: false },
    usbs: [],
    pcis: [],
    shares: [],
  };
  const cores = host?.cores ?? [];
  spec.cpus = cores.slice(0, Math.min(2, cores.length)).map((c) => c.id);
  if (host?.bridges?.includes("br0")) {
    spec.network = "bridge";
    spec.bridge = "br0";
  }
  return spec;
}

/** Hardware a given operating system usually wants. The form stays editable. */
export function suggest(os: string, spec: Spec): Spec {
  const next = { ...spec, os, disks: spec.disks.map((d) => ({ ...d })) };
  const disk = next.disks[0];
  if (os === "windows") {
    Object.assign(next, {
      machine: "q35",
      bios: "ovmf",
      tpm: true,
      clock: "localtime",
      hyperv: true,
      nic: "virtio",
      sound: "ich9",
      video: "qxl",
      graphics: "vnc",
      memoryMB: Math.max(spec.memoryMB, 4096),
      maxMemoryMB: Math.max(spec.maxMemoryMB, 8192),
    });
    if (disk && !disk.path) {
      disk.bus = "virtio";
      disk.format = "qcow2";
      disk.sizeGB = Math.max(disk.sizeGB, 64);
    }
  } else if (os === "windows-legacy") {
    Object.assign(next, {
      machine: "i440fx",
      bios: "seabios",
      tpm: false,
      clock: "localtime",
      hyperv: false,
      nic: "e1000",
      sound: "sb16",
      video: "cirrus",
      graphics: "vnc",
    });
    if (disk && !disk.path) {
      disk.bus = "ide";
      disk.format = "qcow2";
    }
  } else if (os === "linux") {
    Object.assign(next, {
      machine: "q35",
      bios: "ovmf",
      tpm: false,
      clock: "utc",
      hyperv: false,
      nic: "virtio",
      sound: "ich9",
      video: "virtio",
      graphics: "vnc",
    });
    if (disk && !disk.path) disk.bus = "virtio";
  } else {
    Object.assign(next, {
      machine: "q35",
      bios: "seabios",
      tpm: false,
      clock: "utc",
      hyperv: false,
      video: "qxl",
      graphics: "vnc",
    });
  }
  return next;
}
