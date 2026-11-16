import {
  Container,
  FolderTree,
  Layers,
  LayoutDashboard,
  MonitorPlay,
  ScrollText,
  Settings,
  Store,
  type LucideIcon,
} from "lucide-react";

export type NavKey = "dashboard" | "shares" | "docker" | "stacks" | "apps" | "vms" | "audit" | "settings";

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
];
