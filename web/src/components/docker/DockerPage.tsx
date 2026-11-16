import { useCallback, useEffect, useState } from "react";
import {
  Boxes,
  Container as ContainerIcon,
  Download,
  ExternalLink,
  FileText,
  Pencil,
  Play,
  Plus,
  RotateCw,
  Square,
  Store,
  Trash2,
  X,
} from "lucide-react";
import type { Info } from "../../types";
import { api, errorMessage } from "../../lib/api";
import { ago, bytes } from "../../lib/format";
import { Card, IconBadge } from "../ui";
import { Alert, Button, IconButton, Modal } from "../form";
import Allocations from "./Allocations";
import ContainerDialog from "./ContainerDialog";
import LogsDialog from "./LogsDialog";
import { TerminalButton, useTerminal } from "../Terminal";
import { stateStyle, type Container, type DockerStatus } from "./types";

export default function DockerPage({ onOpenApps }: { onOpenApps?: () => void }) {
  const [status, setStatus] = useState<DockerStatus | null>(null);
  const [containers, setContainers] = useState<Container[]>([]);
  const [host, setHost] = useState("localhost");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // The container open in the dialog, "new" to create one, or null when closed.
  const [editing, setEditing] = useState<Container | "new" | null>(null);
  const setCreating = (on: boolean) => setEditing(on ? "new" : null);
  const [logsFor, setLogsFor] = useState<Container | null>(null);
  const [removing, setRemoving] = useState<Container | null>(null);
  const { openContainer } = useTerminal();

  const load = useCallback(async () => {
    try {
      const st = await api<DockerStatus>("/docker/status");
      setStatus(st);
      if (st.running) {
        const res = await api<{ containers: Container[] }>("/docker/containers");
        setContainers(res.containers ?? []);
      }
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 5000);
    return () => clearInterval(t);
  }, [load]);

  useEffect(() => {
    api<Info>("/system/info")
      .then((i) => setHost(i.hostname))
      .catch(() => {});
  }, []);

  const changed = (msg: string) => {
    setNotice(msg);
    void load();
  };

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Docker</h1>
          <p className="mt-2 text-sm font-semibold text-ink-2">
            Run and manage containers on this server.
          </p>
        </div>
        {status?.running && (
          <div className="flex flex-wrap gap-2">
            {onOpenApps && (
              <Button onClick={onOpenApps}>
                <Store size={16} strokeWidth={2.4} />
                App Store
              </Button>
            )}
            <Button variant="primary" onClick={() => setCreating(true)}>
              <Plus size={16} strokeWidth={2.75} />
              Run container
            </Button>
          </div>
        )}
      </header>

      {error && <Alert>{error}</Alert>}

      {notice && (
        <Alert
          tone="info"
          action={
            <IconButton label="Dismiss" onClick={() => setNotice(null)} className="-my-1.5 -mr-2">
              <X size={15} />
            </IconButton>
          }
        >
          {notice}
        </Alert>
      )}

      {status && !status.running && (
        <DockerUnavailable status={status} onInstalled={() => changed("Docker is installed and running.")} />
      )}

      {status?.running && (
        <>
          <div className="flex items-center gap-2 px-1 text-xs font-semibold text-ink-3">
            <span className="h-1.5 w-1.5 rounded-full bg-ok" />
            Docker {status.version} · {containers.length} container
            {containers.length === 1 ? "" : "s"}
          </div>

          {containers.length ? (
            <div className="space-y-3">
              {containers.map((c) => (
                <ContainerRow
                  key={c.id}
                  c={c}
                  host={host}
                  onAction={changed}
                  onError={setError}
                  onLogs={() => setLogsFor(c)}
                  onConsole={() => openContainer({ id: c.id, name: c.name, icon: c.icon })}
                  onEdit={() => setEditing(c)}
                  onRemove={() => setRemoving(c)}
                />
              ))}
            </div>
          ) : (
            <Card className="flex flex-col items-center px-6 py-12 text-center">
              <IconBadge icon={Boxes} size={64} />
              <h2 className="mt-5 text-lg font-extrabold text-ink">No containers yet</h2>
              <p className="mt-2 max-w-sm text-sm font-medium leading-relaxed text-ink-2">
                Run your first container from any Docker image. FELDCORE pulls the image and starts
                it for you.
              </p>
              <Button variant="primary" className="mt-6" onClick={() => setCreating(true)}>
                <Plus size={16} strokeWidth={2.75} />
                Run a container
              </Button>
            </Card>
          )}

          {containers.length > 0 && <Allocations containers={containers} />}
        </>
      )}

      {editing && (
        <ContainerDialog
          container={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={(name, edited) => {
            setEditing(null);
            changed(edited ? `${name} was updated.` : `${name} was created.`);
          }}
        />
      )}
      {logsFor && <LogsDialog container={logsFor} onClose={() => setLogsFor(null)} />}
      {removing && (
        <RemoveDialog
          container={removing}
          onClose={() => setRemoving(null)}
          onRemoved={(message) => {
            setRemoving(null);
            changed(message);
          }}
        />
      )}
    </div>
  );
}

function ContainerRow({
  c,
  host,
  onAction,
  onError,
  onLogs,
  onConsole,
  onEdit,
  onRemove,
}: {
  c: Container;
  host: string;
  onAction: (msg: string) => void;
  onError: (msg: string) => void;
  onLogs: () => void;
  onConsole: () => void;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const [busy, setBusy] = useState("");
  const st = stateStyle(c.state);
  const running = c.state === "running";
  const ports = c.ports ?? [];

  const act = async (verb: "start" | "stop" | "restart") => {
    setBusy(verb);
    try {
      await api(`/docker/containers/${encodeURIComponent(c.id)}/${verb}`, { method: "POST" });
      onAction(`${c.name} ${verb === "stop" ? "stopped" : verb === "start" ? "started" : "restarted"}.`);
    } catch (e) {
      onError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const webUrl = c.webUrl.replace("HOST", host);

  return (
    <Card className="flex flex-col gap-4 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 items-center gap-3.5">
        <ContainerMark src={c.icon} muted={!running} />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-base font-extrabold text-ink">{c.name}</h2>
            {c.managed && (
              <span className="rounded-full bg-accent/12 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-accent">
                FELDCORE
              </span>
            )}
            {c.gpu && (
              <span className="rounded-full bg-ok/15 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-ok">
                GPU
              </span>
            )}
            {c.vpn && (
              <span className="rounded-full bg-accent/12 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-accent">
                via VPN
              </span>
            )}
            {c.vpnGateway && (
              <span className="rounded-full bg-accent/12 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-accent">
                VPN gateway
              </span>
            )}
          </div>
          <p className="truncate text-xs font-medium text-ink-3">{c.image}</p>
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs font-semibold">
            <span className={`inline-flex items-center gap-1.5 ${st.cls}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${st.dot}`} />
              {st.label}
            </span>
            <span className="text-ink-3">{running ? c.status : ago(c.created)}</span>
            {running && (
              <>
                <span className="text-ink-3">{c.cpuPct.toFixed(1)}% CPU</span>
                <span className="text-ink-3">{bytes(c.memUsed)} RAM</span>
              </>
            )}
          </div>
          {ports.length > 0 && (
            <div className="mt-1.5 flex flex-wrap gap-1.5">
              {ports.map((p) => (
                <span
                  key={`${p.host}/${p.proto}`}
                  className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-2"
                >
                  {p.host}→{p.container}/{p.proto}
                </span>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-1">
        {running && webUrl && (
          <IconButton label="Open in browser" onClick={() => window.open(webUrl, "_blank")}>
            <ExternalLink size={16} />
          </IconButton>
        )}
        <TerminalButton label="Console" disabled={!running} onClick={onConsole} />
        <IconButton label="View logs" onClick={onLogs}>
          <FileText size={16} />
        </IconButton>
        <IconButton label={c.vpnGateway ? "Managed from Settings → VPN" : "Edit"} onClick={onEdit} disabled={!!busy || c.vpnGateway}>
          <Pencil size={16} />
        </IconButton>
        {running ? (
          <>
            <IconButton label="Restart" onClick={() => act("restart")} disabled={!!busy}>
              <RotateCw size={16} className={busy === "restart" ? "animate-spin" : ""} />
            </IconButton>
            <IconButton label="Stop" onClick={() => act("stop")} disabled={!!busy}>
              <Square size={16} />
            </IconButton>
          </>
        ) : (
          <IconButton label="Start" onClick={() => act("start")} disabled={!!busy}>
            <Play size={16} />
          </IconButton>
        )}
        <IconButton label="Remove" onClick={onRemove} disabled={!!busy}>
          <Trash2 size={16} />
        </IconButton>
      </div>
    </Card>
  );
}

function ContainerMark({ src, muted }: { src?: string; muted?: boolean }) {
  const [failed, setFailed] = useState(false);
  if (src && !failed) {
    return (
      <img
        src={src}
        alt=""
        referrerPolicy="no-referrer"
        onError={() => setFailed(true)}
        className={`h-12 w-12 shrink-0 rounded-2xl bg-surface-2 object-contain ${muted ? "opacity-60" : ""}`}
      />
    );
  }
  return <IconBadge icon={ContainerIcon} muted={muted} />;
}

function DockerUnavailable({
  status,
  onInstalled,
}: {
  status: DockerStatus;
  onInstalled: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const install = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/docker/install", { method: "POST" });
      onInstalled();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="flex flex-col items-center px-6 py-12 text-center">
      <IconBadge icon={Boxes} size={64} muted />
      <h2 className="mt-5 text-lg font-extrabold text-ink">
        {status.installed ? "Docker isn't running" : "Docker isn't installed"}
      </h2>
      <p className="mt-2 max-w-md text-sm font-medium leading-relaxed text-ink-2">
        {status.message ||
          "Install Docker to run containers from this page. FELDCORE installs your distribution's Docker package and starts it."}
      </p>
      {status.canInstall ? (
        <Button variant="primary" className="mt-6" onClick={install} loading={busy}>
          {!busy && <Download size={15} strokeWidth={2.75} />}
          {busy ? "Installing Docker…" : "Install Docker"}
        </Button>
      ) : (
        !status.installed && (
          <p className="mt-6 text-xs font-semibold text-ink-3">
            FELDCORE needs to run as root to install Docker.
          </p>
        )
      )}
      {busy && (
        <p className="mt-3 text-xs font-medium text-ink-3">
          This downloads a few hundred MB and can take a couple of minutes.
        </p>
      )}
      {error && (
        <div className="mt-4 w-full max-w-md">
          <Alert>{error}</Alert>
        </div>
      )}
    </Card>
  );
}

function RemoveDialog({
  container,
  onClose,
  onRemoved,
}: {
  container: Container;
  onClose: () => void;
  onRemoved: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [removeImage, setRemoveImage] = useState(false);
  useEffect(() => {
    api<{ defaults?: { removeImage?: boolean } }>("/settings")
      .then((s) => setRemoveImage(Boolean(s.defaults?.removeImage)))
      .catch(() => {});
  }, []);
  const [error, setError] = useState<string | null>(null);
  const running = container.state === "running";

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      const q = new URLSearchParams({ force: running ? "1" : "0" });
      if (removeImage) q.set("image", "1");
      const res = await api<{ message?: string }>(
        `/docker/containers/${encodeURIComponent(container.id)}?${q}`,
        { method: "DELETE" },
      );
      onRemoved(res.message || `${container.name} was removed.`);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Modal
      title={`Remove ${container.name}?`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="danger" onClick={confirm} loading={busy}>
            {running ? "Stop & remove" : "Remove"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-sm font-medium leading-relaxed text-ink-2">
          {running && "This container is running and will be stopped first. "}
          The container is deleted. Folders you mapped in stay on the server. Anything saved only
          inside the container is lost.
        </p>
        <label className="flex items-center gap-2.5 text-sm font-semibold text-ink">
          <input
            type="checkbox"
            checked={removeImage}
            onChange={(e) => setRemoveImage(e.target.checked)}
            disabled={busy}
            className="h-4 w-4 accent-accent"
          />
          Also remove image
        </label>
        <p className="text-xs font-medium leading-relaxed text-ink-3">
          Removing the image frees the disk space it uses. The app&apos;s settings and any mapped
          folders — app data, saves and media — are not touched. Installing it again downloads the
          image once more, which takes longer. If another container is still using that image, it
          is left in place.
        </p>
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}
