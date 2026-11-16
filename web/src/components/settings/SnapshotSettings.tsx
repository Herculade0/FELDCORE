import { useEffect, useState } from "react";
import { HardDrive } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes } from "../../lib/format";
import { Card, CardTitle } from "../ui";
import { Alert, Button, TextField } from "../form";

interface Usage {
  path: string;
  bytes: number;
  free: number;
  total: number;
  keepN: number;
  keepDays: number;
}

export default function SnapshotSettings() {
  const [usage, setUsage] = useState<Usage | null>(null);
  const [keepN, setKeepN] = useState("5");
  const [keepDays, setKeepDays] = useState("30");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api<Usage>("/snapshots")
      .then((u) => {
        setUsage(u);
        setKeepN(String(u.keepN));
        setKeepDays(String(u.keepDays));
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const save = async () => {
    setBusy(true);
    setSaved(false);
    setError(null);
    try {
      const next = await api<Usage>("/snapshots", {
        method: "PUT",
        body: { keepN: Number(keepN), keepDays: Number(keepDays), dir: "" },
      });
      setUsage(next);
      setSaved(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const used = usage?.total ? Math.min(100, Math.round((1 - usage.free / usage.total) * 100)) : 0;

  return (
    <div id="settings-snapshots">
    <Card>
      <CardTitle icon={HardDrive} title="Snapshots" hint={usage ? `${bytes(usage.bytes)} used` : ""} />
      <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
        A snapshot keeps a container's settings, the exact image it was running, and its mapped folders. FELDCORE takes one automatically before a container is edited. Old copies are deleted when they pass either limit. Zero means that limit is off.
      </p>
      {usage && (
        <p className="mt-3 text-xs font-semibold text-ink-3">
          {bytes(usage.bytes)} of snapshots
          {usage.total > 0 ? ` · ${bytes(usage.free)} free on this disk (${used}% used)` : ""} · {usage.path}
        </p>
      )}
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <TextField label="Keep this many" value={keepN} onChange={(e) => setKeepN(e.target.value)} inputMode="numeric" hint="Per container. 5 is a good start." />
        <TextField label="Keep for this many days" value={keepDays} onChange={(e) => setKeepDays(e.target.value)} inputMode="numeric" hint="Copies older than this are removed." />
      </div>
      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      <div className="mt-4 flex items-center gap-3">
        <Button variant="primary" loading={busy} onClick={() => void save()}>
          Save retention
        </Button>
        {saved && <p className="text-xs font-semibold text-ok">Saved.</p>}
      </div>
    </Card>
    </div>
  );
}
