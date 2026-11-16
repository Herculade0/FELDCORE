import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, RefreshCw, Search } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card } from "../ui";
import { Alert, Button, inputClass } from "../form";
import ContainerDialog from "../docker/ContainerDialog";
import type { Spec } from "../docker/types";
import type { AppSpec, AppSummary, Catalogue } from "./types";

const PAGE = 48;

export default function AppsPage() {
  const [cat, setCat] = useState<Catalogue | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("");
  const [showDeprecated, setShowDeprecated] = useState(false);
  const [shown, setShown] = useState(PAGE);
  const [opening, setOpening] = useState<string | null>(null);
  const [setup, setSetup] = useState<{ spec: Spec; notes: string[]; title: string; requires?: string } | null>(null);
  const auto = useRef(false);

  const load = useCallback(async () => {
    try {
      const c = await api<Catalogue>("/apps");
      setCat(c);
      setError(null);
      return c;
    } catch (e) {
      setError(errorMessage(e));
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    setError(null);
    try {
      setCat(await api<Catalogue>("/apps/refresh", { method: "POST" }));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (auto.current || !cat?.missing || cat.offline || refreshing) return;
    auto.current = true;
    void refresh();
  }, [cat, refresh, refreshing]);

  useEffect(() => {
    setShown(PAGE);
  }, [query, category, showDeprecated]);

  const labels = useMemo(() => {
    const m = new Map<string, string>();
    for (const c of cat?.categories ?? []) m.set(c.id, c.label);
    return m;
  }, [cat]);

  const apps = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (cat?.apps ?? []).filter((a) => {
      const cats = a.categories ?? [];
      if (!showDeprecated && a.deprecated) return false;
      if (category && !cats.includes(category)) return false;
      if (!q) return true;
      const hay = [a.name, a.author, a.image, a.overview, ...cats].join(" ").toLowerCase();
      return hay.includes(q);
    });
  }, [cat, query, category, showDeprecated]);

  const open = async (app: AppSummary) => {
    setOpening(app.id);
    setError(null);
    try {
      const res = await api<AppSpec>(`/apps/${encodeURIComponent(app.id)}`);
      setSetup({
        spec: res.spec,
        notes: res.notes ?? [],
        title: `Set up ${res.app.name}`,
        requires: res.app.requires,
      });
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setOpening(null);
    }
  };

  const visible = apps.slice(0, shown);
  const updated = cat?.updatedAt ? formatWhen(cat.updatedAt) : "";

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">App Store</h1>
          <p className="mt-2 max-w-xl text-sm font-semibold text-ink-2">
            Browse the Unraid Community Applications catalogue. Choosing an app opens the container
            setup with its template filled in — nothing is installed until you apply.
          </p>
        </div>
        <Button onClick={() => void refresh()} disabled={loading || refreshing}>
          <RefreshCw size={15} className={refreshing ? "animate-spin" : ""} />
          {refreshing ? "Refreshing…" : "Refresh"}
        </Button>
      </header>

      {error && <Alert>{error}</Alert>}
      {cat?.message && <Alert tone={cat.offline ? "warn" : "info"}>{cat.message}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}

      <div className="flex flex-col gap-3 sm:flex-row">
        <div className="relative min-w-0 flex-1">
          <Search size={16} className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-ink-3" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search apps"
            aria-label="Search apps"
            className={`${inputClass} pl-10`}
          />
        </div>
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          aria-label="Category"
          className={`${inputClass} sm:w-64`}
        >
          <option value="">All categories</option>
          {(cat?.categories ?? []).map((c) => (
            <option key={c.id} value={c.id}>
              {c.label} ({c.count})
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={() => setShowDeprecated((v) => !v)}
          aria-pressed={showDeprecated}
          className={`shrink-0 rounded-xl border px-3.5 py-2.5 text-xs font-bold ${
            showDeprecated ? "border-accent text-ink" : "border-line text-ink-3"
          }`}
        >
          Deprecated
        </button>
      </div>

      <p className="px-1 text-xs font-semibold text-ink-3">
        {loading || (cat?.missing && refreshing)
          ? "Loading the catalogue…"
          : `${apps.length.toLocaleString()} app${apps.length === 1 ? "" : "s"}`}
        {updated && ` · saved ${updated}`}
        {cat?.stale && !cat.offline && " · over a day old"}
      </p>

      {visible.length > 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {visible.map((app) => (
            <AppCard
              key={app.id}
              app={app}
              label={(id) => labels.get(id) ?? id.replace(/-/g, " / ")}
              busy={opening === app.id}
              onOpen={() => void open(app)}
            />
          ))}
        </div>
      ) : (
        !loading &&
        !refreshing && (
          <Card className="px-6 py-12 text-center">
            <h2 className="text-lg font-extrabold text-ink">
              {cat?.missing ? "No catalogue yet" : "No matching apps"}
            </h2>
            <p className="mx-auto mt-2 max-w-sm text-sm font-medium text-ink-2">
              {cat?.missing
                ? "Refresh downloads the Community Applications catalogue. It is kept on this server afterwards."
                : "Try another name or category."}
            </p>
          </Card>
        )
      )}

      {shown < apps.length && (
        <div className="flex justify-center">
          <Button onClick={() => setShown((n) => n + PAGE)}>
            Show more ({apps.length - shown} left)
          </Button>
        </div>
      )}

      {setup && (
        <ContainerDialog
          preset={setup.spec}
          notes={setup.notes}
          heading={setup.title}
          requires={setup.requires}
          onClose={() => setSetup(null)}
          onSaved={(name) => {
            setSetup(null);
            setNotice(`${name} was created. Open Docker to see it.`);
          }}
        />
      )}
    </div>
  );
}

function AppCard({
  app,
  label,
  busy,
  onOpen,
}: {
  app: AppSummary;
  label: (id: string) => string;
  busy: boolean;
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onOpen}
      disabled={busy}
      className="glass flex h-full flex-col rounded-3xl p-4 text-left transition-transform duration-200 hover:-translate-y-0.5 disabled:opacity-70"
    >
      <div className="flex items-start gap-3">
        <AppIcon src={app.icon} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-extrabold text-ink">{app.name}</div>
          <div className="truncate text-xs font-semibold text-ink-3">{app.author || app.image}</div>
        </div>
      </div>
      <p className="mt-3 line-clamp-3 flex-1 text-xs font-medium leading-relaxed text-ink-2">
        {app.overview || "No description."}
      </p>
      {app.requires && <RequiresNotice text={app.requires} />}
      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        {(app.categories ?? []).slice(0, 2).map((c) => (
          <span key={c} className="rounded-full bg-accent/10 px-2 py-0.5 text-[10px] font-bold text-accent">
            {label(c)}
          </span>
        ))}
        {app.beta && (
          <span className="rounded-full bg-warn/15 px-2 py-0.5 text-[10px] font-bold text-warn">Beta</span>
        )}
        {app.deprecated && (
          <span className="rounded-full bg-bad/10 px-2 py-0.5 text-[10px] font-bold text-bad">Deprecated</span>
        )}
        <span className="ml-auto text-[10px] font-bold text-accent">{busy ? "Opening…" : "Set up"}</span>
      </div>
    </button>
  );
}

function RequiresNotice({ text }: { text: string }) {
  return (
    <div className="mt-3 rounded-2xl border border-warn/40 bg-warn/15 px-3 py-2 text-xs font-medium leading-relaxed text-ink">
      <span className="font-bold text-warn">This app needs: </span>
      {text}
    </div>
  );
}

function AppIcon({ src }: { src?: string }) {
  const [ok, setOk] = useState(true);
  if (!src || !ok) {
    return (
      <span className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-accent/12 text-accent">
        <Box size={22} />
      </span>
    );
  }
  return (
    <img
      src={src}
      alt=""
      referrerPolicy="no-referrer"
      onError={() => setOk(false)}
      className="h-12 w-12 shrink-0 rounded-2xl bg-surface-2 object-contain"
    />
  );
}

function formatWhen(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}
