import { useEffect, useState } from "react";
import { Camera, RotateCcw, Trash2 } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes } from "../../lib/format";
import { Alert, Button, TextField, Toggle } from "../form";
import { Modal } from "../form";
import type { Container } from "./types";

interface Shot {
  id: string;
  container: string;
  createdAt: string;
  note?: string;
  image: string;
  snapImage: string;
  live: boolean;
  auto: boolean;
  size: number;
  methods: string[];
  warnings?: string[];
}

interface Usage {
  path: string;
  bytes: number;
  free: number;
  total: number;
  keepN: number;
  keepDays: number;
}

export default function SnapshotsDialog({
  container,
  onClose,
}: {
  container: Container;
  onClose: () => void;
}) {
  const [shots, setShots] = useState<Shot[]>([]);
  const [usage, setUsage] = useState<Usage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [live, setLive] = useState(false);
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState<Shot | null>(null);
  const [keepN, setKeepN] = useState("5");
  const [keepDays, setKeepDays] = useState("30");

  const load = () =>
    api<{ snapshots: Shot[]; usage: Usage }>(`/docker/containers/${encodeURIComponent(container.id)}/snapshots`)
      .then((res) => {
        setShots(res.snapshots ?? []);
        setUsage(res.usage);
        setKeepN(String(res.usage.keepN));
        setKeepDays(String(res.usage.keepDays));
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, [container.id]);

  const take = async () => {
    setBusy("take");
    setError(null);
    try {
      await api(`/docker/containers/${encodeURIComponent(container.id)}/snapshots`, {
        method: "POST",
        body: { note, live },
      });
      setNote("");
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const restore = async (shot: Shot) => {
    setBusy(shot.id);
    setError(null);
    try {
      await api(`/docker/containers/${encodeURIComponent(container.id)}/snapshots/${encodeURIComponent(shot.id)}/restore`, {
        method: "POST",
        body: { confirm: true },
      });
      setConfirm(null);
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const remove = async (shot: Shot) => {
    setBusy("del-" + shot.id);
    setError(null);
    try {
      await api(`/docker/containers/${encodeURIComponent(container.id)}/snapshots/${encodeURIComponent(shot.id)}`, {
        method: "DELETE",
      });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const saveRetention = async () => {
    setBusy("keep");
    setError(null);
    try {
      const next = await api<Usage>("/snapshots", {
        method: "PUT",
        body: { keepN: Number(keepN), keepDays: Number(keepDays), dir: "" },
      });
      setUsage(next);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  return (
    <Modal title={`Snapshots · ${container.name}`} size="lg" onClose={onClose} footer={<Button onClick={onClose}>Close</Button>}>
      {error && (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      )}

      <div className="rounded-2xl border border-line bg-surface-2 px-4 py-3">
        <p className="text-sm font-bold text-ink">
          {usage ? `${bytes(usage.bytes)} of snapshots` : "Snapshots"}
          {usage && usage.free > 0 ? ` · ${bytes(usage.free)} free` : ""}
        </p>
        <p className="mt-1 text-xs font-medium text-ink-3">
          Stored on the system share{usage?.path ? ` · ${usage.path}` : ""}. A snapshot is the container's settings, the image tagged{" "}
          <span className="font-mono">feldcore-snap/…</span>, and its mapped folders. btrfs and zfs use a filesystem snapshot when the folder is its own subvolume or dataset; otherwise the folder is a tar.zst archive.
        </p>
      </div>

      <div className="mt-4 grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
        <TextField label="Keep this many" value={keepN} onChange={(e) => setKeepN(e.target.value)} inputMode="numeric" />
        <TextField label="Keep for days" value={keepDays} onChange={(e) => setKeepDays(e.target.value)} inputMode="numeric" hint="0 means no limit for that rule." />
        <Button onClick={() => void saveRetention()} loading={busy === "keep"}>
          Save
        </Button>
      </div>

      <div className="mt-6 space-y-3">
        {shots.length === 0 && (
          <p className="text-sm font-medium leading-relaxed text-ink-2">
            No snapshots yet. Take one before you change anything you might want back. FELDCORE also takes one automatically before an edit.
          </p>
        )}
        {shots.map((s) => (
          <div key={s.id} className="rounded-2xl border border-line px-4 py-3">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <p className="text-sm font-bold text-ink">{when(s.createdAt)}</p>
              <p className="text-xs font-semibold text-ink-3">
                {s.size > 0 ? bytes(s.size) : s.methods?.join(", ") || "settings and image"}
                {s.live ? " · live" : ""}
                {s.auto ? " · automatic" : ""}
              </p>
            </div>
            {s.note && <p className="mt-1 text-xs font-medium text-ink-2">{s.note}</p>}
            <p className="mt-1 truncate font-mono text-[10px] text-ink-3">{s.snapImage}</p>
            {s.warnings?.map((w) => (
              <p key={w} className="mt-1 text-xs font-medium text-warn">
                {w}
              </p>
            ))}
            <div className="mt-3 flex flex-wrap gap-2">
              <Button size="sm" onClick={() => setConfirm(s)} disabled={!!busy}>
                <RotateCcw size={13} />
                Restore
              </Button>
              <Button size="sm" variant="danger" onClick={() => void remove(s)} loading={busy === "del-" + s.id}>
                <Trash2 size={13} />
                Delete
              </Button>
            </div>
          </div>
        ))}
      </div>

      <div className="mt-6 border-t border-line pt-5">
        <p className="text-sm font-bold text-ink">Take a snapshot</p>
        <div className="mt-3">
          <TextField label="Note" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Before the mod update" />
        </div>
        <div className="mt-3 flex items-center gap-3">
          <Toggle checked={live} onChange={setLive} label="Keep the container running" />
          <p className="text-xs font-medium text-ink-3">
            {live
              ? "The container stays up. A file may be copied mid-write."
              : "The container stops briefly so the copy is consistent, then starts again."}
          </p>
        </div>
        <div className="mt-4">
          <Button variant="primary" loading={busy === "take"} onClick={() => void take()}>
            <Camera size={15} />
            Take snapshot
          </Button>
        </div>
      </div>

      {confirm && (
        <Modal
          title="Restore this snapshot?"
          onClose={() => setConfirm(null)}
          footer={
            <>
              <Button onClick={() => setConfirm(null)} disabled={!!busy}>
                Cancel
              </Button>
              <Button variant="danger" loading={busy === confirm.id} onClick={() => void restore(confirm)}>
                Restore
              </Button>
            </>
          }
        >
          <p className="text-sm font-medium leading-relaxed text-ink-2">
            This stops {container.name}, puts the mapped folders back to {when(confirm.createdAt)}, and recreates the container from the saved image and settings. Changes made after this snapshot are replaced.
          </p>
        </Modal>
      )}
    </Modal>
  );
}

function when(iso: string) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
