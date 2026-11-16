// Mirrors the JSON emitted by internal/system (Go).

export interface Info {
  hostname: string;
  os: string;
  platform: string;
  platformVersion: string;
  kernel: string;
  arch: string;
  cpuModel: string;
  cpuCores: number;
  bootTime: number;
}

export interface DiskUsage {
  mountpoint: string;
  device: string;
  fstype: string;
  totalBytes: number;
  usedBytes: number;
  usedPercent: number;
}

export interface NetRate {
  name: string;
  rxPerSec: number;
  txPerSec: number;
}

export interface Stats {
  timestamp: number;
  uptimeSeconds: number;
  cpuPercent: number;
  perCore: number[];
  load1: number;
  load5: number;
  load15: number;
  memTotal: number;
  memUsed: number;
  memPercent: number;
  swapTotal: number;
  swapUsed: number;
  disks: DiskUsage[];
  net: NetRate[];
}
