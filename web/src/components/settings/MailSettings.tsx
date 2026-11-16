import { useEffect, useState } from "react";
import { Mail } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";

interface Event {
  kind: string;
  title: string;
  at: string;
  sent: boolean;
  error?: string;
}

interface MailSettings {
  enabled: boolean;
  configured: boolean;
  mode: string;
  host: string;
  port: number;
  username: string;
  from: string;
  to: string;
  tls: string;
  hasPassword: boolean;
  bridgeManaged: boolean;
  bridgeHost: string;
  bridgePort: number;
  bridgeRunning: boolean;
  onContainerStop: boolean;
  onUpdate: boolean;
  events: Event[];
}

export default function MailNotifications() {
  const [data, setData] = useState<MailSettings | null>(null);
  const [mode, setMode] = useState("smtp");
  const [host, setHost] = useState("");
  const [port, setPort] = useState(587);
  const [username, setUsername] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [tls, setTls] = useState("starttls");
  const [password, setPassword] = useState("");
  const [bridgeManaged, setBridgeManaged] = useState(false);
  const [bridgeHost, setBridgeHost] = useState("127.0.0.1");
  const [bridgePort, setBridgePort] = useState(1025);
  const [enabled, setEnabled] = useState(false);
  const [onStop, setOnStop] = useState(true);
  const [onUpdate, setOnUpdate] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");

  const apply = (m: MailSettings) => {
    setData(m);
    setMode(m.mode || "smtp");
    setHost(m.host || "");
    setPort(m.port || (m.mode === "proton-bridge" ? 1025 : 587));
    setUsername(m.username || "");
    setFrom(m.from || "");
    setTo(m.to || "");
    setTls(m.tls || "starttls");
    setBridgeManaged(m.bridgeManaged);
    setBridgeHost(m.bridgeHost || "127.0.0.1");
    setBridgePort(m.bridgePort || 1025);
    setEnabled(m.enabled);
    setOnStop(m.configured ? m.onContainerStop : true);
    setOnUpdate(m.configured ? m.onUpdate : true);
    setPassword("");
  };

  const load = () =>
    api<MailSettings>("/notifications")
      .then(apply)
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  const body = () => ({
    enabled,
    mode,
    host,
    port: Number(port) || 0,
    username,
    from,
    to,
    tls,
    password,
    bridgeManaged,
    bridgeHost,
    bridgePort: Number(bridgePort) || 0,
    onContainerStop: onStop,
    onUpdate,
  });

  return (
    <Card>
      <div id="settings-mail">
        <CardTitle icon={Mail} title="Notifications" hint="Off until you turn them on" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Email when a container stops unexpectedly, or when a FELDCORE update is published. Generic SMTP, Proton
          Mail Bridge, or a Proton SMTP token for a custom domain.
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
        {data && (
          <>
            <label className="mt-4 flex items-center justify-between gap-4 text-sm font-semibold text-ink">
              Send notifications
              <Toggle checked={enabled} onChange={setEnabled} label="Send notifications" />
            </label>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <SelectField
                label="Mail server"
                value={mode}
                onChange={(e) => {
                  const next = e.target.value;
                  setMode(next);
                  if (next === "proton-token") {
                    setHost("smtp.protonmail.ch");
                    setPort(587);
                    setTls("starttls");
                  }
                  if (next === "proton-bridge") {
                    setHost("127.0.0.1");
                    setPort(1025);
                    setTls("starttls");
                  }
                }}
              >
                <option value="smtp">Generic SMTP</option>
                <option value="proton-bridge">Proton Mail Bridge</option>
                <option value="proton-token">Proton SMTP token</option>
              </SelectField>
              <SelectField label="Encryption" value={tls} onChange={(e) => setTls(e.target.value)}>
                <option value="starttls">STARTTLS</option>
                <option value="tls">TLS</option>
                <option value="none">None</option>
              </SelectField>
              {mode !== "proton-bridge" && (
                <>
                  <TextField label="Server" value={host} onChange={(e) => setHost(e.target.value)} autoCapitalize="none" spellCheck={false} />
                  <TextField label="Port" type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} />
                </>
              )}
              <TextField
                label={mode === "proton-token" ? "SMTP token username" : "Username"}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoCapitalize="none"
                spellCheck={false}
              />
              <TextField
                label={mode === "proton-token" ? "SMTP token" : mode === "proton-bridge" ? "Bridge password" : "Password"}
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={data.hasPassword ? "Stored, encrypted" : ""}
                autoComplete="new-password"
              />
              <TextField label="From" value={from} onChange={(e) => setFrom(e.target.value)} autoCapitalize="none" spellCheck={false} />
              <TextField label="To" value={to} onChange={(e) => setTo(e.target.value)} autoCapitalize="none" spellCheck={false} />
            </div>
            {mode === "proton-bridge" && (
              <div className="mt-4 space-y-3 rounded-2xl border border-line p-4">
                <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
                  Run Bridge as a container
                  <Toggle checked={bridgeManaged} onChange={setBridgeManaged} label="Run Proton Mail Bridge" />
                </label>
                <p className="text-xs font-medium text-ink-3">
                  {bridgeManaged
                    ? `FELDCORE starts ${"shenxn/protonmail-bridge"} and sends mail to 127.0.0.1:1025. The first login is done in that container's console. Bridge's own certificate is accepted.`
                    : "Point this at a Bridge you already run. Its certificate is accepted."}{" "}
                  {data.bridgeRunning ? "Bridge is running." : "Bridge is not running."}
                </p>
                {!bridgeManaged && (
                  <div className="grid gap-4 sm:grid-cols-2">
                    <TextField label="Bridge host" value={bridgeHost} onChange={(e) => setBridgeHost(e.target.value)} />
                    <TextField label="Bridge SMTP port" type="number" value={bridgePort} onChange={(e) => setBridgePort(Number(e.target.value))} />
                  </div>
                )}
              </div>
            )}
            {mode === "proton-token" && (
              <p className="mt-3 text-xs font-medium text-ink-3">
                Proton's SMTP submission tokens are for custom-domain plans. The token is stored encrypted and is not written to the log. The server defaults to smtp.protonmail.ch port 587.
              </p>
            )}
            <div className="mt-4 space-y-3">
              <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
                Container stopped unexpectedly
                <Toggle checked={onStop} onChange={setOnStop} label="Container stopped" />
              </label>
              <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
                FELDCORE update available
                <Toggle checked={onUpdate} onChange={setOnUpdate} label="Update available" />
              </label>
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
              <Button
                variant="primary"
                loading={busy === "save"}
                onClick={() => {
                  setBusy("save");
                  setError(null);
                  api<MailSettings>("/notifications", { method: "PUT", body: body() })
                    .then((m) => {
                      apply(m);
                      setNotice("Saved.");
                    })
                    .catch((e) => setError(errorMessage(e)))
                    .finally(() => setBusy(""));
                }}
              >
                Save
              </Button>
              <Button
                loading={busy === "test"}
                onClick={() => {
                  setBusy("test");
                  setError(null);
                  api<MailSettings>("/notifications", { method: "PUT", body: body() })
                    .then(() => api<{ message: string }>("/notifications/test", { method: "POST" }))
                    .then((r) => {
                      setNotice(r.message);
                      return load();
                    })
                    .catch((e) => setError(errorMessage(e)))
                    .finally(() => setBusy(""));
                }}
              >
                Send test email
              </Button>
            </div>
            {data.events.length > 0 && (
              <ul className="mt-4 space-y-2">
                {data.events
                  .slice()
                  .reverse()
                  .slice(0, 5)
                  .map((ev, i) => (
                    <li key={`${ev.at}-${i}`} className="text-xs font-medium text-ink-2">
                      <span className={ev.sent ? "text-ok" : "text-bad"}>{ev.sent ? "Sent" : "Not sent"}</span>
                      {" · "}
                      {ev.title}
                      {ev.error ? ` — ${ev.error}` : ""}
                    </li>
                  ))}
              </ul>
            )}
          </>
        )}
      </div>
    </Card>
  );
}
