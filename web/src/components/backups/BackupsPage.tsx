import { useEffect, useState } from "react";
import { Archive, Folder, RefreshCw } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes } from "../../lib/format";
import { Card, CardTitle } from "../ui";
import { Alert, Button, Modal, SelectField, TextField, Toggle } from "../form";
import { InstallPanel, showInstall, useInstall } from "../install/InstallPanel";

interface Sets {
  config: boolean;
  appdata: boolean;
  compose: boolean;
  vms: boolean;
  internalAffairs: boolean;
  snapshots: boolean;
}

interface Dest {
  id: string;
  name: string;
  kind: string;
  enabled: boolean;
  path?: string;
  share?: string;
  host?: string;
  port?: number;
  username?: string;
  hasPassword: boolean;
  hasKey: boolean;
  useSavedProton?: boolean;
  scheduleMode: string;
  scheduleTime?: string;
  scheduleDay?: string;
  keepDaily: number;
  keepWeekly: number;
  keepMonthly: number;
  sets: Sets;
  lastRun?: string;
  lastOk: boolean;
  lastError?: string;
  lastSnapshot?: string;
}

interface Job {
  id: string;
  kind: string;
  destinationId: string;
  destination: string;
  state: "queued" | "running" | "succeeded" | "failed";
  step: string;
  percent: number;
  bytesDone: number;
  bytesTotal: number;
  filesDone: number;
  filesTotal: number;
  snapshotId?: string;
  detail?: string;
  error?: string;
  log: string[];
  createdAt: string;
  finishedAt?: string;
}

interface Overview {
  engine: string;
  why: string;
  installed: boolean;
  installHint?: string;
  sets: { key: string; label: string; detail: string; present: boolean }[];
  destinations: Dest[];
  jobs: Job[];
  internalAffairs: {
    path: string;
    present: boolean;
    ledgerHead?: string;
    lastBackedHead?: string;
    note: string;
  };
  protonSaved: boolean;
  protonUser?: string;
}

interface Snap {
  id: string;
  shortId: string;
  time: string;
  tags?: string[];
}

interface Entry {
  name: string;
  type: string;
  path: string;
  size: number;
}

interface Browse {
  path: string;
  parent?: string;
  truncated?: boolean;
  entries: Entry[];
}

interface Draft {
  id?: string;
  name: string;
  kind: string;
  enabled: boolean;
  path: string;
  share: string;
  host: string;
  port: string;
  username: string;
  password: string;
  key: string;
  mailbox: string;
  twoFa: string;
  useSavedProton: boolean;
  scheduleMode: string;
  scheduleTime: string;
  scheduleDay: string;
  keepDaily: string;
  keepWeekly: string;
  keepMonthly: string;
  sets: Sets;
}

const SETS: { key: keyof Sets; label: string }[] = [
  { key: "config", label: "FELDCORE config and secrets" },
  { key: "appdata", label: "Container appdata" },
  { key: "compose", label: "Compose stacks" },
  { key: "vms", label: "Virtual machines" },
  { key: "internalAffairs", label: "Internal Affairs" },
  { key: "snapshots", label: "Container snapshots" },
];

const KIND: Record<string, string> = {
  local: "Local folder or share",
  smb: "SMB",
  nfs: "NFS",
  sftp: "SFTP",
  proton: "Proton Drive",
};

function blank(): Draft {
  return {
    name: "",
    kind: "local",
    enabled: true,
    path: "",
    share: "",
    host: "",
    port: "",
    username: "",
    password: "",
    key: "",
    mailbox: "",
    twoFa: "",
    useSavedProton: true,
    scheduleMode: "daily",
    scheduleTime: "03:00",
    scheduleDay: "sunday",
    keepDaily: "7",
    keepWeekly: "4",
    keepMonthly: "6",
    sets: { config: true, appdata: true, compose: true, vms: true, internalAffairs: true, snapshots: true },
  };
}

function fromDest(d: Dest): Draft {
  return {
    ...blank(),
    id: d.id,
    name: d.name,
    kind: d.kind,
    enabled: d.enabled,
    path: d.path || "",
    share: d.share || "",
    host: d.host || "",
    port: d.port ? String(d.port) : "",
    username: d.username || "",
    useSavedProton: d.useSavedProton ?? true,
    scheduleMode: d.scheduleMode || "manual",
    scheduleTime: d.scheduleTime || "03:00",
    scheduleDay: d.scheduleDay || "sunday",
    keepDaily: String(d.keepDaily),
    keepWeekly: String(d.keepWeekly),
    keepMonthly: String(d.keepMonthly),
    sets: { ...d.sets },
  };
}

function when(iso?: string): string {
  if (!iso) return "Never";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "Never";
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function Meter({ percent }: { percent: number }) {
  const p = Math.max(0, Math.min(100, percent || 0));
  return (
    <div className="h-2.5 w-full overflow-hidden rounded-full bg-track" role="progressbar" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <div className="accent-gradient h-full rounded-full transition-[width] duration-700" style={{ width: `${Math.max(p, p > 0 ? 2 : 0)}%` }} />
    </div>
  );
}

const STATE: Record<Job["state"], { label: string; cls: string }> = {
  queued: { label: "Queued", cls: "bg-track text-ink-2" },
  running: { label: "Running", cls: "bg-accent/15 text-accent" },
  succeeded: { label: "Finished", cls: "bg-ok/15 text-ok" },
  failed: { label: "Failed", cls: "bg-bad/12 text-bad" },
};

export default function BackupsPage() {
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [browseDest, setBrowseDest] = useState<string>("");
  const [snaps, setSnaps] = useState<Snap[]>([]);
  const [snap, setSnap] = useState("");
  const [browse, setBrowse] = useState<Browse | null>(null);
  const [restore, setRestore] = useState<{ paths: string[]; label: string } | null>(null);
  const [mode, setMode] = useState<"copy" | "replace">("copy");
  const [confirm, setConfirm] = useState("");
  const [password, setPassword] = useState<{ name: string; value: string } | null>(null);
  const [removeId, setRemoveId] = useState<string | null>(null);
  const install = useInstall("restic", () => void load());

  const load = () => {
    api<Overview>("/backup")
      .then((next) => {
        setData(next);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));
  };

  useEffect(() => {
    void load();
  }, []);

  const live = (data?.jobs ?? []).some((j) => j.state === "queued" || j.state === "running");
  useEffect(() => {
    if (!live) return;
    const timer = setInterval(load, 1000);
    return () => clearInterval(timer);
  }, [live]);

  const save = async () => {
    if (!draft) return;
    setBusy("save");
    setFormError(null);
    const body = {
      name: draft.name,
      kind: draft.kind,
      enabled: draft.enabled,
      path: draft.path,
      share: draft.share,
      host: draft.host,
      port: draft.port.trim() === "" ? 0 : Number(draft.port),
      username: draft.username,
      password: draft.password,
      key: draft.key,
      mailbox: draft.mailbox,
      twoFa: draft.twoFa,
      useSavedProton: draft.useSavedProton,
      scheduleMode: draft.scheduleMode,
      scheduleTime: draft.scheduleTime,
      scheduleDay: draft.scheduleDay,
      keepDaily: Number(draft.keepDaily) || 0,
      keepWeekly: Number(draft.keepWeekly) || 0,
      keepMonthly: Number(draft.keepMonthly) || 0,
      sets: draft.sets,
    };
    try {
      if (draft.id) {
        await api(`/backup/destinations/${draft.id}`, { method: "PUT", body });
      } else {
        await api("/backup/destinations", { method: "POST", body });
      }
      setDraft(null);
      setNotice("Destination saved.");
      load();
    } catch (e) {
      setFormError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const act = async (id: string, path: string, body?: unknown) => {
    setBusy(id + path);
    setError(null);
    try {
      const job = await api<Job>(path, { method: "POST", body: body ?? {} });
      setNotice(`${job.destination}: ${job.kind} ${job.state}.`);
      load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const openBrowse = async (id: string) => {
    setBrowseDest(id);
    setSnap("");
    setBrowse(null);
    setError(null);
    try {
      const res = await api<{ snapshots: Snap[] }>(`/backup/destinations/${id}/snapshots`);
      setSnaps(res.snapshots);
      if (res.snapshots[0]) {
        setSnap(res.snapshots[0].id);
        await openDir(id, res.snapshots[0].id, "/");
      }
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  const openDir = async (id: string, snapId: string, path: string) => {
    const q = path && path !== "/" ? `?path=${encodeURIComponent(path)}` : "";
    const view = await api<Browse>(`/backup/destinations/${id}/snapshots/${snapId}/files${q}`);
    setBrowse(view);
    setSnap(snapId);
  };

  const startRestore = async () => {
    if (!restore || !browseDest || !snap) return;
    setBusy("restore");
    try {
      await api<Job>("/backup/restore", {
        method: "POST",
        body: {
          destinationId: browseDest,
          snapshotId: snap,
          paths: restore.paths,
          mode,
          confirm: mode === "replace" ? confirm : "",
        },
      });
      setRestore(null);
      setConfirm("");
      setMode("copy");
      setNotice(mode === "replace" ? "Restore started. Files are being put back." : "Restore started. Files will land in a restore folder.");
      load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const reveal = async (d: Dest) => {
    setBusy("pw" + d.id);
    try {
      const res = await api<{ password: string }>(`/backup/destinations/${d.id}/password`, { method: "POST" });
      setPassword({ name: d.name, value: res.password });
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const remove = async () => {
    if (!removeId) return;
    setBusy("delete");
    try {
      await api(`/backup/destinations/${removeId}`, { method: "DELETE" });
      if (browseDest === removeId) {
        setBrowseDest("");
        setBrowse(null);
      }
      setRemoveId(null);
      setNotice("Destination removed. The copies already stored were left in place.");
      load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const dests = data?.destinations ?? [];
  const jobs = data?.jobs ?? [];
  const ia = data?.internalAffairs;

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Backups</h1>
          <p className="mt-1 max-w-2xl text-sm text-ink-2">{data?.why || "Versioned, encrypted copies of this server."}</p>
        </div>
        <Button onClick={() => { setFormError(null); setDraft(blank()); }}>New destination</Button>
      </header>

      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}

      {data && !data.installed && (
        <Card>
          <CardTitle icon={Archive} title="restic" hint="Required" />
          <p className="mt-3 text-sm text-ink-2">{data.installHint}</p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button variant="primary" loading={install.starting || install.active} onClick={() => void install.start()}>
              Install restic
            </Button>
          </div>
          {install.error && (
            <div className="mt-3">
              <Alert>{install.error}</Alert>
            </div>
          )}
          {showInstall(install.job) && install.job && (
            <div className="mt-4">
              <InstallPanel job={install.job} busy={install.starting} onRetry={() => void install.start()} />
            </div>
          )}
        </Card>
      )}

      <Card>
        <CardTitle icon={Archive} title="What is copied" hint={ia?.present ? "Cabinet is on this server" : "Cabinet not set up yet"} />
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          {(data?.sets ?? []).map((s) => (
            <div key={s.key} className="rounded-2xl bg-surface-2 px-4 py-3">
              <div className="text-sm font-bold text-ink">{s.label}</div>
              <p className="mt-1 text-xs text-ink-2">{s.detail}</p>
              <p className="mt-1 text-[11px] font-semibold text-ink-3">{s.present ? "Found on this server" : "Not on this server yet"}</p>
            </div>
          ))}
        </div>
        {ia && (
          <p className="mt-4 text-sm text-ink-2">
            Internal Affairs is in the default set. Ledger head {ia.ledgerHead ? ia.ledgerHead.slice(0, 12) : "is not recorded yet"}.
            Last backed head {ia.lastBackedHead ? ia.lastBackedHead.slice(0, 12) : "has not been stored"}. {ia.note}
          </p>
        )}
      </Card>

      <Card>
        <CardTitle icon={Archive} title="Destinations" hint={`${dests.length} saved`} />
        {dests.length === 0 ? (
          <p className="mt-4 text-sm text-ink-2">No destinations yet. A local folder is enough to take the first copy.</p>
        ) : (
          <ul className="mt-4 space-y-3">
            {dests.map((d) => (
              <li key={d.id} className="rounded-2xl border border-line px-4 py-3">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <div className="text-sm font-bold text-ink">{d.name}</div>
                    <div className="text-xs text-ink-3">
                      {KIND[d.kind] || d.kind}
                      {d.enabled ? "" : " · paused"}
                      {" · "}
                      {d.scheduleMode === "manual" ? "manual" : `${d.scheduleMode} ${d.scheduleTime || ""} ${d.scheduleDay || ""}`.trim()}
                      {" · keep "}
                      {d.keepDaily}/{d.keepWeekly}/{d.keepMonthly}
                    </div>
                    <div className="mt-1 text-xs text-ink-2">
                      Last run {when(d.lastRun)}
                      {d.lastSnapshot ? ` · ${d.lastSnapshot.slice(0, 8)}` : ""}
                      {d.lastError ? ` · ${d.lastError}` : ""}
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button size="sm" variant="primary" loading={busy === d.id + `/backup/destinations/${d.id}/run`} onClick={() => void act(d.id, `/backup/destinations/${d.id}/run`)}>
                      Back up now
                    </Button>
                    <Button size="sm" loading={busy === d.id + `/backup/destinations/${d.id}/check`} onClick={() => void act(d.id, `/backup/destinations/${d.id}/check`, { readData: false })}>
                      Check
                    </Button>
                    <Button size="sm" onClick={() => void openBrowse(d.id)}>Browse</Button>
                    <Button size="sm" onClick={() => { setFormError(null); setDraft(fromDest(d)); }}>Edit</Button>
                    <Button size="sm" loading={busy === "pw" + d.id} onClick={() => void reveal(d)}>Repository password</Button>
                    <Button size="sm" variant="danger" onClick={() => setRemoveId(d.id)}>Remove</Button>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card>
        <div className="flex items-center justify-between gap-3">
          <CardTitle icon={RefreshCw} title="Jobs" hint="Live progress" />
          <Button size="sm" onClick={() => load()}>Refresh</Button>
        </div>
        {jobs.length === 0 ? (
          <p className="mt-4 text-sm text-ink-2">No backup jobs yet.</p>
        ) : (
          <ul className="mt-4 space-y-4">
            {jobs.map((j) => {
              const tone = STATE[j.state] ?? STATE.queued;
              return (
                <li key={j.id} className="rounded-2xl bg-surface-2 px-4 py-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="text-sm font-bold text-ink">
                      {j.destination} · {j.kind}
                    </div>
                    <span className={`rounded-full px-2.5 py-0.5 text-[11px] font-bold ${tone.cls}`}>{tone.label}</span>
                  </div>
                  <p className="mt-1 text-xs text-ink-2">
                    {j.step}
                    {j.bytesTotal > 0 ? ` · ${bytes(j.bytesDone)} of ${bytes(j.bytesTotal)}` : ""}
                    {j.filesTotal > 0 ? ` · ${j.filesDone}/${j.filesTotal} files` : ""}
                    {j.detail ? ` · ${j.detail}` : ""}
                  </p>
                  {(j.state === "running" || j.state === "queued" || j.percent > 0) && (
                    <div className="mt-2">
                      <Meter percent={j.state === "succeeded" ? 100 : j.percent} />
                    </div>
                  )}
                  {j.error && <p className="mt-2 text-xs font-semibold text-bad">{j.error}</p>}
                  {j.log?.length > 0 && (
                    <pre className="mt-2 max-h-36 overflow-auto whitespace-pre-wrap text-[11px] text-ink-2">{j.log.slice(-12).join("\n")}</pre>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </Card>

      {browseDest && (
        <Card>
          <CardTitle icon={Folder} title="Browse a copy" hint={dests.find((d) => d.id === browseDest)?.name} />
          {snaps.length === 0 ? (
            <p className="mt-4 text-sm text-ink-2">This destination has no copies yet.</p>
          ) : (
            <>
              <div className="mt-4 max-w-sm">
                <SelectField
                  label="Copy"
                  value={snap}
                  onChange={(e) => {
                    const id = e.target.value;
                    setSnap(id);
                    void openDir(browseDest, id, "/").catch((err) => setError(errorMessage(err)));
                  }}
                >
                  {snaps.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.shortId || s.id.slice(0, 8)} · {when(s.time)}
                    </option>
                  ))}
                </SelectField>
              </div>
              {browse && (
                <div className="mt-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-xs font-semibold text-ink-3">{browse.path}</p>
                    <div className="flex gap-2">
                      {browse.parent && (
                        <Button size="sm" onClick={() => void openDir(browseDest, snap, browse.parent || "/").catch((e) => setError(errorMessage(e)))}>
                          Up
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="primary"
                        onClick={() => {
                          setMode("copy");
                          setConfirm("");
                          setRestore({ paths: [], label: "this whole copy" });
                        }}
                      >
                        Restore this copy
                      </Button>
                    </div>
                  </div>
                  {browse.truncated && <p className="mt-2 text-xs text-ink-3">This folder is showing the first 2000 entries.</p>}
                  <ul className="mt-3 divide-y divide-line">
                    {browse.entries.map((en) => (
                      <li key={en.path} className="flex items-center justify-between gap-3 py-2">
                        <button
                          type="button"
                          className="min-w-0 truncate text-left text-sm font-semibold text-ink"
                          onClick={() => {
                            if (en.type === "dir") void openDir(browseDest, snap, en.path).catch((e) => setError(errorMessage(e)));
                          }}
                        >
                          {en.type === "dir" ? "Folder" : "File"} · {en.name}
                          {en.type === "file" ? ` · ${bytes(en.size)}` : ""}
                        </button>
                        <Button
                          size="sm"
                          onClick={() => {
                            setMode("copy");
                            setConfirm("");
                            setRestore({ paths: [en.path], label: en.name });
                          }}
                        >
                          Restore
                        </Button>
                      </li>
                    ))}
                    {browse.entries.length === 0 && <li className="py-3 text-sm text-ink-2">This folder is empty.</li>}
                  </ul>
                </div>
              )}
            </>
          )}
        </Card>
      )}

      {draft && (
        <Modal
          title={draft.id ? "Edit destination" : "New destination"}
          onClose={() => setDraft(null)}
          onSubmit={() => void save()}
          footer={
            <>
              <Button onClick={() => setDraft(null)}>Cancel</Button>
              <Button variant="primary" loading={busy === "save"} onClick={() => void save()}>
                Save destination
              </Button>
            </>
          }
        >
          {formError && (
            <div className="mb-3">
              <Alert>{formError}</Alert>
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <TextField label="Name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
            <SelectField label="Where" value={draft.kind} onChange={(e) => setDraft({ ...draft, kind: e.target.value, port: e.target.value === "sftp" ? draft.port || "22" : draft.port })}>
              <option value="local">Local folder or share</option>
              <option value="smb">SMB</option>
              <option value="nfs">NFS</option>
              <option value="sftp">SFTP</option>
              <option value="proton">Proton Drive</option>
            </SelectField>
            {draft.kind === "local" && (
              <>
                <TextField label="Absolute path" hint="The repository is created in a feldcore-restic folder inside this path." value={draft.path} onChange={(e) => setDraft({ ...draft, path: e.target.value })} />
                <TextField label="Or a share name" hint="Leave the path blank when you use a share." value={draft.share} onChange={(e) => setDraft({ ...draft, share: e.target.value })} />
              </>
            )}
            {(draft.kind === "smb" || draft.kind === "nfs" || draft.kind === "sftp") && (
              <>
                <TextField label="Server" value={draft.host} onChange={(e) => setDraft({ ...draft, host: e.target.value })} />
                <TextField label="Port" hint="Blank uses the usual port." value={draft.port} onChange={(e) => setDraft({ ...draft, port: e.target.value })} />
                <TextField
                  label={draft.kind === "nfs" ? "Export" : draft.kind === "sftp" ? "Folder" : "Share name"}
                  value={draft.path}
                  onChange={(e) => setDraft({ ...draft, path: e.target.value })}
                />
                {draft.kind !== "nfs" && (
                  <TextField label="User" hint={draft.kind === "smb" ? "Blank uses guest." : ""} value={draft.username} onChange={(e) => setDraft({ ...draft, username: e.target.value })} />
                )}
              </>
            )}
            {draft.kind === "smb" && (
              <TextField label="Password" type="password" value={draft.password} onChange={(e) => setDraft({ ...draft, password: e.target.value })} placeholder={draft.id ? "Stored, encrypted" : ""} autoComplete="new-password" />
            )}
            {draft.kind === "sftp" && (
              <div className="sm:col-span-2">
                <label className="text-xs font-bold text-ink-2">Private key</label>
                <textarea
                  className="mt-1 w-full rounded-2xl border border-line bg-surface-2 px-3 py-2 font-mono text-xs text-ink"
                  rows={5}
                  value={draft.key}
                  placeholder={draft.id ? "Stored, encrypted. Paste a new key to replace it." : "Begins with -----BEGIN"}
                  onChange={(e) => setDraft({ ...draft, key: e.target.value })}
                />
                <p className="mt-1 text-xs text-ink-3">An unencrypted key. FELDCORE connects with BatchMode, so a passphrase key cannot be used.</p>
              </div>
            )}
            {draft.kind === "proton" && (
              <>
                <TextField label="Drive folder" hint="Created under your Proton Drive." value={draft.path} onChange={(e) => setDraft({ ...draft, path: e.target.value })} />
                <div className="flex items-center justify-between gap-3 rounded-2xl bg-surface-2 px-3 py-2">
                  <span className="text-sm font-semibold text-ink">Use the Settings account{data?.protonUser ? ` (${data.protonUser})` : ""}</span>
                  <Toggle checked={draft.useSavedProton} onChange={(on) => setDraft({ ...draft, useSavedProton: on })} label="Use saved Proton account" />
                </div>
                {!draft.useSavedProton && (
                  <>
                    <TextField label="Proton email" value={draft.username} onChange={(e) => setDraft({ ...draft, username: e.target.value })} />
                    <TextField label="Password" type="password" value={draft.password} onChange={(e) => setDraft({ ...draft, password: e.target.value })} autoComplete="new-password" />
                    <TextField label="Mailbox password" type="password" value={draft.mailbox} onChange={(e) => setDraft({ ...draft, mailbox: e.target.value })} autoComplete="new-password" />
                    <TextField label="2FA or OTP secret" value={draft.twoFa} onChange={(e) => setDraft({ ...draft, twoFa: e.target.value })} />
                  </>
                )}
                <p className="text-xs text-ink-3 sm:col-span-2">
                  Proton Drive uses rclone. A live Proton account is required for the upload itself. The repository password is separate and is shown here after the first backup.
                  {data?.protonSaved ? " A Proton account is already saved in Settings." : " Save a Proton account in Settings, or enter one here."}
                </p>
              </>
            )}
            <SelectField label="Schedule" value={draft.scheduleMode} onChange={(e) => setDraft({ ...draft, scheduleMode: e.target.value })}>
              <option value="manual">Manual</option>
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
            </SelectField>
            {draft.scheduleMode !== "manual" && (
              <TextField label="Time" hint="24-hour, such as 03:00" value={draft.scheduleTime} onChange={(e) => setDraft({ ...draft, scheduleTime: e.target.value })} />
            )}
            {draft.scheduleMode === "weekly" && (
              <SelectField label="Day" value={draft.scheduleDay} onChange={(e) => setDraft({ ...draft, scheduleDay: e.target.value })}>
                {["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"].map((d) => (
                  <option key={d} value={d}>{d}</option>
                ))}
              </SelectField>
            )}
            <TextField label="Keep daily" value={draft.keepDaily} onChange={(e) => setDraft({ ...draft, keepDaily: e.target.value })} />
            <TextField label="Keep weekly" value={draft.keepWeekly} onChange={(e) => setDraft({ ...draft, keepWeekly: e.target.value })} />
            <TextField label="Keep monthly" value={draft.keepMonthly} onChange={(e) => setDraft({ ...draft, keepMonthly: e.target.value })} />
          </div>
          <div className="mt-4 space-y-2">
            {SETS.map((s) => (
              <label key={s.key} className="flex items-center justify-between gap-3 text-sm font-semibold text-ink">
                {s.label}
                <Toggle
                  checked={draft.sets[s.key]}
                  onChange={(on) => setDraft({ ...draft, sets: { ...draft.sets, [s.key]: on } })}
                  label={s.label}
                />
              </label>
            ))}
          </div>
          <label className="mt-4 flex items-center justify-between gap-3 text-sm font-semibold text-ink">
            Schedule is on
            <Toggle checked={draft.enabled} onChange={(on) => setDraft({ ...draft, enabled: on })} label="Enabled" />
          </label>
          <p className="mt-3 text-xs text-ink-3">Zero in every retention box keeps every version. The repository password is created on the first backup and is not stored inside the archive.</p>
        </Modal>
      )}

      {restore && (
        <Modal
          title={`Restore ${restore.label}`}
          onClose={() => setRestore(null)}
          footer={
            <>
              <Button onClick={() => setRestore(null)}>Cancel</Button>
              <Button variant="primary" loading={busy === "restore"} onClick={() => void startRestore()}>
                Start restore
              </Button>
            </>
          }
        >
          <SelectField label="Where it goes" value={mode} onChange={(e) => setMode(e.target.value === "replace" ? "replace" : "copy")}>
            <option value="copy">A restore folder on this server</option>
            <option value="replace">Replace the copies on this server</option>
          </SelectField>
          {mode === "replace" && (
            <div className="mt-3">
              <TextField label='Type replace to confirm' value={confirm} onChange={(e) => setConfirm(e.target.value)} />
            </div>
          )}
        </Modal>
      )}

      {password && (
        <Modal title={`Repository password for ${password.name}`} onClose={() => setPassword(null)} footer={<Button onClick={() => setPassword(null)}>Close</Button>}>
          <p className="text-sm text-ink-2">Keep this with the backup. The encrypted secrets inside a copy cannot open the repository.</p>
          <pre className="mt-3 overflow-auto rounded-2xl bg-surface-2 p-3 text-xs text-ink">{password.value}</pre>
        </Modal>
      )}

      {removeId && (
        <Modal
          title="Remove this destination?"
          onClose={() => setRemoveId(null)}
          footer={
            <>
              <Button onClick={() => setRemoveId(null)}>Cancel</Button>
              <Button variant="danger" loading={busy === "delete"} onClick={() => void remove()}>
                Remove destination
              </Button>
            </>
          }
        >
          <p className="text-sm text-ink-2">The destination is removed from FELDCORE. Copies already written stay where they are.</p>
        </Modal>
      )}
    </div>
  );
}
