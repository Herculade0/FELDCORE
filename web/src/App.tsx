import { useState } from "react";
import {
  LayoutDashboard,
  FolderTree,
  Container,
  MonitorPlay,
  Settings as SettingsIcon,
} from "lucide-react";
import Dashboard from "./components/Dashboard";

type NavKey = "dashboard" | "shares" | "docker" | "vms" | "settings";

const NAV: { key: NavKey; label: string; icon: typeof LayoutDashboard }[] = [
  { key: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { key: "shares", label: "Shares", icon: FolderTree },
  { key: "docker", label: "Docker", icon: Container },
  { key: "vms", label: "VMs", icon: MonitorPlay },
  { key: "settings", label: "Settings", icon: SettingsIcon },
];

export default function App() {
  const [active, setActive] = useState<NavKey>("dashboard");

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-60 flex-col border-r border-white/5 bg-[#0d131c]">
        <div className="flex items-center gap-2 px-5 py-5">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-emerald-500/20 text-emerald-300">
            <span className="text-sm font-bold">F</span>
          </div>
          <span className="text-sm font-semibold tracking-wide text-slate-100">
            FELDCORE
          </span>
        </div>
        <nav className="mt-2 flex-1 space-y-1 px-3">
          {NAV.map(({ key, label, icon: Icon }) => (
            <button
              key={key}
              onClick={() => setActive(key)}
              className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors ${
                active === key
                  ? "bg-white/10 text-slate-100"
                  : "text-slate-400 hover:bg-white/5 hover:text-slate-200"
              }`}
            >
              <Icon size={18} />
              {label}
            </button>
          ))}
        </nav>
        <div className="px-5 py-4 text-xs text-slate-600">v0.1.0 · pre-alpha</div>
      </aside>

      <main className="flex-1 overflow-auto">
        <div className="mx-auto max-w-6xl px-6 py-6">
          {active === "dashboard" ? (
            <Dashboard />
          ) : (
            <ComingSoon nav={active} />
          )}
        </div>
      </main>
    </div>
  );
}

const ROADMAP: Record<Exclude<NavKey, "dashboard">, { title: string; body: string }> = {
  shares: {
    title: "Shares",
    body: "Create a share (a managed directory), then expose it over SMB or NFS with a toggle. FELDCORE writes the Samba/NFS config and reloads the service.",
  },
  docker: {
    title: "Docker",
    body: "Manage containers and install apps from a store backed by the Unraid Community Applications template catalog.",
  },
  vms: {
    title: "Virtual Machines",
    body: "Create and control KVM/libvirt virtual machines. Planned for a later phase.",
  },
  settings: {
    title: "Settings",
    body: "Users and authentication, network, service toggles, and appearance.",
  },
};

function ComingSoon({ nav }: { nav: Exclude<NavKey, "dashboard"> }) {
  const r = ROADMAP[nav];
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center text-center">
      <h1 className="text-2xl font-semibold text-slate-100">{r.title}</h1>
      <p className="mt-3 max-w-md text-sm text-slate-400">{r.body}</p>
      <span className="mt-5 rounded-full bg-white/5 px-3 py-1 text-xs text-slate-400">
        Coming soon
      </span>
    </div>
  );
}
