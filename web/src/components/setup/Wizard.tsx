import { useEffect, useRef, useState } from "react";
import QRCode from "qrcode";
import { Compass } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, SelectField, TextField } from "../form";
import { Card, CardTitle } from "../ui";
import { LogoMark, Wordmark } from "../Logo";

interface Mount {
  path: string;
  device: string;
  free: number;
  total: number;
  os: boolean;
}

interface DiskPart {
  name: string;
  path: string;
  size: number;
  fs?: string;
  mount?: string;
  os?: boolean;
}

interface Disk {
  name: string;
  path: string;
  size: number;
  model?: string;
  fs?: string;
  mount?: string;
  os?: boolean;
  serial?: string;
  byId?: string;
  usedFor?: string;
  children?: DiskPart[];
}

interface Conn {
  ok: boolean;
  dns: boolean;
  reachable: boolean;
  detail: string;
}

interface WizardView {
  done: boolean;
  step: string;
  steps: string[];
  preview?: string[];
  warning?: string;
  error?: string;
  optional?: boolean;
  rerun?: boolean;
  needsCode?: boolean;
  hostname: string;
  timezone: string;
  locale?: string;
  locales?: string[];
  zones?: string[];
  ntp: string;
  iface: string;
  ip: string;
  gateway: string;
  dns?: string[];
  netMode: string;
  newUrl?: string;
  mounts?: Mount[];
  disks?: Disk[];
  sharesRoot: string;
  onOsDisk: boolean;
  destructive?: boolean;
  confirmPhrase?: string;
  connectivity?: Conn;
  adminUser?: string;
  adminExists?: boolean;
  totpEnabled?: boolean;
  totpSecret?: string;
  totpUrl?: string;
  recoveryCodes?: string[];
  remoteMode?: string;
  notifyKind?: string;
  backupKind?: string;
  storageMode?: string;
  storageDevice?: string;
  storagePool?: string;
  summary?: string[];
}

const LABELS: Record<string, string> = {
  welcome: "Welcome",
  host: "This server",
  network: "Network",
  admin: "Administrator",
  signin: "Sign-in",
  storage: "Storage",
  remote: "Remote access",
  notify: "Notifications",
  backup: "Backups",
  summary: "Summary",
};

function bytes(n: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

function withCurrent(options: string[] | undefined, current: string): string[] {
  const list = options ? [...options] : [];
  if (current && !list.includes(current)) list.unshift(current);
  return list;
}

function OTPQR({ url }: { url: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas || !url) return;
    void QRCode.toCanvas(canvas, url, { width: 180, margin: 1 });
  }, [url]);
  return <canvas ref={ref} className="rounded-xl bg-white p-2" aria-label="Authenticator QR code" />;
}

export default function Wizard({
  onDone,
  embedded = false,
}: {
  onDone: () => void;
  embedded?: boolean;
}) {
  const [view, setView] = useState<WizardView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [reviewed, setReviewed] = useState(false);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[]>([]);

  const [host, setHost] = useState("");
  const [tz, setTz] = useState("UTC");
  const [locale, setLocale] = useState("en_US.UTF-8");
  const [ntp, setNtp] = useState("systemd-timesyncd");
  const [netMode, setNetMode] = useState("dhcp");
  const [address, setAddress] = useState("");
  const [gateway, setGateway] = useState("");
  const [dns, setDns] = useState("");
  const [root, setRoot] = useState("/srv/feldcore");
  const [storageMode, setStorageMode] = useState("keep");
  const [device, setDevice] = useState("");
  const [pool, setPool] = useState("");
  const [format, setFormat] = useState(false);
  const [phrase, setPhrase] = useState("");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [password2, setPassword2] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [skipTotp, setSkipTotp] = useState(false);
  const [disableTotp, setDisableTotp] = useState(false);
  const [remoteMode, setRemoteMode] = useState("none");
  const [cfMode, setCfMode] = useState("token");
  const [cfToken, setCfToken] = useState("");
  const [cfApi, setCfApi] = useState("");
  const [wgConf, setWgConf] = useState("");
  const [notifyKind, setNotifyKind] = useState("none");
  const [mailHost, setMailHost] = useState("");
  const [mailPort, setMailPort] = useState("587");
  const [mailUser, setMailUser] = useState("");
  const [mailPass, setMailPass] = useState("");
  const [mailFrom, setMailFrom] = useState("");
  const [mailTo, setMailTo] = useState("");
  const [ntfyUrl, setNtfyUrl] = useState("https://ntfy.sh");
  const [ntfyTopic, setNtfyTopic] = useState("");
  const [ntfyToken, setNtfyToken] = useState("");
  const [backupKind, setBackupKind] = useState("none");
  const [backupUser, setBackupUser] = useState("");
  const [backupPass, setBackupPass] = useState("");
  const [backupSchedule, setBackupSchedule] = useState("");

  const touch = () => setReviewed(false);

  const load = async () => {
    const v = await api<WizardView>("/setup/wizard");
    setView(v);
    if (v.done && !v.rerun) onDone();
    return v;
  };

  useEffect(() => {
    void load().catch((err) => setError(errorMessage(err)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!view) return;
    setHost(view.hostname || "");
    setTz(view.timezone || "UTC");
    setLocale(view.locale || "en_US.UTF-8");
    setNtp(view.ntp || "systemd-timesyncd");
    setNetMode(view.netMode || "dhcp");
    setGateway(view.gateway || "");
    setDns((view.dns ?? []).join(" "));
    setRoot(view.sharesRoot || "/srv/feldcore");
    setStorageMode(view.storageMode || "keep");
    setDevice(view.storageDevice || "");
    setPool(view.storagePool || "");
    setRemoteMode(view.remoteMode || "none");
    setNotifyKind(view.notifyKind || "none");
    setBackupKind(view.backupKind || "none");
    setReviewed(false);
    setPhrase("");
    setFormat(false);
    // Hydrate when the page changes. Typing must not be reset by a preview.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view?.step]);

  const body = () => ({
    step: view?.step,
    code,
    hostname: host,
    timezone: tz,
    locale,
    ntp,
    netMode,
    iface: view?.iface ?? "",
    address,
    gateway,
    dns,
    sharesRoot: root,
    storageMode,
    storageDevice: device,
    storageMount: root,
    storageFormat: format,
    storageFs: "ext4",
    storagePool: pool,
    storageConfirm: phrase,
    username: username.trim(),
    password,
    password2,
    currentPassword,
    totpCode,
    skipTotp,
    disableTotp,
    remoteMode,
    cfMode,
    cfToken,
    cfApi,
    wgConf,
    notifyKind,
    mailHost,
    mailPort: Number(mailPort) || 0,
    mailUser,
    mailPass,
    mailFrom,
    mailTo,
    mailTls: "starttls",
    ntfyUrl,
    ntfyTopic,
    ntfyToken,
    backupKind,
    backupUser,
    backupPass,
    backupSchedule,
  });

  const send = async (extra: Record<string, unknown>) => {
    setBusy(true);
    setError(null);
    try {
      const next = await api<WizardView>("/setup/wizard", {
        method: "POST",
        body: { ...body(), ...extra },
      });
      setView(next);
      if (next.recoveryCodes && next.recoveryCodes.length > 0) setRecovery(next.recoveryCodes);
      if (next.error) setError(next.error);
      if (extra.confirm || extra.skip || extra.back || extra.discardRerun) setReviewed(false);
      else if (!extra.check && extra.action !== "totp") setReviewed(true);
      if (next.done && !next.rerun) onDone();
      return next;
    } catch (err) {
      setError(errorMessage(err));
      return null;
    } finally {
      setBusy(false);
    }
  };

  if (!view) {
    return (
      <div className="flex min-h-screen items-center justify-center text-sm font-semibold text-ink-2">
        {error ?? "Loading setup…"}
      </div>
    );
  }

  const steps = view.steps?.length ? view.steps : ["welcome"];
  const index = Math.max(0, steps.indexOf(view.step));
  const immediate = view.step === "welcome" || view.step === "summary";
  const phraseNeeded = Boolean(view.destructive && view.confirmPhrase);
  const phraseOk = !phraseNeeded || phrase === view.confirmPhrase;

  const go = () => {
    if (view.needsCode && code.trim() === "") {
      setError("Enter the setup code from the server console.");
      return;
    }
    if (!immediate && !reviewed) {
      void send({});
      return;
    }
    if (phraseNeeded && phrase !== view.confirmPhrase) {
      setError(`Type ${view.confirmPhrase} to confirm erasing that disk.`);
      return;
    }
    void send({ confirm: true });
  };

  return (
    <div className={embedded ? "" : "min-h-screen px-6 py-10"}>
      {!embedded && (
        <div className="mx-auto mb-8 flex max-w-5xl items-center gap-3">
          <LogoMark />
          <Wordmark />
        </div>
      )}
      <div className="mx-auto grid max-w-5xl items-start gap-6 lg:grid-cols-[15rem_1fr]">
        <ol className="glass rounded-3xl p-4" aria-label="Setup steps">
          {steps.map((step, i) => {
            const on = step === view.step;
            const done = i < index;
            return (
              <li key={step} className="flex items-center gap-3 py-1.5" aria-current={on ? "step" : undefined}>
                <span
                  className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-extrabold ${
                    on ? "accent-gradient text-white" : done ? "bg-accent/15 text-accent" : "bg-track text-ink-3"
                  }`}
                >
                  {i + 1}
                </span>
                <span className={`text-sm font-bold ${on ? "text-ink" : "text-ink-3"}`}>{LABELS[step] ?? step}</span>
              </li>
            );
          })}
        </ol>

        <section data-step={view.step} className="glass rounded-[2rem] p-7 sm:p-8">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <p className="text-[11px] font-bold uppercase tracking-[0.18em] text-ink-3">
                Step {index + 1} of {steps.length}
                {view.optional ? " · optional" : ""}
                {view.rerun ? " · settings replay" : ""}
              </p>
              <h1 className="mt-1 text-2xl font-extrabold tracking-tight text-ink">{LABELS[view.step] ?? view.step}</h1>
            </div>
          </div>

          <div className="mt-6 grid gap-8 lg:grid-cols-2">
            <div className="space-y-4">
              {view.needsCode && (
                <TextField
                  label="Setup code"
                  value={code}
                  onChange={(e) => setCode(e.target.value.toUpperCase())}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="ABCD-EFGH"
                  hint="Printed as “FELDCORE setup code” when the server starts. It is not saved in this browser."
                  required
                />
              )}
              {view.step === "welcome" && <Welcome />}
              {view.step === "host" && (
                <>
                  <TextField label="Host name" value={host} onChange={(e) => { touch(); setHost(e.target.value); }} autoCapitalize="none" spellCheck={false} required />
                  <SelectField label="Time zone" value={tz} onChange={(e) => { touch(); setTz(e.target.value); }}>
                    {withCurrent(view.zones, tz).map((z) => (
                      <option key={z} value={z}>{z}</option>
                    ))}
                  </SelectField>
                  <SelectField label="Language" value={locale} onChange={(e) => { touch(); setLocale(e.target.value); }} hint="Used as the system locale.">
                    {withCurrent(view.locales, locale).map((z) => (
                      <option key={z} value={z}>{z}</option>
                    ))}
                  </SelectField>
                  <SelectField label="Clock sync" value={ntp} onChange={(e) => { touch(); setNtp(e.target.value); }}>
                    <option value="systemd-timesyncd">systemd-timesyncd</option>
                    <option value="chrony">chrony</option>
                  </SelectField>
                </>
              )}
              {view.step === "network" && (
                <NetworkFields
                  view={view}
                  netMode={netMode}
                  address={address}
                  gateway={gateway}
                  dns={dns}
                  busy={busy}
                  onMode={(v) => { touch(); setNetMode(v); }}
                  onAddress={(v) => { touch(); setAddress(v); }}
                  onGateway={(v) => { touch(); setGateway(v); }}
                  onDns={(v) => { touch(); setDns(v); }}
                  onCheck={() => void send({ check: true })}
                />
              )}
              {(view.step === "admin" || view.step === "signin") && (
                <AccountFields
                  view={view}
                  username={username}
                  password={password}
                  password2={password2}
                  currentPassword={currentPassword}
                  totpCode={totpCode}
                  skipTotp={skipTotp}
                  disableTotp={disableTotp}
                  busy={busy}
                  onUsername={(v) => { touch(); setUsername(v); }}
                  onPassword={(v) => { touch(); setPassword(v); }}
                  onPassword2={(v) => { touch(); setPassword2(v); }}
                  onCurrent={(v) => { touch(); setCurrentPassword(v); }}
                  onCode={(v) => { touch(); setTotpCode(v); }}
                  onSkip={(v) => { touch(); setSkipTotp(v); setDisableTotp(false); }}
                  onDisable={(v) => { touch(); setDisableTotp(v); setSkipTotp(false); }}
                  onEnrol={() => void send({ action: "totp" })}
                />
              )}
              {view.step === "storage" && (
                <StorageFields
                  view={view}
                  mode={storageMode}
                  device={device}
                  pool={pool}
                  root={root}
                  format={format}
                  phrase={phrase}
                  onMode={(v) => { touch(); setStorageMode(v); if (v !== "disk") setFormat(false); }}
                  onDevice={(v) => { touch(); setDevice(v); setStorageMode("disk"); }}
                  onPool={(v) => { touch(); setPool(v); }}
                  onRoot={(v) => { touch(); setRoot(v); }}
                  onFormat={(v) => { touch(); setFormat(v); }}
                  onPhrase={setPhrase}
                />
              )}
              {view.step === "remote" && (
                <RemoteFields
                  mode={remoteMode}
                  cfMode={cfMode}
                  cfToken={cfToken}
                  cfApi={cfApi}
                  wgConf={wgConf}
                  onMode={(v) => { touch(); setRemoteMode(v); }}
                  onCfMode={(v) => { touch(); setCfMode(v); }}
                  onCfToken={(v) => { touch(); setCfToken(v); }}
                  onCfApi={(v) => { touch(); setCfApi(v); }}
                  onWg={(v) => { touch(); setWgConf(v); }}
                />
              )}
              {view.step === "notify" && (
                <NotifyFields
                  kind={notifyKind}
                  mailHost={mailHost}
                  mailPort={mailPort}
                  mailUser={mailUser}
                  mailPass={mailPass}
                  mailFrom={mailFrom}
                  mailTo={mailTo}
                  ntfyUrl={ntfyUrl}
                  ntfyTopic={ntfyTopic}
                  ntfyToken={ntfyToken}
                  onKind={(v) => { touch(); setNotifyKind(v); }}
                  onMailHost={(v) => { touch(); setMailHost(v); }}
                  onMailPort={(v) => { touch(); setMailPort(v); }}
                  onMailUser={(v) => { touch(); setMailUser(v); }}
                  onMailPass={(v) => { touch(); setMailPass(v); }}
                  onMailFrom={(v) => { touch(); setMailFrom(v); }}
                  onMailTo={(v) => { touch(); setMailTo(v); }}
                  onNtfyUrl={(v) => { touch(); setNtfyUrl(v); }}
                  onNtfyTopic={(v) => { touch(); setNtfyTopic(v); }}
                  onNtfyToken={(v) => { touch(); setNtfyToken(v); }}
                />
              )}
              {view.step === "backup" && (
                <BackupFields
                  kind={backupKind}
                  user={backupUser}
                  pass={backupPass}
                  schedule={backupSchedule}
                  onKind={(v) => { touch(); setBackupKind(v); }}
                  onUser={(v) => { touch(); setBackupUser(v); }}
                  onPass={(v) => { touch(); setBackupPass(v); }}
                  onSchedule={(v) => { touch(); setBackupSchedule(v); }}
                />
              )}
              {view.step === "summary" && <Summary view={view} />}
            </div>

            <div className="space-y-4">
              {view.warning && <Alert tone="warn">{view.warning}</Alert>}
              {view.onOsDisk && view.step === "storage" && storageMode === "keep" && (
                <Alert tone="warn">That folder is on the operating-system disk. A separate disk is safer.</Alert>
              )}
              {format && storageMode === "disk" && (
                <Alert tone="error">
                  Formatting erases everything on {device || "the selected disk"}. This cannot be undone. The operating-system disk cannot be formatted here.
                </Alert>
              )}
              {view.step === "storage" && storageMode === "pool" && (
                <Alert tone="info">
                  An existing pool with this name is mounted and used for shares. A new name is only recorded. Assembling several disks still happens on the Storage page, after each disk serial is typed.
                </Alert>
              )}
              {reviewed && view.preview && view.preview.length > 0 && (
                <div className="rounded-2xl border border-line bg-surface-2 p-4">
                  <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">What this will do</p>
                  <ul className="mt-2 space-y-1.5 text-sm font-medium text-ink">
                    {view.preview.map((line) => (
                      <li key={line}>{line}</li>
                    ))}
                  </ul>
                </div>
              )}
              {view.connectivity && (
                <Alert tone={view.connectivity.ok ? "info" : "warn"}>{view.connectivity.detail}</Alert>
              )}
              {recovery.length > 0 && (
                <Alert tone="warn">
                  <p className="font-bold">Save these recovery codes now. They are not shown again.</p>
                  <ul className="mt-2 grid grid-cols-2 gap-1 font-mono text-xs">
                    {recovery.map((c) => (
                      <li key={c}>{c}</li>
                    ))}
                  </ul>
                </Alert>
              )}
              {view.summary && view.step === "summary" && (
                <ul className="space-y-2 text-sm font-medium text-ink">
                  {view.summary.map((line) => (
                    <li key={line}>{line}</li>
                  ))}
                </ul>
              )}
              {error && <Alert>{error}</Alert>}
            </div>
          </div>

          <div className="mt-8 flex flex-wrap items-center gap-3">
            {index > 0 && (
              <Button variant="ghost" disabled={busy} onClick={() => void send({ back: true })}>
                Back
              </Button>
            )}
            {view.optional && (
              <Button variant="secondary" disabled={busy} onClick={() => void send({ skip: true })}>
                Skip for now
              </Button>
            )}
            <Button variant="primary" loading={busy} disabled={reviewed && !phraseOk} onClick={go}>
              {view.step === "summary"
                ? view.rerun
                  ? "Save and return"
                  : "Finish setup"
                : view.step === "welcome"
                  ? "Continue"
                  : reviewed
                    ? "Apply"
                    : "Review"}
            </Button>
            {view.rerun && (
              <Button
                variant="ghost"
                disabled={busy}
                onClick={() => void send({ discardRerun: true })}
              >
                Leave this replay
              </Button>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}

function Welcome() {
  return (
    <div className="space-y-3 text-sm font-medium leading-relaxed text-ink-2">
      <p>
        This sets up the host name, network, administrator, data disk, remote access, notifications and backups.
        You can close the page and come back; setup continues at the same step.
      </p>
      <p>
        The setup code is required until the administrator account exists. Optional steps can be skipped and changed later in Settings.
      </p>
    </div>
  );
}

function NetworkFields({
  view, netMode, address, gateway, dns, busy, onMode, onAddress, onGateway, onDns, onCheck,
}: {
  view: WizardView;
  netMode: string;
  address: string;
  gateway: string;
  dns: string;
  busy: boolean;
  onMode: (v: string) => void;
  onAddress: (v: string) => void;
  onGateway: (v: string) => void;
  onDns: (v: string) => void;
  onCheck: () => void;
}) {
  return (
    <>
      <p className="text-sm font-medium text-ink-2">
        This server is using {view.iface || "an interface"}{view.ip ? ` at ${view.ip}` : ""}.
        {view.gateway ? ` Gateway ${view.gateway}.` : ""}
      </p>
      <SelectField label="Address" value={netMode} onChange={(e) => onMode(e.target.value)}>
        <option value="dhcp">Keep DHCP</option>
        <option value="static">Set a static address</option>
      </SelectField>
      {netMode === "static" && (
        <>
          <TextField label="Address" value={address} onChange={(e) => onAddress(e.target.value)} placeholder="192.168.1.20/24" spellCheck={false} hint="A wrong address drops this page. Fix it from the console." />
          <TextField label="Gateway" value={gateway} onChange={(e) => onGateway(e.target.value)} spellCheck={false} />
        </>
      )}
      <TextField label="DNS" value={dns} onChange={(e) => onDns(e.target.value)} spellCheck={false} hint="Used for the connectivity check. DHCP does not rewrite the system DNS." />
      <Button variant="secondary" disabled={busy} onClick={onCheck}>Check connectivity</Button>
    </>
  );
}

function AccountFields(props: {
  view: WizardView;
  username: string;
  password: string;
  password2: string;
  currentPassword: string;
  totpCode: string;
  skipTotp: boolean;
  disableTotp: boolean;
  busy: boolean;
  onUsername: (v: string) => void;
  onPassword: (v: string) => void;
  onPassword2: (v: string) => void;
  onCurrent: (v: string) => void;
  onCode: (v: string) => void;
  onSkip: (v: boolean) => void;
  onDisable: (v: boolean) => void;
  onEnrol: () => void;
}) {
  const existing = props.view.step === "signin" || props.view.adminExists;
  return (
    <>
      {!existing && (
        <>
          <TextField label="Username" value={props.username} onChange={(e) => props.onUsername(e.target.value)} autoComplete="username" spellCheck={false} required />
          <TextField label="Password" type="password" value={props.password} onChange={(e) => props.onPassword(e.target.value)} autoComplete="new-password" hint="At least 12 characters, with upper and lower case, a number and a symbol. A passphrase of 16 or more characters is fine too." required />
          <TextField label="Confirm password" type="password" value={props.password2} onChange={(e) => props.onPassword2(e.target.value)} autoComplete="new-password" required />
        </>
      )}
      {existing && (
        <TextField label="Current password" type="password" value={props.currentPassword} onChange={(e) => props.onCurrent(e.target.value)} autoComplete="current-password" />
      )}
      <div className="rounded-2xl border border-line bg-surface-2 p-4">
        <p className="text-sm font-bold text-ink">Authenticator app</p>
        <p className="mt-1 text-sm font-medium text-ink-2">
          Two-factor sign-in is optional and strongly recommended. The secret stays in the secrets store, not in the setup file.
        </p>
        {props.view.totpEnabled && props.view.step === "signin" ? (
          <label className="mt-3 flex items-center gap-2 text-sm font-semibold text-ink">
            <input type="checkbox" checked={props.disableTotp} onChange={(e) => props.onDisable(e.target.checked)} />
            Turn the authenticator off
          </label>
        ) : (
          <>
            <Button className="mt-3" variant="secondary" disabled={props.busy || props.skipTotp} onClick={props.onEnrol}>
              Set up authenticator
            </Button>
            {props.view.totpUrl && !props.skipTotp && (
              <div className="mt-4 flex flex-wrap items-start gap-4">
                <OTPQR url={props.view.totpUrl} />
                <div className="min-w-0">
                  <p className="text-xs font-bold text-ink-2">Or enter this secret</p>
                  <p className="mt-1 break-all font-mono text-sm font-bold text-ink">{props.view.totpSecret}</p>
                </div>
              </div>
            )}
            {!props.view.totpEnabled && (
              <label className="mt-3 flex items-center gap-2 text-sm font-semibold text-ink">
                <input type="checkbox" checked={props.skipTotp} onChange={(e) => props.onSkip(e.target.checked)} />
                Skip two-factor for now
              </label>
            )}
          </>
        )}
        {!props.skipTotp && (
          <div className="mt-3">
            <TextField label="Authenticator code" value={props.totpCode} onChange={(e) => props.onCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" spellCheck={false} />
          </div>
        )}
      </div>
    </>
  );
}

function StorageFields(props: {
  view: WizardView;
  mode: string;
  device: string;
  pool: string;
  root: string;
  format: boolean;
  phrase: string;
  onMode: (v: string) => void;
  onDevice: (v: string) => void;
  onPool: (v: string) => void;
  onRoot: (v: string) => void;
  onFormat: (v: boolean) => void;
  onPhrase: (v: string) => void;
}) {
  const disks = props.view.disks ?? [];
  return (
    <>
      <SelectField label="Where shares live" value={props.mode} onChange={(e) => props.onMode(e.target.value)}>
        <option value="keep">Keep a folder</option>
        <option value="disk">Use one disk</option>
        <option value="pool">Name a pool</option>
      </SelectField>
      <TextField label="Folder" value={props.root} onChange={(e) => props.onRoot(e.target.value)} spellCheck={false} />
      {props.mode === "pool" && (
        <TextField label="Pool name" value={props.pool} onChange={(e) => props.onPool(e.target.value)} spellCheck={false} hint="An existing pool with this name is mounted and used. A new name is only recorded; disks are not assembled here." />
      )}
      {props.mode === "disk" && (
        <>
          {disks.length === 0 && <p className="text-sm font-medium text-ink-2">No whole disks were detected.</p>}
          <ul className="space-y-2">
            {disks.map((d) => (
              <li key={d.path}>
                <label className={`flex cursor-pointer items-start gap-3 rounded-2xl border px-3 py-2 ${props.device === d.path ? "border-accent" : "border-line"}`}>
                  <input type="radio" name="disk" className="mt-1" checked={props.device === d.path} onChange={() => props.onDevice(d.path)} />
                  <span>
                    <span className="block text-sm font-bold text-ink">{d.path}{d.model ? ` · ${d.model}` : ""}</span>
                    <span className="block text-xs font-medium text-ink-3">
                      {bytes(d.size)}{d.mount ? ` · mounted at ${d.mount}` : " · not mounted"}{d.os ? " · operating system" : ""}{d.usedFor ? ` · ${d.usedFor}` : ""}
                    </span>
                  </span>
                </label>
              </li>
            ))}
          </ul>
          <label className="flex items-center gap-2 text-sm font-semibold text-ink">
            <input type="checkbox" checked={props.format} onChange={(e) => props.onFormat(e.target.checked)} />
            Erase this disk and format it as ext4
          </label>
        </>
      )}
      {props.view.confirmPhrase && (
        <TextField
          label="Type the confirmation phrase"
          value={props.phrase}
          onChange={(e) => props.onPhrase(e.target.value)}
          spellCheck={false}
          autoComplete="off"
          hint={`Type ${props.view.confirmPhrase} exactly.`}
        />
      )}
    </>
  );
}

function RemoteFields(props: {
  mode: string;
  cfMode: string;
  cfToken: string;
  cfApi: string;
  wgConf: string;
  onMode: (v: string) => void;
  onCfMode: (v: string) => void;
  onCfToken: (v: string) => void;
  onCfApi: (v: string) => void;
  onWg: (v: string) => void;
}) {
  return (
    <>
      <SelectField label="Remote access" value={props.mode} onChange={(e) => props.onMode(e.target.value)}>
        <option value="none">None</option>
        <option value="cloudflare">Cloudflare Tunnel</option>
        <option value="wireguard">WireGuard</option>
      </SelectField>
      {props.mode === "cloudflare" && (
        <>
          <SelectField label="Cloudflare credential" value={props.cfMode} onChange={(e) => props.onCfMode(e.target.value)}>
            <option value="token">Tunnel token</option>
            <option value="api">API token</option>
          </SelectField>
          {props.cfMode === "api" ? (
            <TextField label="API token" type="password" value={props.cfApi} onChange={(e) => props.onCfApi(e.target.value)} spellCheck={false} autoComplete="off" />
          ) : (
            <TextField label="Tunnel token" type="password" value={props.cfToken} onChange={(e) => props.onCfToken(e.target.value)} spellCheck={false} autoComplete="off" />
          )}
          <p className="text-xs font-medium text-ink-3">The token is encrypted. It is not written into the setup log.</p>
        </>
      )}
      {props.mode === "wireguard" && (
        <label className="block text-xs font-bold text-ink-2">
          WireGuard configuration
          <textarea
            value={props.wgConf}
            onChange={(e) => props.onWg(e.target.value)}
            spellCheck={false}
            rows={8}
            className="mt-1.5 w-full rounded-xl border border-line bg-surface-2 px-3.5 py-2.5 font-mono text-xs font-medium text-ink"
            placeholder="[Interface]..."
          />
        </label>
      )}
      {props.mode === "none" && (
        <p className="text-sm font-medium text-ink-2">Cloudflare Tunnel is turned off. An existing WireGuard tunnel is left as it is.</p>
      )}
    </>
  );
}

function NotifyFields(props: {
  kind: string;
  mailHost: string;
  mailPort: string;
  mailUser: string;
  mailPass: string;
  mailFrom: string;
  mailTo: string;
  ntfyUrl: string;
  ntfyTopic: string;
  ntfyToken: string;
  onKind: (v: string) => void;
  onMailHost: (v: string) => void;
  onMailPort: (v: string) => void;
  onMailUser: (v: string) => void;
  onMailPass: (v: string) => void;
  onMailFrom: (v: string) => void;
  onMailTo: (v: string) => void;
  onNtfyUrl: (v: string) => void;
  onNtfyTopic: (v: string) => void;
  onNtfyToken: (v: string) => void;
}) {
  return (
    <>
      <SelectField label="Notifications" value={props.kind} onChange={(e) => props.onKind(e.target.value)}>
        <option value="none">Not yet</option>
        <option value="email">Email</option>
        <option value="ntfy">ntfy</option>
      </SelectField>
      {props.kind === "email" && (
        <>
          <TextField label="SMTP host" value={props.mailHost} onChange={(e) => props.onMailHost(e.target.value)} spellCheck={false} />
          <TextField label="Port" value={props.mailPort} onChange={(e) => props.onMailPort(e.target.value)} inputMode="numeric" />
          <TextField label="Username" value={props.mailUser} onChange={(e) => props.onMailUser(e.target.value)} spellCheck={false} />
          <TextField label="Password" type="password" value={props.mailPass} onChange={(e) => props.onMailPass(e.target.value)} autoComplete="off" />
          <TextField label="From" value={props.mailFrom} onChange={(e) => props.onMailFrom(e.target.value)} spellCheck={false} />
          <TextField label="Send test to" value={props.mailTo} onChange={(e) => props.onMailTo(e.target.value)} spellCheck={false} />
        </>
      )}
      {props.kind === "ntfy" && (
        <>
          <TextField label="Server" value={props.ntfyUrl} onChange={(e) => props.onNtfyUrl(e.target.value)} spellCheck={false} />
          <TextField label="Topic" value={props.ntfyTopic} onChange={(e) => props.onNtfyTopic(e.target.value)} spellCheck={false} />
          <TextField label="Access token" type="password" value={props.ntfyToken} onChange={(e) => props.onNtfyToken(e.target.value)} autoComplete="off" hint="Optional. A test message is published before this step continues." />
        </>
      )}
      {props.kind === "none" && <p className="text-sm font-medium text-ink-2">You can add email or ntfy later. Skipping does not turn an existing notification off.</p>}
    </>
  );
}

function BackupFields(props: {
  kind: string;
  user: string;
  pass: string;
  schedule: string;
  onKind: (v: string) => void;
  onUser: (v: string) => void;
  onPass: (v: string) => void;
  onSchedule: (v: string) => void;
}) {
  return (
    <>
      <SelectField label="Backups" value={props.kind} onChange={(e) => props.onKind(e.target.value)}>
        <option value="none">Not yet</option>
        <option value="proton">Proton Drive</option>
      </SelectField>
      {props.kind === "proton" && (
        <>
          <TextField label="Proton account email" value={props.user} onChange={(e) => props.onUser(e.target.value)} spellCheck={false} />
          <TextField label="Password" type="password" value={props.pass} onChange={(e) => props.onPass(e.target.value)} autoComplete="off" />
          <TextField label="Daily time" value={props.schedule} onChange={(e) => props.onSchedule(e.target.value)} placeholder="03:30" spellCheck={false} hint="Leave empty for manual backups. App data is included. Snapshots are not." />
          <p className="text-xs font-medium text-ink-3">Proton Drive's rclone support is experimental. The password is encrypted.</p>
        </>
      )}
      {props.kind === "none" && <p className="text-sm font-medium text-ink-2">Skipping does not turn an existing backup off.</p>}
    </>
  );
}

function Summary({ view }: { view: WizardView }) {
  return (
    <p className="text-sm font-medium leading-relaxed text-ink-2">
      {view.rerun
        ? "These choices are saved. Disk formatting was not part of this replay."
        : "Finishing creates the default shares appdata, system, domains and isos if they are missing. SMB and NFS stay off."}
    </p>
  );
}

/** Settings replay of the steps that do not erase a disk or create an account. */
export function GuidedSetup() {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api<WizardView>("/setup/wizard")
      .then((v) => {
        if (v.rerun) setOpen(true);
      })
      .catch(() => {});
  }, []);

  const start = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/setup/wizard", { method: "POST", body: { beginRerun: true } });
      setOpen(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (open) {
    return <Wizard embedded onDone={() => setOpen(false)} />;
  }

  return (
    <Card>
      <CardTitle icon={Compass} title="Guided setup" hint="Does not erase disks" />
      <p className="mt-4 text-sm font-medium leading-relaxed text-ink-2">
        Run the host, network, sign-in, remote access, notification and backup steps again.
        Creating another administrator, and formatting a disk, stay on the first-run setup only.
      </p>
      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      <Button className="mt-4" variant="primary" loading={busy} onClick={() => void start()}>
        Run guided setup again
      </Button>
    </Card>
  );
}
