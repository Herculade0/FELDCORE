import {
  Archive,
  CircleHelp,
  Container,
  FolderTree,
  HardDrive,
  HeartPulse,
  Layers,
  LayoutDashboard,
  LayoutGrid,
  MonitorPlay,
  ScrollText,
  Settings,
  Store,
  type LucideIcon,
} from "lucide-react";

export type NavKey =
  | "dashboard"
  | "launcher"
  | "storage"
  | "shares"
  | "docker"
  | "stacks"
  | "apps"
  | "vms"
  | "backups"
  | "health"
  | "audit"
  | "settings"
  | "help";

export interface NavItem {
  key: NavKey;
  label: string;
  desc: string;
  icon: LucideIcon;
  /** False while the section is still a roadmap placeholder. */
  ready: boolean;
}

export const NAV: NavItem[] = [
  {
    key: "dashboard",
    label: "Dashboard",
    desc: "Your server at a glance",
    icon: LayoutDashboard,
    ready: true,
  },
  {
    key: "launcher",
    label: "Launcher",
    desc: "Apps you can open",
    icon: LayoutGrid,
    ready: true,
  },
  {
    key: "storage",
    label: "Storage",
    desc: "Disks and pools",
    icon: HardDrive,
    ready: true,
  },
  {
    key: "shares",
    label: "Shares",
    desc: "SMB & NFS folders",
    icon: FolderTree,
    ready: true,
  },
  {
    key: "docker",
    label: "Docker",
    desc: "Containers",
    icon: Container,
    ready: true,
  },
  {
    key: "stacks",
    label: "Stacks",
    desc: "Compose stacks",
    icon: Layers,
    ready: true,
  },
  {
    key: "apps",
    label: "App Store",
    desc: "Community applications",
    icon: Store,
    ready: true,
  },
  {
    key: "vms",
    label: "VMs",
    desc: "Virtual machines",
    icon: MonitorPlay,
    ready: true,
  },
  {
    key: "backups",
    label: "Backups",
    desc: "Copies and restore",
    icon: Archive,
    ready: true,
  },
  {
    key: "health",
    label: "Health",
    desc: "Disks, heat, and services",
    icon: HeartPulse,
    ready: true,
  },
  {
    key: "audit",
    label: "Audit",
    desc: "Agent calls",
    icon: ScrollText,
    ready: true,
  },
  {
    key: "settings",
    label: "Settings",
    desc: "Appearance & system",
    icon: Settings,
    ready: true,
  },
  {
    key: "help",
    label: "Help",
    desc: "Guides and reference",
    icon: CircleHelp,
    ready: true,
  },
];
