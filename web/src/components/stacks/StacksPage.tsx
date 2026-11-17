import { useEffect, useRef, useState } from "react";
import {
  Download,
  FileText,
  FileUp,
  GitBranch,
  Layers,
  Play,
  Plus,
  RefreshCw,
  Square,
  Trash2,
  Upload,
} from "lucide-react";
import { ApiError, api, errorMessage } from "../../lib/api";
import { Card } from "../ui";
import { Alert, Button, IconButton, Modal, TextField, Toggle } from "../form";
import { InstallPanel, showInstall, useInstall, type InstallJob } from "../install/InstallPanel";
import PublishForm from "../publish/PublishForm";

interface Finding {
  service: string;
  id: string;
  severity: string;
  title: string;
  reason: string;
  suggestion: string;
  fix: boolean;
}

interface ServiceInfo {
  name: string;
  image?: string;
  container?: string;
  id?: string;
  state?: string;
  status?: string;
  ports?: string[];
}

interface Preview {
  valid: boolean;
  error?: string;
  compose: string;
  services: ServiceInfo[];
  findings: Finding[];
  notes?: string[];
  diff?: string;
  network?: string;
  project?: string;
  isolate: boolean;
  ownNetworks?: boolean;
  changed?: boolean;
}

interface Stack {
  name: string;
  compose: string;
  env?: string;
  isolate: boolean;
  source?: string;
  gitUrl?: string;
  gitPath?: string;
  gitRef?: string;
  replaces?: string;
  state: string;
  network?: string;
  project: string;
  path: string;
  services: ServiceInfo[];
  notes?: string[];
}

interface Summary {
  name: string;
  isolate: boolean;
  source?: string;
  replaces?: string;
  state: string;
  network?: string;
  project: string;
  path: string;
  services: ServiceInfo[];
}

interface Catalog {
  root: string;
  compose?: string;
  composeOk: boolean;
  stacks: Summary[];
}

interface GateBody {
  error?: string;
  findings?: Finding[];
  diff?: string;
  confirm?: boolean;
  ack?: boolean;
}

const STATE: Record<string, string> = {
  running: "bg-ok/15 text-ok",
  partial: "bg-warn/15 text-warn",
  stopped: "bg-track text-ink-2",
  failed: "bg-bad/12 text-bad",
  saved: "bg-track text-ink-2",
};

function asGate(e: unknown): GateBody | null {
  if (!(e instanceof ApiError) || e.status !== 409 || !e.body || typeof e.body !== "object") return null;
  return e.body as GateBody;
}

function jobStack(job: InstallJob | null): string {
  if (!job) return "";
  const parts = job.name.split(":");
  return parts[0] === "stack" ? parts[1] ?? "" : "";
}

export default function StacksPage({
  focus,
  onFocusHandled,
}: {
  focus?: string | null;
  onFocusHandled?: () => void;
}) {
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [open, setOpen] = useState<string | "new" | null>(null);
  const [stack, setStack] = useState<Stack | null>(null);
  const [name, setName] = useState("");
  const [compose, setCompose] = useState("");
  const [env, setEnv] = useState("");
  const [isolate, setIsolate] = useState(true);
  const [rewrite, setRewrite] = useState(false);
  const [source, setSource] = useState("paste");
  const [gitUrl, setGitUrl] = useState("");
  const [gitRef, setGitRef] = useState("");
  const [gitPath, setGitPath] = useState("");
  const [showGit, setShowGit] = useState(false);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [gate, setGate] = useState<GateBody | null>(null);
  const [busy, setBusy] = useState("");
  const [job, setJob] = useState<InstallJob | null>(null);
  const [logsFor, setLogsFor] = useState<{ name: string; service: string } | null>(null);
  const [intent, setIntent] = useState<"deploy" | "update">("deploy");
  const [publishFor, setPublishFor] = useState<ServiceInfo | null>(null);
  const [removeFor, setRemoveFor] = useState<string | null>(null);
  const handled = useRef(onFocusHandled);
  handled.current = onFocusHandled;
  const composeInstall = useInstall("compose", () => {
    setNotice("Docker Compose is installed.");
    void refresh();
  });

  const refresh = async () => {
    try {
      const cat = await api<Catalog>("/stacks");
      setCatalog(cat);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  useEffect(() => {
    void refresh();
    const t = setInterval(() => void refresh(), 5000);
    return () => clearInterval(t);
  }, []);

  useEffect(() => {
    if (!focus) return;
    let gone = false;
    api<Stack>(`/stacks/${encodeURIComponent(focus)}`)
      .then((st) => {
        if (!gone) fill(st);
      })
      .catch((e) => {
        if (!gone) setError(errorMessage(e));
      })
      .finally(() => handled.current?.());
    return () => {
      gone = true;
    };
  }, [focus]);

  useEffect(() => {
    if (!job || (job.state !== "queued" && job.state !== "running")) return;
    let gone = false;
    const tick = () => {
      api<InstallJob>(`/stacks/jobs/${job.id}`)
        .then((next) => {
          if (!gone) setJob(next);
        })
        .catch(() => {});
    };
    const t = setInterval(tick, 700);
    return () => {
      gone = true;
      clearInterval(t);
    };
  }, [job?.id, job?.state]);

  useEffect(() => {
    if (!job || (job.state !== "succeeded" && job.state !== "failed")) return;
    void refresh();
    const stackName = jobStack(job);
    if (job.state === "succeeded" && stackName && !job.name.endsWith(":remove")) {
      void load(stackName).catch((e) => setError(errorMessage(e)));
    }
    if (job.state === "succeeded" && job.name.endsWith(":remove") && open === jobStack(job)) {
      setOpen(null);
      setStack(null);
    }
  }, [job?.id, job?.state]);

  function fill(st: Stack) {
    setOpen(st.name);
    setStack(st);
    setName(st.name);
    setCompose(st.compose);
    setEnv(st.env ?? "");
    setIsolate(st.isolate);
    setSource(st.source || "paste");
    setGitUrl(st.gitUrl ?? "");
    setGitRef(st.gitRef ?? "");
    setGitPath(st.gitPath ?? "");
    setRewrite(false);
    setGate(null);
    setPreview(null);
  }

  async function load(stackName: string) {
    const st = await api<Stack>(`/stacks/${encodeURIComponent(stackName)}`);
    fill(st);
  }

  function startNew() {
    setOpen("new");
    setStack(null);
    setName("");
    setCompose("");
    setEnv("");
    setIsolate(true);
    setSource("paste");
    setGitUrl("");
    setGitRef("");
    setGitPath("");
    setRewrite(false);
    setShowGit(false);
    setGate(null);
    setPreview(null);
  }

  const previewGen = useRef(0);
  useEffect(() => {
    if (open == null) return;
    const id = ++previewGen.current;
    if (!compose.trim()) {
      setPreview(null);
      return;
    }
    const t = setTimeout(() => {
      api<Preview>("/stacks/preview", {
        method: "POST",
        body: {
          name: open === "new" ? name.trim().toLowerCase() : open,
          compose,
          env,
          isolate,
          rewrite,
        },
      })
        .then((res) => {
          if (previewGen.current !== id) return;
          setPreview(res);
          if (res.changed && res.compose && res.compose !== compose) setCompose(res.compose);
        })
        .catch((e) => {
          if (previewGen.current !== id) return;
          setPreview({ valid: false, error: errorMessage(e), compose, services: [], findings: [], isolate });
        });
    }, 350);
    return () => clearTimeout(t);
  }, [open, name, compose, env, isolate, rewrite]);

  const working = !!job && (job.state === "queued" || job.state === "running");
  const findings = preview?.findings ?? gate?.findings ?? [];
  const diff = preview?.diff || gate?.diff || "";
  const high = findings.some((f) => f.severity === "high");
  const summary = catalog?.stacks.find((s) => s.name === (open === "new" ? "" : open));
  const services = summary?.services?.length ? summary.services : stack?.services ?? [];

  async function applyFix(f: Finding) {
    setBusy("fix");
    setError(null);
    try {
      const res = await api<Preview>("/stacks/preview", {
        method: "POST",
        body: {
          name: open === "new" ? name.trim().toLowerCase() : open,
          compose,
          env,
          isolate,
          rewrite,
          fixes: [{ service: f.service, id: f.id }],
        },
      });
      if (res.compose) setCompose(res.compose);
      setPreview(res);
      setGate(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  }

  function fileBody(deploy: boolean, anyway: boolean) {
    return {
      name: name.trim().toLowerCase(),
      compose,
      env,
      isolate,
      source,
      gitUrl,
      gitPath,
      gitRef,
      deploy,
      confirm: Boolean(diff),
      deployAnyway: anyway,
    };
  }

  async function save() {
    setBusy("save");
    setError(null);
    setGate(null);
    try {
      if (open === "new") {
        const st = await api<Stack>("/stacks", { method: "POST", body: fileBody(false, false) });
        setNotice(`${st.name} was saved. Deploy it when you are ready.`);
        await refresh();
        fill(st);
      } else if (open) {
        const st = await api<Stack>(`/stacks/${encodeURIComponent(open)}`, {
          method: "PUT",
          body: fileBody(false, false),
        });
        setNotice(`${st.name} was saved.`);
        fill(st);
        await refresh();
      }
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  }

  async function deploy(anyway = false) {
    setBusy(anyway ? "anyway" : "deploy");
    setError(null);
    setIntent("deploy");
    try {
      const path = open === "new" ? "/stacks" : `/stacks/${encodeURIComponent(open || "")}/deploy`;
      const next = await api<InstallJob>(path, { method: "POST", body: fileBody(true, anyway) });
      setGate(null);
      setJob(next);
      setNotice(null);
      await refresh();
    } catch (e) {
      const g = asGate(e);
      if (g) setGate(g);
      else setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  }

  async function act(stackName: string, verb: "update" | "stop", anyway = false) {
    setBusy(verb);
    setError(null);
    try {
      const next = await api<InstallJob>(`/stacks/${encodeURIComponent(stackName)}/${verb}`, {
        method: "POST",
        body: verb === "update" ? { deployAnyway: anyway } : {},
      });
      setJob(next);
      setGate(null);
      if (verb === "update") setIntent("update");
    } catch (e) {
      const g = asGate(e);
      if (g) {
        if (verb === "update") setIntent("update");
        if (open !== stackName) await load(stackName).catch(() => {});
        setGate(g);
      } else setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  }

  async function importGit() {
    setBusy("git");
    setError(null);
    try {
      const got = await api<{ compose: string; env?: string; path: string; url: string; ref?: string; notes?: string[] }>(
        "/stacks/import",
        { method: "POST", body: { url: gitUrl.trim(), ref: gitRef.trim(), path: gitPath.trim(), rewrite } },
      );
      setCompose(got.compose);
      setEnv(got.env ?? "");
      setSource("git");
      setGitUrl(got.url);
      setGitPath(got.path);
      setGitRef(got.ref ?? gitRef);
      setShowGit(false);
      setNotice(got.notes?.length ? got.notes.join(" ") : "The compose file was fetched. It is not saved until you deploy.");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  }

  async function readFile(file: File, into: "compose" | "env") {
    const text = await file.text();
    if (into === "compose") {
      setCompose(text);
      setSource("upload");
    } else {
      setEnv(text);
    }
  }

  const stacks = catalog?.stacks ?? [];

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Stacks</h1>
          <p className="mt-2 max-w-2xl text-sm font-semibold text-ink-2">
            Deploy a Docker Compose file. Each stack is saved on disk and, by default, put on its own network.
          </p>
        </div>
        <Button variant="primary" onClick={startNew}>
          <Plus size={16} strokeWidth={2.75} />
          New stack
        </Button>
      </header>

      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}

      {catalog && (
        <p className="px-1 text-xs font-semibold text-ink-3">
          Files live in <span className="font-mono text-ink-2">{catalog.root}</span>
          {catalog.composeOk ? ` · Compose ${catalog.compose}` : " · Compose is not installed yet"}
          . Backups include this folder.
        </p>
      )}

      {catalog && !catalog.composeOk && (
        <Card className="space-y-4">
          <div>
            <h2 className="text-lg font-extrabold text-ink">Docker Compose is not installed</h2>
            <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
              You can still write and check a stack. Deploy, update, stop and logs need the Compose v2 plugin
              (`docker compose`).
            </p>
          </div>
          {!composeInstall.active && (
            <Button variant="primary" onClick={() => void composeInstall.start()} loading={composeInstall.starting}>
              <Download size={16} />
              Install Compose
            </Button>
          )}
          {composeInstall.error && <Alert>{composeInstall.error}</Alert>}
          {showInstall(composeInstall.job) && composeInstall.job && (
            <InstallPanel job={composeInstall.job} onRetry={() => void composeInstall.start()} busy={composeInstall.starting} />
          )}
        </Card>
      )}

      {job && (working || job.state === "failed" || showInstall(job)) && (
        <Card>
          <InstallPanel job={job} kind="job" />
        </Card>
      )}

      {open != null && (
        <Card className="space-y-5">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 className="text-lg font-extrabold text-ink">{open === "new" ? "New stack" : open}</h2>
              <p className="mt-1 text-xs font-semibold text-ink-3">
                {preview?.project || stack?.project || "The project name is feldcore- and the stack name."}
                {preview?.network ? ` · network ${preview.network}` : stack?.network ? ` · network ${stack.network}` : ""}
              </p>
            </div>
            <Button size="sm" onClick={() => { setOpen(null); setStack(null); setGate(null); }}>
              Close
            </Button>
          </div>

          {open === "new" && (
            <TextField
              label="Name"
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
              placeholder="media"
              hint="Lowercase letters, numbers and hyphens. This becomes the folder and the network."
              autoComplete="off"
              disabled={working}
            />
          )}

          <div className="flex flex-wrap items-center gap-2">
            <label className="inline-flex cursor-pointer items-center gap-2 rounded-xl border border-line bg-surface-2 px-3 py-2 text-xs font-bold text-ink">
              <Upload size={14} />
              Upload compose
              <input
                type="file"
                accept=".yml,.yaml,text/yaml,application/yaml"
                className="sr-only"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) void readFile(file, "compose");
                  e.target.value = "";
                }}
              />
            </label>
            <label className="inline-flex cursor-pointer items-center gap-2 rounded-xl border border-line bg-surface-2 px-3 py-2 text-xs font-bold text-ink">
              <FileUp size={14} />
              Upload .env
              <input
                type="file"
                className="sr-only"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) void readFile(file, "env");
                  e.target.value = "";
                }}
              />
            </label>
            <Button size="sm" onClick={() => setShowGit((v) => !v)}>
              <GitBranch size={14} />
              Git repository
            </Button>
          </div>

          {showGit && (
            <div className="grid gap-3 sm:grid-cols-2">
              <TextField
                className="sm:col-span-2"
                label="Repository URL"
                value={gitUrl}
                onChange={(e) => setGitUrl(e.target.value)}
                placeholder="https://github.com/org/app.git"
                hint="https only. The clone is deleted after the file is read."
              />
              <TextField label="Branch, tag or commit" value={gitRef} onChange={(e) => setGitRef(e.target.value)} placeholder="main" />
              <TextField label="Path in the repository" value={gitPath} onChange={(e) => setGitPath(e.target.value)} placeholder="docker-compose.yml" />
              <div className="sm:col-span-2">
                <Button variant="primary" onClick={() => void importGit()} loading={busy === "git"}>
                  Fetch compose file
                </Button>
              </div>
            </div>
          )}

          <label className="block">
            <span className="mb-1.5 block text-xs font-bold text-ink-2">Compose file</span>
            <textarea
              value={compose}
              onChange={(e) => setCompose(e.target.value)}
              disabled={working}
              spellCheck={false}
              placeholder={"services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\""}
              className="min-h-64 w-full rounded-2xl border border-line bg-surface-2 px-4 py-3 font-mono text-xs leading-5 text-ink"
            />
          </label>
          {preview && !preview.valid && preview.error && <Alert>{preview.error}</Alert>}

          <label className="block">
            <span className="mb-1.5 block text-xs font-bold text-ink-2">Environment file</span>
            <textarea
              value={env}
              onChange={(e) => setEnv(e.target.value)}
              disabled={working}
              spellCheck={false}
              placeholder={"TZ=UTC\nPUID=1000"}
              className="min-h-24 w-full rounded-2xl border border-line bg-surface-2 px-4 py-3 font-mono text-xs leading-5 text-ink"
            />
          </label>

          <div className="flex flex-wrap items-center justify-between gap-4 rounded-2xl border border-line px-4 py-3">
            <div>
              <p className="text-sm font-bold text-ink">Private network</p>
              <p className="mt-1 max-w-xl text-xs font-medium leading-relaxed text-ink-3">
                {preview?.ownNetworks
                  ? "This file defines its own networks, so those are used. They stay private to this stack unless one is marked external."
                  : isolate
                    ? "Services join a bridge named feldcore-stack- followed by the stack name. Other containers cannot reach them unless you publish a port."
                    : "Services join the shared Docker bridge, so other containers on that network can reach them."}
              </p>
            </div>
            <Toggle checked={isolate} onChange={setIsolate} label="Private network" disabled={working || preview?.ownNetworks} />
          </div>

          <label className="flex items-start gap-3 text-sm font-medium text-ink">
            <input
              type="checkbox"
              className="mt-1"
              checked={rewrite}
              onChange={(e) => setRewrite(e.target.checked)}
              disabled={working}
            />
            <span>
              Rewrite Unraid paths
              <span className="mt-1 block text-xs font-medium text-ink-3">
                /mnt/user, /mnt/user0, /mnt/cache and /mnt/diskN are mapped onto this server's shares. Portainer and
                Unraid labels are kept.
              </span>
            </span>
          </label>

          {preview?.notes?.map((n) => (
            <Alert key={n} tone="info">{n}</Alert>
          ))}
          {stack?.replaces && (
            <Alert tone="warn">
              Deploying this stack removes the original container. Mapped folders stay on disk.
            </Alert>
          )}
          {stack?.notes?.map((n) => (
            <Alert key={n} tone="info">{n}</Alert>
          ))}

          {findings.length > 0 && (
            <div className="space-y-3">
              <h3 className="text-sm font-extrabold text-ink">Security check</h3>
              {findings.map((f) => (
                <div
                  key={`${f.service}:${f.id}`}
                  className={`rounded-2xl border px-4 py-3 ${f.severity === "high" ? "border-bad/40 bg-bad/8" : "border-warn/40 bg-warn/10"}`}
                >
                  <p className="text-sm font-extrabold text-ink">
                    {f.title}
                    <span className="ml-2 text-xs font-bold uppercase tracking-wider text-ink-3">{f.service}</span>
                  </p>
                  <p className="mt-1 text-sm font-medium text-ink-2">{f.reason}</p>
                  <p className="mt-1 text-xs font-semibold text-ink-3">{f.suggestion}</p>
                  {f.fix && (
                    <Button size="sm" className="mt-3" onClick={() => void applyFix(f)} loading={busy === "fix"} disabled={working}>
                      Apply safer setting
                    </Button>
                  )}
                </div>
              ))}
            </div>
          )}

          {diff && (
            <div>
              <h3 className="text-sm font-extrabold text-ink">Changes since the saved file</h3>
              <pre className="mt-2 max-h-72 overflow-auto rounded-2xl bg-[#07101f] px-4 py-3 font-mono text-[11px] leading-5 text-[#d5deef]">
                {diff}
              </pre>
            </div>
          )}
          {gate?.error && <Alert tone={gate.ack ? "warn" : "info"}>{gate.error}</Alert>}

          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void save()} loading={busy === "save"} disabled={working || !compose.trim()}>
              Save
            </Button>
            <Button variant="primary" onClick={() => void deploy(false)} loading={busy === "deploy"} disabled={working || !compose.trim() || preview?.valid === false}>
              <Play size={15} />
              {diff ? "Deploy these changes" : "Deploy"}
            </Button>
            {high && (
              <Button variant="danger" onClick={() => void deploy(true)} loading={busy === "anyway"} disabled={working || !compose.trim()}>
                Deploy anyway
              </Button>
            )}
            {open !== "new" && (
              <>
                <Button
                  onClick={() => void act(open, "update", intent === "update" && !!gate?.ack)}
                  loading={busy === "update"}
                  disabled={working}
                  variant={intent === "update" && gate?.ack ? "danger" : "secondary"}
                >
                  <RefreshCw size={15} />
                  {intent === "update" && gate?.ack ? "Update anyway" : "Update"}
                </Button>
                <Button onClick={() => void act(open, "stop")} loading={busy === "stop"} disabled={working}>
                  <Square size={15} />
                  Stop
                </Button>
              </>
            )}
          </div>

          {open !== "new" && services.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-sm font-extrabold text-ink">Services</h3>
              {services.map((svc) => (
                <div key={svc.name} className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-line px-4 py-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-extrabold text-ink">{svc.name}</p>
                    <p className="truncate text-xs font-medium text-ink-3">{svc.image || svc.status || "Not running"}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    {svc.state && (
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${STATE[svc.state] ?? "bg-track text-ink-2"}`}>
                        {svc.state}
                      </span>
                    )}
                    <Button size="sm" onClick={() => setLogsFor({ name: open, service: svc.name })}>Logs</Button>
                    <Button size="sm" disabled={!svc.id} onClick={() => setPublishFor(svc)} title={svc.id ? "Publish this service" : "Start the stack before publishing"}>
                      Publish
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      {stacks.length === 0 && open == null ? (
        <Card className="flex flex-col items-center px-6 py-12 text-center">
          <span className="flex h-16 w-16 items-center justify-center rounded-3xl bg-accent/12 text-accent">
            <Layers size={28} />
          </span>
          <h2 className="mt-5 text-lg font-extrabold text-ink">No stacks yet</h2>
          <p className="mt-2 max-w-md text-sm font-medium leading-relaxed text-ink-2">
            Paste a compose file, upload one, or fetch it from a Git repository. You can also turn a container on the
            Docker page into a stack.
          </p>
          <Button variant="primary" className="mt-6" onClick={startNew}>
            <Plus size={16} strokeWidth={2.75} />
            New stack
          </Button>
        </Card>
      ) : (
        <div className="space-y-3">
          {stacks.map((s) => (
            <Card key={s.name} className="flex flex-col gap-4 sm:flex-row sm:items-center">
              <button type="button" className="min-w-0 flex-1 text-left" onClick={() => void load(s.name).catch((e) => setError(errorMessage(e)))}>
                <div className="flex items-center gap-2">
                  <h2 className="truncate text-base font-extrabold text-ink">{s.name}</h2>
                  <span className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${STATE[s.state] ?? "bg-track text-ink-2"}`}>
                    {s.state}
                  </span>
                  {s.isolate && (
                    <span className="rounded-full bg-ok/15 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-ok">
                      Private
                    </span>
                  )}
                </div>
                <p className="mt-1 truncate text-xs font-medium text-ink-3">
                  {s.services.map((svc) => svc.name).join(", ") || "No services"}
                  {s.network ? ` · ${s.network}` : ""}
                </p>
              </button>
              <div className="flex shrink-0 items-center gap-1">
                <IconButton label="Update" onClick={() => void act(s.name, "update")} disabled={working}>
                  <RefreshCw size={16} />
                </IconButton>
                {s.state === "running" || s.state === "partial" ? (
                  <IconButton label="Stop" onClick={() => void act(s.name, "stop")} disabled={working}>
                    <Square size={16} />
                  </IconButton>
                ) : (
                  <IconButton
                    label="Deploy"
                    disabled={working}
                    onClick={() => {
                      setIntent("deploy");
                      void api<InstallJob>(`/stacks/${encodeURIComponent(s.name)}/deploy`, { method: "POST", body: {} })
                        .then((next) => {
                          setGate(null);
                          setJob(next);
                        })
                        .catch(async (e) => {
                          const g = asGate(e);
                          if (g) {
                            await load(s.name).catch(() => {});
                            setGate(g);
                          } else setError(errorMessage(e));
                        });
                    }}
                  >
                    <Play size={16} />
                  </IconButton>
                )}
                <IconButton label="Logs" onClick={() => setLogsFor({ name: s.name, service: "" })}>
                  <FileText size={16} />
                </IconButton>
                <IconButton label="Remove" onClick={() => setRemoveFor(s.name)} disabled={working}>
                  <Trash2 size={16} />
                </IconButton>
              </div>
            </Card>
          ))}
        </div>
      )}

      {logsFor && (
        <LogsDialog name={logsFor.name} service={logsFor.service} onClose={() => setLogsFor(null)} />
      )}
      {publishFor?.id && (
        <Modal title={`Publish ${publishFor.name}`} onClose={() => setPublishFor(null)} size="lg">
          <PublishForm containerId={publishFor.id} containerName={publishFor.container || publishFor.name} />
        </Modal>
      )}
      {removeFor && (
        <RemoveDialog
          name={removeFor}
          busy={busy === "remove"}
          onClose={() => setRemoveFor(null)}
          onRemove={async (volumes) => {
            setBusy("remove");
            setError(null);
            try {
              const next = await api<InstallJob>(
                `/stacks/${encodeURIComponent(removeFor)}${volumes ? "?volumes=1" : ""}`,
                { method: "DELETE" },
              );
              setJob(next);
              setRemoveFor(null);
            } catch (e) {
              setError(errorMessage(e));
            } finally {
              setBusy("");
            }
          }}
        />
      )}
    </div>
  );
}

function LogsDialog({ name, service, onClose }: { name: string; service: string; onClose: () => void }) {
  const [log, setLog] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [svc, setSvc] = useState(service);
  const load = async (which: string) => {
    try {
      const q = which ? `?service=${encodeURIComponent(which)}&tail=200` : "?tail=200";
      const res = await api<{ log: string }>(`/stacks/${encodeURIComponent(name)}/logs${q}`);
      setLog(res.log || "No log lines yet.");
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  useEffect(() => {
    void load(svc);
  }, [name, svc]);
  return (
    <Modal
      title={svc ? `${name} · ${svc}` : `${name} logs`}
      onClose={onClose}
      size="xl"
      footer={
        <>
          <Button onClick={() => void load(svc)}>Refresh</Button>
          <Button variant="primary" onClick={onClose}>Close</Button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <TextField label="Service" value={svc} onChange={(e) => setSvc(e.target.value)} hint="Leave empty for the whole stack." />
      <pre className="mt-4 max-h-[50vh] overflow-auto rounded-2xl bg-[#07101f] px-4 py-3 font-mono text-[11px] leading-5 whitespace-pre-wrap break-words text-[#d5deef]">
        {log || "Loading…"}
      </pre>
    </Modal>
  );
}

function RemoveDialog({
  name,
  busy,
  onClose,
  onRemove,
}: {
  name: string;
  busy: boolean;
  onClose: () => void;
  onRemove: (volumes: boolean) => void;
}) {
  const [volumes, setVolumes] = useState(false);
  return (
    <Modal
      title={`Remove ${name}`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={() => onRemove(volumes)}>
            Remove stack
          </Button>
        </>
      }
    >
      <p className="text-sm font-medium leading-relaxed text-ink-2">
        This stops the containers and deletes the stack files. Folders you mapped from the server stay where they are.
      </p>
      <label className="mt-4 flex items-start gap-3 text-sm font-medium text-ink">
        <input type="checkbox" className="mt-1" checked={volumes} onChange={(e) => setVolumes(e.target.checked)} />
        <span>
          Also delete named volumes
          <span className="mt-1 block text-xs font-medium text-ink-3">
            Only volumes Compose created for this stack. Bind mounts are not deleted.
          </span>
        </span>
      </label>
    </Modal>
  );
}
