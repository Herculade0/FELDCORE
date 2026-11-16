import { useEffect, useState } from "react";
import {
  Check,
  FolderTree,
  Info as InfoIcon,
  Monitor,
  Moon,
  Palette,
  RefreshCw,
  Shield,
  Sun,
  type LucideIcon,
} from "lucide-react";
import type { Info } from "../types";
import { VERSION } from "../version";
import { api, errorMessage } from "../lib/api";
import { ACCENTS, useTheme, type Mode } from "../lib/theme";
import { shortCpu } from "../lib/format";
import { Card, CardTitle } from "./ui";
import { Alert, Button, SelectField, TextField, Toggle } from "./form";
import AgentAccessCard from "./AgentAccess";
import VPNSettings from "./settings/VPNSettings";
import DriveBackup from "./settings/DriveBackup";
import MailNotifications from "./settings/MailSettings";
import RemoteAccess from "./settings/RemoteAccess";
import WebAddresses from "./settings/WebAddresses";
import SnapshotSettings from "./settings/SnapshotSettings";
import CitizenID from "./settings/CitizenID";

const MODES: { key: Mode; label: string; icon: LucideIcon }[] = [
  { key: "light", label: "Light", icon: Sun },
  { key: "dark", label: "Dark", icon: Moon },
  { key: "system", label: "System", icon: Monitor },
];

interface Svc {
  name: string;
  label: string;
  installed: boolean;
  enabled: boolean;
  running: boolean;
  available: boolean;
}

interface Settings {
  system: { hostname: string; timezone: string; zones?: string[]; ntpServers: string };
  network: { iface: string; ip: string; gateway: string; dns?: string[]; mode: string; backend: string };
  services: Svc[];
  security: {
    firewall: boolean;
    sshPorts?: number[];
    listenPort: number;
    rules?: string[];
    sessionHours: number;
    username: string;
  };
  updates: { unattended: boolean; updateCheck: boolean };
  apps: { feedURL: string; feedDefault: string };
  defaults: { removeImage: boolean };
}

export default function SettingsPage({ onOpenAudit }: { onOpenAudit?: () => void }) {
  const { mode, accent, setMode, setAccent } = useTheme();
  const [info, setInfo] = useState<Info | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [tab, setTab] = useState<"general" | "network" | "remote" | "citizen">("general");

  const reload = () =>
    api<Settings>("/settings")
      .then((s) => {
        setSettings(s);
        setLoadError(null);
      })
      .catch((e) => setLoadError(errorMessage(e)));

  useEffect(() => {
    api<Info>("/system/info")
      .then(setInfo)
      .catch(() => setInfo(null));
    void reload();
  }, []);

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Settings</h1>
        <p className="mt-2 text-sm font-semibold text-ink-2">
          Appearance, this server, names on the network, remote access, Citizen ID, and agent access.
        </p>
      </header>

      <div className="inline-flex flex-wrap rounded-2xl bg-track p-1" role="tablist" aria-label="Settings sections">
        {(
          [
            ["general", "General"],
            ["network", "Network"],
            ["remote", "Remote access"],
            ["citizen", "Citizen ID"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            role="tab"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
            className={`rounded-xl px-4 py-2 text-sm font-bold transition-colors ${
              tab === id ? "accent-gradient text-white shadow-md shadow-accent/30" : "text-ink-2 hover:text-ink"
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {loadError && <Alert>{loadError}</Alert>}

      {tab === "remote" && <RemoteAccess />}

      {tab === "citizen" && <CitizenID />}

      {tab === "network" && settings && (
        <>
          <NetworkCard settings={settings} onSaved={reload} />
          <WebAddresses />
          <VPNSettings />
          <DriveBackup />
          <MailNotifications />
        </>
      )}

      {tab === "general" && (
        <>
      <Card>
        <CardTitle icon={Palette} title="Appearance" hint="Saved in this browser" />
        <div className="mt-6 grid gap-8 lg:grid-cols-2">
          <fieldset>
            <legend className="mb-3 text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">Theme</legend>
            <div className="inline-flex rounded-2xl bg-track p-1">
              {MODES.map(({ key, label, icon: Icon }) => {
                const on = mode === key;
                return (
                  <button
                    key={key}
                    onClick={() => setMode(key)}
                    aria-pressed={on}
                    className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm font-bold transition-colors ${
                      on ? "accent-gradient text-white shadow-md shadow-accent/30" : "text-ink-2 hover:text-ink"
                    }`}
                  >
                    <Icon size={15} />
                    {label}
                  </button>
                );
              })}
            </div>
          </fieldset>
          <fieldset>
            <legend className="mb-3 text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">Accent</legend>
            <div className="flex flex-wrap gap-3">
              {ACCENTS.map((a) => {
                const on = accent === a.key;
                return (
                  <button
                    key={a.key}
                    onClick={() => setAccent(a.key)}
                    aria-pressed={on}
                    className={`flex items-center gap-2.5 rounded-2xl border py-1.5 pl-1.5 pr-3.5 text-sm font-bold transition-colors ${
                      on ? "border-accent text-ink" : "border-line text-ink-2 hover:text-ink"
                    }`}
                  >
                    <span
                      className="flex h-7 w-7 items-center justify-center rounded-xl text-white"
                      style={{ backgroundImage: `linear-gradient(135deg, ${a.from}, ${a.to})` }}
                    >
                      {on && <Check size={15} strokeWidth={3} />}
                    </span>
                    {a.name}
                  </button>
                );
              })}
            </div>
          </fieldset>
        </div>
      </Card>

      {settings && tab === "general" && (
        <>
          <SystemCard settings={settings} onSaved={reload} />
          <ServicesCard services={settings.services} onSaved={reload} />
          <SecurityCard settings={settings} onSaved={reload} />
          <AgentAccessCard onOpenAudit={onOpenAudit} />
          <UpdatesCard settings={settings} onSaved={reload} />
          <AppsCard settings={settings} onSaved={reload} />
          <DefaultsCard settings={settings} onSaved={reload} />
          <SnapshotSettings />
        </>
      )}

      <Card>
        <CardTitle icon={InfoIcon} title="About" />
        <dl className="mt-5 grid gap-x-10 gap-y-3 text-sm sm:grid-cols-2">
          <Item k="FELDCORE" v={`v${VERSION} (pre-alpha)`} />
          <Item k="Hostname" v={info?.hostname ?? "–"} />
          <Item
            k="Operating system"
            v={info ? `${info.platform.charAt(0).toUpperCase()}${info.platform.slice(1)} ${info.platformVersion}` : "–"}
          />
          <Item k="Kernel" v={info?.kernel ?? "–"} />
          <Item k="Processor" v={info?.cpuModel ? shortCpu(info.cpuModel) : "–"} />
          <Item k="Architecture" v={info?.arch ?? "–"} />
        </dl>
      </Card>
        </>
      )}
    </div>
  );
}

function SystemCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [host, setHost] = useState(settings.system.hostname);
  const [tz, setTz] = useState(settings.system.timezone || "UTC");
  const [ntp, setNtp] = useState(settings.system.ntpServers);
  const zones = settings.system.zones?.length ? settings.system.zones : ["UTC"];
  return (
    <Card>
      <div id="settings-system">
        <CardTitle icon={Monitor} title="System" hint="Host name, clock" />
        <div className="mt-5 grid gap-4 sm:grid-cols-2">
          <TextField label="Host name" value={host} onChange={(e) => setHost(e.target.value)} />
          <SelectField label="Time zone" value={tz} onChange={(e) => setTz(e.target.value)}>
            {(zones.includes(tz) ? zones : [tz, ...zones]).map((z) => (
              <option key={z} value={z}>
                {z}
              </option>
            ))}
          </SelectField>
          <TextField
            className="sm:col-span-2"
            label="NTP servers"
            value={ntp}
            onChange={(e) => setNtp(e.target.value)}
            placeholder="pool.ntp.org"
            hint="Optional. Leave blank to keep the system's own NTP servers. systemd-timesyncd is used."
          />
        </div>
        <Save
          onClick={() => api("/settings/system", { method: "PUT", body: { hostname: host, timezone: tz, ntpServers: ntp } })}
          onSaved={onSaved}
        />
      </div>
    </Card>
  );
}

function NetworkCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [mode, setMode] = useState(settings.network.mode || "dhcp");
  const [address, setAddress] = useState("");
  const [gateway, setGateway] = useState(settings.network.gateway || "");
  const [dns, setDns] = useState((settings.network.dns ?? []).join(" "));
  return (
    <Card>
      <div id="settings-network">
        <CardTitle icon={Monitor} title="Network" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2 [overflow-wrap:anywhere]">
          {settings.network.iface
            ? `Using ${settings.network.iface} at ${settings.network.ip || "an unknown address"}.`
            : "No active interface was detected."}{" "}
          DHCP does not move the address. A wrong static address drops this page, and it is not put back on its own.
        </p>
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <SelectField label="Address" value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="dhcp">DHCP</option>
            <option value="static">Static</option>
          </SelectField>
          {mode === "static" && (
            <>
              <TextField label="Address" value={address} onChange={(e) => setAddress(e.target.value)} placeholder="192.168.1.20/24" />
              <TextField label="Gateway" value={gateway} onChange={(e) => setGateway(e.target.value)} />
              <TextField label="DNS" value={dns} onChange={(e) => setDns(e.target.value)} placeholder="1.1.1.1" />
            </>
          )}
        </div>
        <Save
          label={mode === "static" ? "Apply static address" : "Use DHCP"}
          onClick={() =>
            api("/settings/network", {
              method: "PUT",
              body: { mode, iface: settings.network.iface, address, gateway, dns },
            })
          }
          onSaved={onSaved}
        />
      </div>
    </Card>
  );
}

function ServicesCard({ services, onSaved }: { services: Svc[]; onSaved: () => void }) {
  return (
    <Card>
      <div id="settings-services">
        <CardTitle icon={FolderTree} title="Services" hint="Install if missing" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Turning a service on installs it when it is not already installed, then starts it. Turning it off stops it. Packages are not removed.
        </p>
        <div className="mt-4 space-y-3">
          {services.map((svc) => (
            <ServiceRow key={svc.name} svc={svc} onSaved={onSaved} />
          ))}
        </div>
      </div>
    </Card>
  );
}

function ServiceRow({ svc, onSaved }: { svc: Svc; onSaved: () => void }) {
  const [on, setOn] = useState(svc.enabled);
  return (
    <div className={`flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-line bg-surface-2 px-4 py-3 ${svc.available ? "" : "opacity-60"}`}>
      <div className="min-w-0">
        <p className="text-sm font-bold text-ink">{svc.label}</p>
        <p className="text-xs font-medium text-ink-3">
          {svc.available
            ? `${svc.installed ? "Installed" : "Not installed"} · ${svc.running ? "running" : "stopped"}`
            : "This CPU has no virtualisation."}
        </p>
      </div>
      <div className="flex items-center gap-3">
        <Toggle checked={on} onChange={setOn} label={svc.label} disabled={!svc.available} />
        <Save
          label={on ? "Turn on" : "Turn off"}
          disabled={!svc.available || on === svc.enabled}
          onClick={() => api("/settings/services", { method: "PUT", body: { name: svc.name, enabled: on } })}
          onSaved={onSaved}
        />
      </div>
    </div>
  );
}

function SecurityCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [firewall, setFirewall] = useState(settings.security.firewall);
  const [hours, setHours] = useState(String(settings.security.sessionHours || 168));
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const ports = settings.security.sshPorts?.length ? settings.security.sshPorts.join(", ") : "22";
  return (
    <Card>
      <div id="settings-security">
        <CardTitle icon={Shield} title="Security" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          The firewall uses ufw. SSH port {ports} is always allowed, and sshd is never edited or switched off. FELDCORE's port {settings.security.listenPort} is allowed too.
        </p>
        <div className="mt-4 flex items-center gap-3">
          <Toggle checked={firewall} onChange={setFirewall} label="Firewall" />
          <span className="text-sm font-bold text-ink">Firewall {firewall ? "on" : "off"}</span>
        </div>
        <ul className="mt-3 space-y-1 text-xs font-medium text-ink-3">
          {(settings.security.rules ?? []).map((rule) => (
            <li key={rule} className="font-mono [overflow-wrap:anywhere]">
              {rule}
            </li>
          ))}
        </ul>
        <Save
          label="Save firewall"
          onClick={() => api("/settings/firewall", { method: "PUT", body: { enabled: firewall } })}
          onSaved={onSaved}
        />
        <div className="mt-6 grid gap-4 sm:grid-cols-2">
          <TextField label="Current password" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" />
          <TextField label="New password" type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" hint="At least 8 characters." />
        </div>
        <Save
          label="Change password"
          onClick={() => api("/settings/password", { method: "PUT", body: { current, next } })}
          onSaved={() => {
            setCurrent("");
            setNext("");
            onSaved();
          }}
        />
        <p className="mt-3 text-xs font-medium leading-relaxed text-ink-3">
          This is the administrator password. Other people, and their authenticators, are managed under Citizen ID.
        </p>
        <div className="mt-6 max-w-xs">
          <TextField
            label="Session timeout (hours)"
            value={hours}
            onChange={(e) => setHours(e.target.value)}
            inputMode="numeric"
            hint="New sign-ins only. 168 hours is one week."
          />
        </div>
        <Save
          label="Save timeout"
          onClick={() => api("/settings/session", { method: "PUT", body: { hours: Number(hours) } })}
          onSaved={onSaved}
        />
      </div>
    </Card>
  );
}

function UpdatesCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [unattended, setUnattended] = useState(settings.updates.unattended);
  const [check, setCheck] = useState(settings.updates.updateCheck);
  return (
    <Card>
      <div id="settings-updates">
        <CardTitle icon={RefreshCw} title="Updates" />
        <div className="mt-4 space-y-3">
          <Row
            label="Security updates"
            hint="Debian's unattended-upgrades, limited to security updates."
            checked={unattended}
            onChange={setUnattended}
          />
          <Row
            label="Check for FELDCORE updates"
            hint="Looks for a newer release and tells you. It does not install one."
            checked={check}
            onChange={setCheck}
          />
        </div>
        <Save
          onClick={() => api("/settings/updates", { method: "PUT", body: { unattended, updateCheck: check } })}
          onSaved={onSaved}
        />
      </div>
    </Card>
  );
}

function AppsCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [url, setUrl] = useState(settings.apps.feedURL);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  return (
    <Card>
      <div id="settings-apps">
        <CardTitle icon={FolderTree} title="App Store" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Unraid paths under <span className="font-mono text-xs">/mnt/user/appdata</span> always go to the appdata share.
        </p>
        <div className="mt-4">
          <TextField
            label="Catalogue URL"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            hint={`Blank uses ${settings.apps.feedDefault}`}
            spellCheck={false}
          />
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <Save
            label="Save URL"
            onClick={() => api("/settings/feed", { method: "PUT", body: { feedURL: url.trim() } })}
            onSaved={onSaved}
          />
          <Button
            variant="primary"
            loading={busy}
            onClick={() => {
              setBusy(true);
              setError(null);
              setNote(null);
              api("/apps/refresh", { method: "POST" })
                .then(() => setNote("Catalogue refreshed."))
                .catch((e) => setError(errorMessage(e)))
                .finally(() => setBusy(false));
            }}
          >
            Refresh catalogue
          </Button>
        </div>
        {note && <p className="mt-3 text-xs font-semibold text-ok">{note}</p>}
        {error && (
          <div className="mt-3">
            <Alert>{error}</Alert>
          </div>
        )}
      </div>
    </Card>
  );
}

function DefaultsCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [removeImage, setRemoveImage] = useState(settings.defaults.removeImage);
  return (
    <Card>
      <div id="settings-defaults">
        <CardTitle icon={FolderTree} title="Defaults" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Recreate makes sure appdata, system, domains and isos exist. A folder that is already there is adopted. Nothing already in those folders is changed, and SMB and NFS stay off.
        </p>
        <div className="mt-4">
          <Save label="Recreate default shares" onClick={() => api("/settings/shares", { method: "POST" })} onSaved={onSaved} />
        </div>
        <div className="mt-5">
          <Row
            label="Also remove image by default"
            hint="The delete-container dialog starts with this ticked. You can still untick it each time."
            checked={removeImage}
            onChange={setRemoveImage}
          />
        </div>
        <Save
          label="Save preference"
          onClick={() => api("/settings/defaults", { method: "PUT", body: { removeImage } })}
          onSaved={onSaved}
        />
      </div>
    </Card>
  );
}

function Row({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <div className="flex items-start gap-3 rounded-2xl border border-line bg-surface-2 px-4 py-3">
      <Toggle checked={checked} onChange={onChange} label={label} />
      <div className="min-w-0">
        <p className="text-sm font-bold text-ink">{label}</p>
        <p className="text-xs font-medium leading-relaxed text-ink-3">{hint}</p>
      </div>
    </div>
  );
}

function Save({
  onClick,
  onSaved,
  label = "Save",
  disabled,
}: {
  onClick: () => Promise<unknown>;
  onSaved: () => void;
  label?: string;
  disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState(false);
  return (
    <div className="mt-4">
      <Button
        variant="primary"
        loading={busy}
        disabled={disabled}
        onClick={() => {
          setBusy(true);
          setError(null);
          setOk(false);
          onClick()
            .then(() => {
              setOk(true);
              onSaved();
            })
            .catch((e) => setError(errorMessage(e)))
            .finally(() => setBusy(false));
        }}
      >
        {label}
      </Button>
      {ok && <p className="mt-2 text-xs font-semibold text-ok">Saved.</p>}
      {error && (
        <div className="mt-2">
          <Alert>{error}</Alert>
        </div>
      )}
    </div>
  );
}

function Item({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-line pb-3">
      <dt className="shrink-0 font-semibold text-ink-3">{k}</dt>
      <dd className="truncate font-bold text-ink" title={v}>
        {v}
      </dd>
    </div>
  );
}
