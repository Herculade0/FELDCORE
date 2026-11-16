import { useCallback, useEffect, useState } from "react";
import {
  Camera,
  Code,
  MonitorPlay,
  Pause,
  Play,
  Plus,
  Settings2,
  Square,
  Trash2,
  Pencil,
  Power,
} from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, IconBadge } from "../ui";
import { Alert, Button, IconButton, Modal, TextField } from "../form";
import Console from "./Console";
import VMForm from "./VMForm";
import VMSettings from "./VMSettings";
import { emptySpec, type Host, type Snapshot, type Spec, type VM } from "./types";
import { PageHelp } from "../help/PageHelp";

type View = "list" | "form" | "settings";

interface SnapList {
  snapshots: Snapshot[];
}

export default function VMsPage({ focus }: { focus?: { view?: string; nonce: number } }) {
  const [view, setView] = useState<View>("list");
  const [host, setHost] = useState<Host | null>(null);
  const [vms, setVMs] = useState<VM[]>([]);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [draft, setDraft] = useState<Spec | null>(null);
  const [editing, setEditing] = useState<string | undefined>();
  const [consoleFor, setConsoleFor] = useState<string | null>(null);
  const [xmlFor, setXmlFor] = useState<string | null>(null);
  const [snapsFor, setSnapsFor] = useState<string | null>(null);
  const [deleteFor, setDeleteFor] = useState<VM | null>(null);
  const [installing, setInstalling] = useState(false);

  const load = useCallback(async () => {
    try {
      const [h, list] = await Promise.all([
        api<Host>("/vms/host"),
        api<{ vms: VM[]; message?: string }>("/vms"),
      ]);
      setHost(h);
      setVMs(list.vms ?? []);
      setMessage(list.message || h.message || null);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    if (!focus?.nonce) return;
    if (focus.view === "settings") setView("settings");
    else setView("list");
  }, [focus?.nonce, focus?.view]);

  useEffect(() => {
    void load();
    const t = setInterval(() => {
      if (view === "list") void load();
    }, 5000);
    return () => clearInterval(t);
  }, [load, view]);

  const act = async (name: string, path: string, ok: string) => {
    setError(null);
    try {
      await api(`/vms/${encodeURIComponent(name)}/${path}`, { method: "POST" });
      setNotice(ok);
      await load();
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  const installLibvirt = async () => {
    setInstalling(true);
    setError(null);
    try {
      await api("/settings/services", { method: "PUT", body: { name: "libvirt", enabled: true } });
      setNotice("libvirt is installed and running.");
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setInstalling(false);
    }
  };

  if (view === "settings") return <VMSettings onBack={() => setView("list")} />;
  if (view === "form" && draft) {
    return (
      <VMForm
        editing={editing}
        initial={draft}
        host={
          host ?? {
            kvm: false,
            kvmMessage: "Checking whether KVM is available…",
            virsh: false,
            libvirt: false,
            qemuImg: false,
            swtpm: false,
            virtiofsd: false,
            root: false,
            cores: [],
            memoryMB: 0,
            bridges: [],
            iommu: [],
            usb: [],
            isos: [],
            images: [],
            shares: [],
            domainsPath: "",
            isosPath: "",
          }
        }
        onCancel={() => setView("list")}
        onSaved={(msg) => {
          setView("list");
          setNotice(msg);
          void load();
        }}
      />
    );
  }

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Virtual machines</h1>
            <PageHelp topic="vms" />
          </div>
          <p className="mt-2 max-w-xl text-sm font-semibold text-ink-2">
            KVM guests on this server. Start and stop them here, and open the console in the browser.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setView("settings")}>
            <Settings2 size={16} />
            VM Manager
          </Button>
          <Button
            variant="primary"
            onClick={() => {
              setEditing(undefined);
              setDraft(emptySpec(host));
              setView("form");
            }}
          >
            <Plus size={16} strokeWidth={2.75} />
            Add VM
          </Button>
        </div>
      </header>

      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}
      {host && !host.kvm && host.kvmMessage && <Alert tone="warn">{host.kvmMessage}</Alert>}
      {message && host?.libvirt && <Alert tone="info">{message}</Alert>}

      {host && !host.libvirt && (
        <Card className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-base font-extrabold text-ink">
              {host.virsh ? "libvirt is not running" : "libvirt is not installed"}
            </h2>
            <p className="mt-1 max-w-xl text-sm font-medium text-ink-2">
              The VM Manager uses libvirt and KVM. Turning the service on installs QEMU, OVMF and a
              software TPM, then starts libvirtd. It stays off when this CPU cannot virtualise.
            </p>
          </div>
          <Button variant="primary" loading={installing} onClick={() => void installLibvirt()}>
            {host.virsh ? "Start libvirt" : "Install libvirt"}
          </Button>
        </Card>
      )}

      {vms.length === 0 ? (
        <Card className="flex flex-col items-center px-6 py-14 text-center">
          <IconBadge icon={MonitorPlay} size={64} muted={!host?.libvirt} />
          <h2 className="mt-5 text-lg font-extrabold text-ink">No virtual machines</h2>
          <p className="mt-2 max-w-md text-sm font-medium leading-relaxed text-ink-2">
            Add a VM to choose CPUs, memory, firmware, disks and an ISO. The console opens in this
            browser once the guest is running.
          </p>
          <Button
            variant="primary"
            className="mt-6"
            onClick={() => {
              setEditing(undefined);
              setDraft(emptySpec(host));
              setView("form");
            }}
          >
            <Plus size={16} strokeWidth={2.75} />
            Add VM
          </Button>
        </Card>
      ) : (
        <div className="space-y-3">
          {vms.map((vm) => (
            <VMRow
              key={vm.name}
              vm={vm}
              onAction={act}
              onEdit={() => {
                setEditing(vm.name);
                setDraft(emptySpec(host));
                setView("form");
              }}
              onConsole={() => setConsoleFor(vm.name)}
              onXML={() => setXmlFor(vm.name)}
              onSnaps={() => setSnapsFor(vm.name)}
              onDelete={() => setDeleteFor(vm)}
              onAutostart={(on) =>
                void api(`/vms/${encodeURIComponent(vm.name)}/autostart`, { method: "POST", body: { enabled: on } })
                  .then(() => load())
                  .catch((e) => setError(errorMessage(e)))
              }
            />
          ))}
        </div>
      )}

      {consoleFor && <Console name={consoleFor} onClose={() => setConsoleFor(null)} />}
      {xmlFor && (
        <XMLEditor
          name={xmlFor}
          onClose={() => setXmlFor(null)}
          onSaved={() => {
            setXmlFor(null);
            setNotice("XML saved.");
            void load();
          }}
        />
      )}
      {snapsFor && <Snapshots name={snapsFor} onClose={() => setSnapsFor(null)} onError={setError} />}
      {deleteFor && (
        <DeleteDialog
          vm={deleteFor}
          onClose={() => setDeleteFor(null)}
          onDone={(msg) => {
            setDeleteFor(null);
            setNotice(msg);
            void load();
          }}
        />
      )}
    </div>
  );
}

const STATE: Record<string, { label: string; dot: string; text: string }> = {
  running: { label: "Running", dot: "bg-ok", text: "text-ok" },
  paused: { label: "Paused", dot: "bg-warn", text: "text-warn" },
  stopped: { label: "Stopped", dot: "bg-ink-3", text: "text-ink-3" },
  crashed: { label: "Crashed", dot: "bg-bad", text: "text-bad" },
};

function VMRow({
  vm,
  onAction,
  onEdit,
  onConsole,
  onXML,
  onSnaps,
  onDelete,
  onAutostart,
}: {
  vm: VM;
  onAction: (name: string, path: string, ok: string) => Promise<void>;
  onEdit: () => void;
  onConsole: () => void;
  onXML: () => void;
  onSnaps: () => void;
  onDelete: () => void;
  onAutostart: (on: boolean) => void;
}) {
  const [busy, setBusy] = useState("");
  const st = STATE[vm.state] ?? { label: vm.state, dot: "bg-ink-3", text: "text-ink-3" };
  const run = async (path: string, ok: string) => {
    setBusy(path);
    await onAction(vm.name, path, ok);
    setBusy("");
  };
  const running = vm.state === "running";
  const paused = vm.state === "paused";
  return (
    <Card className="flex flex-col gap-4 lg:flex-row lg:items-center">
      <div className="flex min-w-0 flex-1 items-center gap-3.5">
        <IconBadge icon={MonitorPlay} muted={!running} />
        <div className="min-w-0">
          <h2 className="truncate text-base font-extrabold text-ink">{vm.name}</h2>
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs font-semibold">
            <span className={`inline-flex items-center gap-1.5 ${st.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${st.dot}`} />
              {st.label}
            </span>
            <span className="text-ink-3">
              {vm.cpus} vCPU · {vm.memoryMB >= 1024 ? `${Math.round(vm.memoryMB / 1024)} GB` : `${vm.memoryMB} MB`}
            </span>
            <button type="button" className="text-ink-3 hover:text-accent" onClick={() => onAutostart(!vm.autostart)}>
              {vm.autostart ? "Autostart on" : "Autostart off"}
            </button>
          </div>
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-1">
        <IconButton label="Console" onClick={onConsole} disabled={!running}>
          <MonitorPlay size={16} />
        </IconButton>
        <IconButton label="Snapshots" onClick={onSnaps}>
          <Camera size={16} />
        </IconButton>
        <IconButton label="Edit XML" onClick={onXML} disabled={running || paused}>
          <Code size={16} />
        </IconButton>
        <IconButton label="Edit" onClick={onEdit} disabled={running || paused}>
          <Pencil size={16} />
        </IconButton>
        {running ? (
          <>
            <IconButton label="Pause" disabled={!!busy} onClick={() => void run("pause", `${vm.name} is paused.`)}>
              <Pause size={16} />
            </IconButton>
            <IconButton label="Stop" disabled={!!busy} onClick={() => void run("stop", `${vm.name} is shutting down.`)}>
              <Square size={16} />
            </IconButton>
            <IconButton label="Force stop" disabled={!!busy} onClick={() => void run("force-stop", `${vm.name} was forced off.`)}>
              <Power size={16} />
            </IconButton>
          </>
        ) : paused ? (
          <>
            <IconButton label="Resume" disabled={!!busy} onClick={() => void run("resume", `${vm.name} resumed.`)}>
              <Play size={16} />
            </IconButton>
            <IconButton label="Force stop" disabled={!!busy} onClick={() => void run("force-stop", `${vm.name} was forced off.`)}>
              <Power size={16} />
            </IconButton>
          </>
        ) : (
          <IconButton label="Start" disabled={!!busy} onClick={() => void run("start", `${vm.name} is starting.`)}>
            <Play size={16} />
          </IconButton>
        )}
        <IconButton label="Delete" onClick={onDelete} disabled={running || paused}>
          <Trash2 size={16} />
        </IconButton>
      </div>
    </Card>
  );
}

function XMLEditor({ name, onClose, onSaved }: { name: string; onClose: () => void; onSaved: () => void }) {
  const [xml, setXml] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    void api<{ xml: string }>(`/vms/${encodeURIComponent(name)}/xml`)
      .then((r) => setXml(r.xml))
      .catch((e) => setError(errorMessage(e)));
  }, [name]);
  return (
    <Modal
      title={`XML · ${name}`}
      size="xl"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            loading={saving}
            onClick={() => {
              setSaving(true);
              void api(`/vms/${encodeURIComponent(name)}/xml`, { method: "PUT", body: { xml } })
                .then(onSaved)
                .catch((e) => setError(errorMessage(e)))
                .finally(() => setSaving(false));
            }}
          >
            Save XML
          </Button>
        </>
      }
    >
      {error && (
        <div className="mb-3">
          <Alert>{error}</Alert>
        </div>
      )}
      <p className="mb-3 text-xs font-medium text-ink-2">
        VNC must listen on 127.0.0.1. The name has to stay {name}. Saving replaces the domain.
      </p>
      <textarea
        value={xml}
        onChange={(e) => setXml(e.target.value)}
        spellCheck={false}
        className="h-[60vh] w-full rounded-2xl border border-line bg-surface-2 p-3 font-mono text-xs leading-relaxed text-ink"
      />
    </Modal>
  );
}

function Snapshots({ name, onClose, onError }: { name: string; onClose: () => void; onError: (s: string) => void }) {
  const [snaps, setSnaps] = useState<Snapshot[]>([]);
  const [snapName, setSnapName] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    const res = await api<SnapList>(`/vms/${encodeURIComponent(name)}/snapshots`);
    setSnaps(res.snapshots ?? []);
  }, [name]);
  useEffect(() => {
    void load().catch((e) => onError(errorMessage(e)));
  }, [load, onError]);
  const run = async (path: string, method: "POST" | "DELETE") => {
    setBusy(true);
    try {
      await api(path, { method, body: method === "POST" && path.endsWith("/snapshots") ? { name: snapName } : undefined });
      setSnapName("");
      await load();
    } catch (e) {
      onError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal title={`Snapshots · ${name}`} size="lg" onClose={onClose}>
      <p className="mb-4 text-xs font-medium text-ink-2">
        Snapshots use the qcow2 image. A raw disk cannot be snapshotted.
      </p>
      <div className="flex gap-2">
        <div className="min-w-0 flex-1">
          <TextField label="New snapshot" value={snapName} onChange={(e) => setSnapName(e.target.value)} placeholder="before-upgrade" />
        </div>
        <div className="pt-6">
          <Button variant="primary" disabled={!snapName || busy} onClick={() => void run(`/vms/${encodeURIComponent(name)}/snapshots`, "POST")}>
            Create
          </Button>
        </div>
      </div>
      <ul className="mt-4 space-y-2">
        {snaps.length === 0 && <li className="text-sm font-medium text-ink-3">No snapshots yet.</li>}
        {snaps.map((s) => (
          <li key={s.name} className="flex items-center justify-between gap-3 rounded-2xl border border-line px-3 py-2">
            <span className="text-sm font-bold text-ink">
              {s.name}
              {s.state && <span className="ml-2 text-xs font-semibold text-ink-3">{s.state}</span>}
            </span>
            <span className="flex gap-2">
              <Button
                size="sm"
                disabled={busy}
                onClick={() => void run(`/vms/${encodeURIComponent(name)}/snapshots/${encodeURIComponent(s.name)}/revert`, "POST")}
              >
                Revert
              </Button>
              <Button
                size="sm"
                variant="danger"
                disabled={busy}
                onClick={() => void run(`/vms/${encodeURIComponent(name)}/snapshots/${encodeURIComponent(s.name)}`, "DELETE")}
              >
                Delete
              </Button>
            </span>
          </li>
        ))}
      </ul>
    </Modal>
  );
}

function DeleteDialog({ vm, onClose, onDone }: { vm: VM; onClose: () => void; onDone: (msg: string) => void }) {
  const [disks, setDisks] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <Modal
      title={`Delete ${vm.name}`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="danger"
            loading={busy}
            onClick={() => {
              setBusy(true);
              void api(`/vms/${encodeURIComponent(vm.name)}?disks=${disks ? "1" : "0"}`, { method: "DELETE" })
                .then(() => onDone(disks ? `${vm.name} and its disks were deleted.` : `${vm.name} was removed. Disks were kept.`))
                .catch((e) => {
                  setError(errorMessage(e));
                  setBusy(false);
                });
            }}
          >
            Delete
          </Button>
        </>
      }
    >
      {error && (
        <div className="mb-3">
          <Alert>{error}</Alert>
        </div>
      )}
      <p className="text-sm font-medium text-ink-2">
        This undefines the domain. ISO files are never deleted. Disk files are removed only when you
        ask, and only if they live in this VM's folder.
      </p>
      <label className="mt-4 flex items-center gap-3 text-sm font-semibold text-ink">
        <input type="checkbox" className="h-4 w-4 accent-accent" checked={disks} onChange={(e) => setDisks(e.target.checked)} />
        Also delete the virtual disks
      </label>
    </Modal>
  );
}
