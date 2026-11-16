import { useEffect, useState } from "react";
import { Check, Info as InfoIcon, Monitor, Moon, Palette, Sun, type LucideIcon } from "lucide-react";
import type { Info } from "../types";
import { VERSION } from "../version";
import { ACCENTS, useTheme, type Mode } from "../lib/theme";
import { shortCpu } from "../lib/format";
import { Card, CardTitle } from "./ui";

const MODES: { key: Mode; label: string; icon: LucideIcon }[] = [
  { key: "light", label: "Light", icon: Sun },
  { key: "dark", label: "Dark", icon: Moon },
  { key: "system", label: "System", icon: Monitor },
];

export default function SettingsPage() {
  const { mode, accent, setMode, setAccent } = useTheme();
  const [info, setInfo] = useState<Info | null>(null);

  useEffect(() => {
    fetch("/api/v1/system/info")
      .then((r) => (r.ok ? r.json() : null))
      .then(setInfo)
      .catch(() => setInfo(null));
  }, []);

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Settings</h1>
        <p className="mt-2 text-sm font-semibold text-ink-2">
          Appearance and system information. More arrives with each module.
        </p>
      </header>

      <Card>
        <CardTitle icon={Palette} title="Appearance" hint="Saved in this browser" />
        <div className="mt-6 grid gap-8 lg:grid-cols-2">
          <fieldset>
            <legend className="mb-3 text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">
              Theme
            </legend>
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
            <legend className="mb-3 text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">
              Accent
            </legend>
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

      <Card>
        <CardTitle icon={InfoIcon} title="About" />
        <dl className="mt-5 grid gap-x-10 gap-y-3 text-sm sm:grid-cols-2">
          <Item k="FELDCORE" v={`v${VERSION} (pre-alpha)`} />
          <Item k="Hostname" v={info?.hostname ?? "–"} />
          <Item
            k="Operating system"
            v={
              info
                ? `${info.platform.charAt(0).toUpperCase()}${info.platform.slice(1)} ${info.platformVersion}`
                : "–"
            }
          />
          <Item k="Kernel" v={info?.kernel ?? "–"} />
          <Item k="Processor" v={info?.cpuModel ? shortCpu(info.cpuModel) : "–"} />
          <Item k="Architecture" v={info?.arch ?? "–"} />
        </dl>
      </Card>
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
