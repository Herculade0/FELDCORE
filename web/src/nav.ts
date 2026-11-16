import {
  Container,
  FolderTree,
  LayoutDashboard,
  MonitorPlay,
  Settings,
  type LucideIcon,
} from "lucide-react";

export type NavKey = "dashboard" | "shares" | "docker" | "vms" | "settings";

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
    key: "shares",
    label: "Shares",
    desc: "SMB & NFS folders",
    icon: FolderTree,
    ready: false,
  },
  {
    key: "docker",
    label: "Docker",
    desc: "Containers & app store",
    icon: Container,
    ready: false,
  },
  {
    key: "vms",
    label: "VMs",
    desc: "Virtual machines",
    icon: MonitorPlay,
    ready: false,
  },
  {
    key: "settings",
    label: "Settings",
    desc: "Appearance & system",
    icon: Settings,
    ready: true,
  },
];
