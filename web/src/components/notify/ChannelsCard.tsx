import { useEffect, useState } from "react";
import { Bell } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";

interface Channel {
  id: string;
  kind: string;
  name: string;
  enabled: boolean;
  url?: string;
  topic?: string;
  hasSecret: boolean;
}

interface Route {
  id: string;
  event: string;
  minSeverity: string;
  channels: string[];
}

interface Quiet {
  enabled: boolean;
  start?: string;
  end?: string;
  allowCritical: boolean;
}

interface NotifyView {
  channels: Channel[];
  routes: Route[];
  quiet: Quiet;
  dedupeSeconds: number;
  ratePerHour: number;
  inAppEnabled: boolean;
}

const KINDS = [
  { value: "ntfy", label: "ntfy" },
  { value: "gotify", label: "Gotify" },
  { value: "discord", label: "Discord" },
  { value: "slack", label: "Slack" },
  { value: "webhook", label: "Webhook" },
];

const EMPTY = {
  id: "",
  kind: "ntfy",
  name: "",
  enabled: true,
  url: "",
  topic: "",
  secret: "",
};

export default function ChannelsCard() {
  const [view, setView] = useState<NotifyView | null>(null);
  const [form, setForm] = useState(EMPTY);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");

  const load = () =>
    api<NotifyView>("/notifications")
      .then((v) => {
        setView({
          channels: v.channels ?? [],
          routes: v.routes ?? [],
          quiet: v.quiet ?? { enabled: false, allowCritical: true, start: "22:00", end: "07:00" },
          dedupeSeconds: v.dedupeSeconds || 900,
          ratePerHour: v.ratePerHour || 30,
          inAppEnabled: v.inAppEnabled !== false,
        });
      })
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  if (!view) {
    return (
      <Card>
        <CardTitle icon={Bell} title="Notification channels" />
        {error && (
          <div className="mt-3">
            <Alert>{error}</Alert>
          </div>
        )}
      </Card>
    );
  }

  const choices = [
    { id: "inapp", name: "In-app bell" },
    { id: "email", name: "Email" },
    ...view.channels.map((c) => ({ id: c.id, name: c.name })),
  ];

  const savePolicy = () => {
    setBusy("policy");
    setError(null);
    api<NotifyView>("/notifications/policy", {
      method: "PUT",
      body: {
        inAppEnabled: view.inAppEnabled,
        quiet: {
          enabled: view.quiet.enabled,
          start: view.quiet.start || "22:00",
          end: view.quiet.end || "07:00",
          allowCritical: view.quiet.allowCritical,
        },
        dedupeSeconds: Number(view.dedupeSeconds),
        ratePerHour: Number(view.ratePerHour),
      },
    })
      .then(() => {
        setNotice("Saved.");
        return load();
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setBusy(""));
  };

  const saveRoutes = () => {
    setBusy("routes");
    setError(null);
    const routes = view.routes
      .filter((r) => r.event.trim() && r.channels.length)
      .map((r) => ({
        id: r.id,
        event: r.event.trim(),
        minSeverity: r.minSeverity || "warning",
        channels: r.channels,
      }));
    api<NotifyView>("/notifications/routes", { method: "PUT", body: { routes } })
      .then(() => {
        setNotice("Routes saved.");
        return load();
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setBusy(""));
  };

  const webhook = form.kind === "discord" || form.kind === "slack";

  return (
    <Card>
      <CardTitle icon={Bell} title="Notification channels" hint="In addition to email" />
      <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
        ntfy, Gotify, a Discord or Slack webhook, or any JSON webhook. Tokens stay encrypted and are not written
        to the log. Quiet hours use this server's clock. Critical messages can still go out. The bell keeps its
        own copy either way.
      </p>
      {error && (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      )}
      {notice && (
        <div className="mt-3">
          <Alert tone="info">{notice}</Alert>
        </div>
      )}

      <div className="mt-5 space-y-3">
        <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
          In-app bell
          <Toggle
            checked={view.inAppEnabled}
            onChange={(on) => setView({ ...view, inAppEnabled: on })}
            label="In-app bell"
          />
        </label>
        <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
          Quiet hours
          <Toggle
            checked={view.quiet.enabled}
            onChange={(on) => setView({ ...view, quiet: { ...view.quiet, enabled: on } })}
            label="Quiet hours"
          />
        </label>
        <div className="grid gap-4 sm:grid-cols-2">
          <TextField
            label="Quiet from"
            value={view.quiet.start || ""}
            placeholder="22:00"
            onChange={(e) => setView({ ...view, quiet: { ...view.quiet, start: e.target.value } })}
          />
          <TextField
            label="Quiet until"
            value={view.quiet.end || ""}
            placeholder="07:00"
            onChange={(e) => setView({ ...view, quiet: { ...view.quiet, end: e.target.value } })}
          />
          <TextField
            label="De-duplication"
            hint="Seconds. The same event is not sent again inside this window unless it gets more severe."
            type="number"
            value={view.dedupeSeconds}
            onChange={(e) => setView({ ...view, dedupeSeconds: Number(e.target.value) })}
          />
          <TextField
            label="Rate limit"
            hint="Messages per hour, per external channel. The bell is not limited."
            type="number"
            value={view.ratePerHour}
            onChange={(e) => setView({ ...view, ratePerHour: Number(e.target.value) })}
          />
        </div>
        <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
          Send critical during quiet hours
          <Toggle
            checked={view.quiet.allowCritical}
            onChange={(on) => setView({ ...view, quiet: { ...view.quiet, allowCritical: on } })}
            label="Allow critical"
          />
        </label>
        <Button variant="primary" loading={busy === "policy"} onClick={savePolicy}>
          Save policy
        </Button>
      </div>

      <div className="mt-6 border-t border-line pt-5">
        <h3 className="text-sm font-bold text-ink">Routing</h3>
        <p className="mt-1 text-xs font-medium text-ink-3">
          An event name, a prefix such as backup.*, or * for every event. Email still follows the switches in the
          card above unless a route names it.
        </p>
        <div className="mt-3 space-y-3">
          {view.routes.map((route, i) => (
            <div key={route.id || i} className="rounded-2xl border border-line p-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <TextField
                  label="Event"
                  value={route.event}
                  placeholder="backup.failed"
                  spellCheck={false}
                  onChange={(e) => {
                    const routes = view.routes.slice();
                    routes[i] = { ...route, event: e.target.value };
                    setView({ ...view, routes });
                  }}
                />
                <SelectField
                  label="Minimum severity"
                  value={route.minSeverity || "warning"}
                  onChange={(e) => {
                    const routes = view.routes.slice();
                    routes[i] = { ...route, minSeverity: e.target.value };
                    setView({ ...view, routes });
                  }}
                >
                  <option value="info">Info</option>
                  <option value="warning">Warning</option>
                  <option value="critical">Critical</option>
                </SelectField>
              </div>
              <div className="mt-3 flex flex-wrap gap-2">
                {choices.map((c) => {
                  const on = route.channels.includes(c.id);
                  return (
                    <button
                      key={c.id}
                      type="button"
                      onClick={() => {
                        const channels = on ? route.channels.filter((id) => id !== c.id) : [...route.channels, c.id];
                        const routes = view.routes.slice();
                        routes[i] = { ...route, channels };
                        setView({ ...view, routes });
                      }}
                      className={`rounded-full px-3 py-1 text-xs font-bold ${
                        on ? "accent-gradient text-white" : "bg-track text-ink-2"
                      }`}
                    >
                      {c.name}
                    </button>
                  );
                })}
                <button
                  type="button"
                  className="rounded-full px-3 py-1 text-xs font-bold text-bad"
                  onClick={() => setView({ ...view, routes: view.routes.filter((_, j) => j !== i) })}
                >
                  Remove
                </button>
              </div>
            </div>
          ))}
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          <Button
            onClick={() =>
              setView({
                ...view,
                routes: [...view.routes, { id: "", event: "", minSeverity: "warning", channels: ["inapp"] }],
              })
            }
          >
            Add route
          </Button>
          <Button variant="primary" loading={busy === "routes"} onClick={saveRoutes}>
            Save routes
          </Button>
        </div>
      </div>

      <div className="mt-6 border-t border-line pt-5">
        <h3 className="text-sm font-bold text-ink">Channels</h3>
        <ul className="mt-3 space-y-2">
          {view.channels.map((c) => (
            <li key={c.id} className="flex flex-wrap items-center justify-between gap-2 rounded-2xl border border-line px-3 py-2">
              <div className="min-w-0">
                <div className="text-sm font-bold text-ink">
                  {c.name}{" "}
                  <span className="text-xs font-semibold text-ink-3">{c.kind}</span>
                </div>
                <div className="truncate text-[11px] font-medium text-ink-3">
                  {c.enabled ? "On" : "Off"}
                  {c.url ? ` · ${c.url}` : ""}
                  {c.topic ? ` · ${c.topic}` : ""}
                  {c.hasSecret ? " · secret stored" : ""}
                </div>
              </div>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  loading={busy === c.id}
                  onClick={() => {
                    setBusy(c.id);
                    setError(null);
                    api<{ message: string }>(`/notifications/channels/${c.id}/test`, { method: "POST" })
                      .then((r) => setNotice(r.message))
                      .catch((e) => setError(errorMessage(e)))
                      .finally(() => setBusy(""));
                  }}
                >
                  Send test
                </Button>
                <Button
                  size="sm"
                  variant="danger"
                  onClick={() => {
                    setError(null);
                    api<NotifyView>(`/notifications/channels/${c.id}`, { method: "DELETE" })
                      .then(() => load())
                      .catch((e) => setError(errorMessage(e)));
                  }}
                >
                  Delete
                </Button>
              </div>
            </li>
          ))}
          {view.channels.length === 0 && (
            <li className="text-xs font-medium text-ink-3">No extra channels yet. Email is configured above.</li>
          )}
        </ul>

        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <SelectField label="Kind" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            {KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </SelectField>
          <TextField label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          {!webhook && (
            <TextField
              label={form.kind === "ntfy" ? "ntfy server" : form.kind === "gotify" ? "Gotify server" : "Webhook URL"}
              value={form.url}
              spellCheck={false}
              autoCapitalize="none"
              placeholder="https://"
              onChange={(e) => setForm({ ...form, url: e.target.value })}
            />
          )}
          {form.kind === "ntfy" && (
            <TextField
              label="Topic"
              value={form.topic}
              spellCheck={false}
              onChange={(e) => setForm({ ...form, topic: e.target.value })}
            />
          )}
          <TextField
            label={webhook ? "Webhook URL" : form.kind === "gotify" ? "App token" : "Token"}
            type="password"
            value={form.secret}
            autoComplete="new-password"
            spellCheck={false}
            hint={webhook ? "The webhook address is the secret. It is not shown again." : "Optional for ntfy and a plain webhook."}
            onChange={(e) => setForm({ ...form, secret: e.target.value })}
          />
        </div>
        <label className="mt-4 flex items-center justify-between gap-4 text-sm font-semibold text-ink">
          Enabled
          <Toggle checked={form.enabled} onChange={(on) => setForm({ ...form, enabled: on })} label="Channel enabled" />
        </label>
        <div className="mt-4">
          <Button
            variant="primary"
            loading={busy === "channel"}
            onClick={() => {
              setBusy("channel");
              setError(null);
              api<NotifyView>("/notifications/channels", {
                method: "POST",
                body: {
                  id: form.id,
                  kind: form.kind,
                  name: form.name,
                  enabled: form.enabled,
                  url: webhook ? "" : form.url,
                  topic: form.topic,
                  secret: form.secret,
                },
              })
                .then(() => {
                  setForm(EMPTY);
                  setNotice("Channel saved.");
                  return load();
                })
                .catch((e) => setError(errorMessage(e)))
                .finally(() => setBusy(""));
            }}
          >
            Add channel
          </Button>
        </div>
      </div>
    </Card>
  );
}
