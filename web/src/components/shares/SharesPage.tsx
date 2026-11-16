import { useCallback, useEffect, useState } from "react";
import {
  Check,
  Copy,
  Download,
  Folder,
  FolderOpen,
  FolderPlus,
  Pencil,
  Plus,
  Server,
  Trash2,
  X,
  type LucideIcon,
} from "lucide-react";
import type { Info } from "../../types";
import { api, errorMessage } from "../../lib/api";
import { Card, IconBadge } from "../ui";
import { Alert, Button, IconButton, Modal } from "../form";
import FileBrowser from "./FileBrowser";
import ShareDialog from "./ShareDialog";
import UsersCard from "./UsersCard";
import { ACCESS_LABEL, type Overview, type ProtoStatus, type ServiceStatus, type Share } from "./types";

export default function SharesPage() {
  const [data, setData] = useState<Overview | null>(null);
  const [host, setHost] = useState("server");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState<Share | "new" | null>(null);
  const [deleting, setDeleting] = useState<Share | null>(null);
  const [browsing, setBrowsing] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setData(await api<Overview>("/shares"));
      setLoadError(null);
    } catch (e) {
      setLoadError(errorMessage(e));
    }
  }, []);

  // Service state changes outside the page (a restart, an install), so poll.
  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 8000);
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

  if (browsing) {
    return <FileBrowser name={browsing} onBack={() => setBrowsing(null)} />;
  }

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Shares</h1>
          <p className="mt-2 text-sm font-semibold text-ink-2">
            Folders on this server that other computers on your network can open.
          </p>
        </div>
        <Button variant="primary" onClick={() => setEditing("new")} disabled={!data}>
          <Plus size={16} strokeWidth={2.75} />
          New share
        </Button>
      </header>

      {loadError && <Alert>{loadError}</Alert>}

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

      {data && !data.services.privileged && (
        <Alert tone="warn">
          FELDCORE isn't running as root, so it can't change file sharing on this server. You can
          look around, but shares won't go live. Run the daemon as root (the installer does this).
        </Alert>
      )}

      {!data && !loadError && (
        <p className="py-10 text-center text-sm font-medium text-ink-3">Loading shares…</p>
      )}

      {data && (
        <>
          <section className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <ServiceCard
              service="smb"
              title="Windows file sharing"
              sub="SMB, provided by Samba"
              status={data.services.smb}
              onInstalled={() => changed("Samba is installed and ready.")}
            />
            <ServiceCard
              service="nfs"
              title="NFS"
              sub="For Linux machines and hypervisors"
              status={data.services.nfs}
              onInstalled={() => changed("The NFS server is installed and ready.")}
            />
          </section>

          {data.shares.length ? (
            <section className="grid grid-cols-1 gap-4 xl:grid-cols-2">
              {data.shares.map((s) => (
                <ShareCard
                  key={s.name}
                  share={s}
                  host={host}
                  onEdit={() => setEditing(s)}
                  onDelete={() => setDeleting(s)}
                  onBrowse={() => setBrowsing(s.name)}
                />
              ))}
            </section>
          ) : (
            <Card className="flex flex-col items-center px-6 py-12 text-center">
              <IconBadge icon={FolderPlus} size={64} />
              <h2 className="mt-5 text-lg font-extrabold text-ink">No shares yet</h2>
              <p className="mt-2 max-w-sm text-sm font-medium leading-relaxed text-ink-2">
                A share is a folder on this server. Create one, then choose who can reach it from
                Windows, macOS or Linux.
              </p>
              <Button variant="primary" className="mt-6" onClick={() => setEditing("new")}>
                <Plus size={16} strokeWidth={2.75} />
                Create your first share
              </Button>
            </Card>
          )}

          <UsersCard overview={data} onChanged={changed} />
        </>
      )}

      {editing && data && (
        <ShareDialog
          share={editing === "new" ? undefined : editing}
          overview={data}
          onClose={() => setEditing(null)}
          onSaved={(name, created) => {
            setEditing(null);
            changed(created ? `${name} was created.` : `${name} was updated.`);
          }}
        />
      )}

      {deleting && (
        <DeleteShareDialog
          share={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={(kept) => {
            const s = deleting;
            setDeleting(null);
            changed(
              kept
                ? `${s.name} is no longer shared. Its files are still on the server in ${s.path}.`
                : `${s.name} was removed.`,
            );
          }}
        />
      )}
    </div>
  );
}

function ServiceCard({
  service,
  title,
  sub,
  status,
  onInstalled,
}: {
  service: "smb" | "nfs";
  title: string;
  sub: string;
  status: ServiceStatus;
  onInstalled: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const install = async () => {
    setBusy(true);
    setError(null);
    try {
      await api(`/shares/services/${service}/install`, { method: "POST" });
      onInstalled();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const pill = !status.installed
    ? { label: "Not installed", cls: "bg-track text-ink-2" }
    : status.active
      ? { label: "Running", cls: "bg-ok/15 text-ok" }
      : { label: "Stopped", cls: "bg-track text-ink-2" };

  return (
    <Card>
      <div className="flex items-center gap-3.5">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl bg-accent/12 text-accent">
          <Server size={18} />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-sm font-bold text-ink">{title}</h2>
          <p className="truncate text-xs font-medium text-ink-3">{sub}</p>
        </div>
        {status.installed || !status.canInstall ? (
          <span className={`shrink-0 rounded-full px-3 py-1 text-[11px] font-bold ${pill.cls}`}>
            {pill.label}
          </span>
        ) : (
          <Button size="sm" variant="primary" onClick={install} loading={busy}>
            {!busy && <Download size={13} strokeWidth={2.75} />}
            {busy ? "Installing…" : "Install"}
          </Button>
        )}
      </div>
      {busy && (
        <p className="mt-3 text-xs font-medium text-ink-3">
          Downloading and installing. This usually takes under a minute.
        </p>
      )}
      {(error || status.error) && (
        <p className="mt-3 text-xs font-semibold text-bad">{error || status.error}</p>
      )}
    </Card>
  );
}

function ShareCard({
  share,
  host,
  onEdit,
  onDelete,
  onBrowse,
}: {
  share: Share;
  host: string;
  onEdit: () => void;
  onDelete: () => void;
  onBrowse: () => void;
}) {
  return (
    <Card>
      <div className="flex items-start gap-4">
        <IconBadge icon={Folder} />
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-base font-extrabold text-ink">{share.name}</h2>
          <p className="truncate text-xs font-medium text-ink-3">
            {share.comment || "No description"}
          </p>
        </div>
        <div className="flex">
          <IconButton label={`Edit ${share.name}`} onClick={onEdit}>
            <Pencil size={16} />
          </IconButton>
          <IconButton label={`Delete ${share.name}`} onClick={onDelete}>
            <Trash2 size={16} />
          </IconButton>
        </div>
      </div>

      <div className="mt-4 space-y-2.5">
        <ProtoRow
          label="SMB"
          status={share.smbStatus}
          detail={ACCESS_LABEL[share.smb.access]}
          address={`\\\\${host}\\${share.name}`}
        />
        <ProtoRow
          label="NFS"
          status={share.nfsStatus}
          detail={share.nfs.readOnly ? "Read-only" : "Read and write"}
          address={`${host}:${share.path}`}
        />
      </div>
      <div className="mt-4 flex justify-end">
        <Button size="sm" variant="primary" onClick={onBrowse}>
          <FolderOpen size={14} />
          Browse
        </Button>
      </div>
    </Card>
  );
}

function ProtoRow({
  label,
  status,
  detail,
  address,
}: {
  label: string;
  status: ProtoStatus;
  detail: string;
  address: string;
}) {
  const off = status.state === "off";
  return (
    <div className="rounded-2xl border border-line bg-surface-2 px-3.5 py-3">
      <div className="flex items-center gap-3">
        <span
          className={`w-11 shrink-0 rounded-lg py-1 text-center text-[10px] font-extrabold tracking-wider ${
            off ? "bg-track text-ink-3" : "accent-gradient text-white"
          }`}
        >
          {label}
        </span>
        <span className={`min-w-0 flex-1 truncate text-sm font-semibold ${off ? "text-ink-3" : "text-ink"}`}>
          {off ? "Off" : detail}
        </span>
        {!off && <StatePill state={status.state} />}
      </div>
      {status.state === "active" && <Address value={address} />}
      {status.state === "error" && status.message && (
        <p className="mt-2 text-xs font-semibold text-bad">{status.message}</p>
      )}
    </div>
  );
}

function StatePill({ state }: { state: ProtoStatus["state"] }) {
  const ok = state === "active";
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1.5 text-[11px] font-bold ${
        ok ? "text-ok" : "text-bad"
      }`}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {ok ? "Live" : "Not live"}
    </span>
  );
}

/** The address to type on another computer, with a copy button. */
function Address({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const canCopy = typeof navigator !== "undefined" && !!navigator.clipboard;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard blocked; the address is still selectable */
    }
  };

  const Icon: LucideIcon = copied ? Check : Copy;
  return (
    <div className="mt-2 flex items-center gap-2">
      <code className="min-w-0 flex-1 truncate font-mono text-xs text-ink-2" title={value}>
        {value}
      </code>
      {canCopy && (
        <button
          type="button"
          onClick={copy}
          aria-label={`Copy address ${value}`}
          className="flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-[11px] font-bold text-ink-3 transition-colors hover:bg-accent/10 hover:text-accent"
        >
          <Icon size={12} />
          {copied ? "Copied" : "Copy"}
        </button>
      )}
    </div>
  );
}

function DeleteShareDialog({
  share,
  onClose,
  onDeleted,
}: {
  share: Share;
  onClose: () => void;
  onDeleted: (folderKept: boolean) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api<{ folderKept: boolean }>(
        `/shares/${encodeURIComponent(share.name)}`,
        { method: "DELETE" },
      );
      onDeleted(res.folderKept);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Modal
      title={`Stop sharing ${share.name}?`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="danger" onClick={confirm} loading={busy}>
            Stop sharing
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-sm font-medium leading-relaxed text-ink-2">
          Other computers will lose access straight away. Your files are not deleted: anything in{" "}
          <code className="font-mono text-ink">{share.path}</code> stays on the server. If the
          folder is empty it is tidied away.
        </p>
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}
