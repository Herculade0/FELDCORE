import { useEffect, useState } from "react";
import { ArrowLeft, Download, HardDrive, Trash2 } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes } from "../../lib/format";
import { Card, CardTitle } from "../ui";
import { Alert, Button, TextField } from "../form";
import { useVirtio } from "./useVirtio";
import type { VMSettings as Paths } from "./types";

export default function VMSettings({ onBack }: { onBack: () => void }) {
  const [paths, setPaths] = useState<Paths | null>(null);
  const [domains, setDomains] = useState("");
  const [isos, setIsos] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const virtio = useVirtio();

  useEffect(() => {
    void api<Paths>("/vms/settings")
      .then((p) => {
        setPaths(p);
        setDomains(p.domainsPath);
        setIsos(p.isosPath);
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const p = await api<Paths>("/vms/settings", {
        method: "PUT",
        body: { domainsPath: domains.trim(), isosPath: isos.trim() },
      });
      setPaths(p);
      setNotice("VM Manager paths saved.");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <button
            type="button"
            onClick={onBack}
            className="mb-3 inline-flex items-center gap-1.5 text-xs font-bold text-ink-3 hover:text-accent"
          >
            <ArrowLeft size={14} />
            Virtual machines
          </button>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">VM Manager</h1>
          <p className="mt-2 max-w-2xl text-sm font-semibold text-ink-2">
            Where virtual disks and ISOs live, and the VirtIO driver images Windows guests use.
            Libvirt itself is switched on under Settings, Services.
          </p>
        </div>
      </header>

      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}
      {virtio.error && <Alert>{virtio.error}</Alert>}

      <Card>
        <CardTitle icon={HardDrive} title="Default paths" hint="Blank uses the share" />
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <TextField
            label="Domains"
            value={domains}
            onChange={(e) => setDomains(e.target.value)}
            placeholder={paths?.domainsDefault || "/mnt/feldcore/data/domains"}
            hint="Virtual disks are created here, one folder per VM."
          />
          <TextField
            label="ISOs"
            value={isos}
            onChange={(e) => setIsos(e.target.value)}
            placeholder={paths?.isosDefault || "/mnt/feldcore/data/isos"}
            hint="Install media and VirtIO driver ISOs are picked from here."
          />
        </div>
        <div className="mt-4 flex justify-end">
          <Button variant="primary" onClick={() => void save()} loading={saving}>
            Save paths
          </Button>
        </div>
      </Card>

      <Card>
        <CardTitle icon={Download} title="VirtIO drivers" hint="Fedora virtio-win" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Stable and latest come from Fedora's virtio-win direct downloads. The file is saved into
          the ISO folder, checked as a complete ISO 9660 image, and a SHA-256 is recorded beside it.
          Windows guests need it for a virtio disk and network card. Linux guests can ignore it.
        </p>
        <div className="mt-4 space-y-3">
          {(virtio.view?.releases ?? []).map((rel) => (
            <div
              key={rel.channel}
              className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-line px-4 py-3"
            >
              <div>
                <div className="text-sm font-extrabold capitalize text-ink">{rel.channel}</div>
                <div className="text-xs font-semibold text-ink-3">
                  {rel.error
                    ? rel.error
                    : rel.version
                      ? `virtio-win ${rel.version}${rel.present ? " · in the ISO folder" : ""}`
                      : "Looking up the current build…"}
                </div>
              </div>
              <Button
                variant={rel.present ? "secondary" : "primary"}
                onClick={() => void virtio.download(rel.channel)}
                disabled={!!virtio.job && !virtio.job.finished}
              >
                <Download size={14} />
                {rel.present ? "Download again" : "Download"}
              </Button>
            </div>
          ))}
          {!virtio.view && <p className="text-sm font-semibold text-ink-3">Checking Fedora…</p>}
        </div>

        {virtio.job && (
          <div className="mt-4">
            <div className="mb-1.5 flex justify-between text-xs font-bold text-ink-2">
              <span>{virtio.job.error ? virtio.job.error : virtio.job.label}</span>
              <span>
                {virtio.job.finished && !virtio.job.error
                  ? "Saved"
                  : virtio.job.total > 0
                    ? `${bytes(virtio.job.done)} / ${bytes(virtio.job.total)}`
                    : "Starting…"}
              </span>
            </div>
            <div className="h-2.5 overflow-hidden rounded-full bg-track">
              <div
                className={`h-full rounded-full ${virtio.job.error ? "bg-bad" : "accent-gradient"}`}
                style={{ width: `${virtio.job.finished && !virtio.job.error ? 100 : virtio.job.percent}%` }}
              />
            </div>
          </div>
        )}

        <div className="mt-5">
          <h3 className="text-xs font-bold uppercase tracking-wider text-ink-3">In the ISO folder</h3>
          {(virtio.view?.installed ?? []).length === 0 ? (
            <p className="mt-2 text-sm font-medium text-ink-2">No VirtIO ISO downloaded yet.</p>
          ) : (
            <ul className="mt-2 space-y-2">
              {(virtio.view?.installed ?? []).map((f) => (
                <li
                  key={f.name}
                  className="flex items-center justify-between gap-3 rounded-2xl bg-surface-2 px-3 py-2.5"
                >
                  <div className="min-w-0">
                    <div className="truncate text-sm font-bold text-ink">{f.name}</div>
                    <div className="truncate text-[11px] font-semibold text-ink-3">
                      {bytes(f.size)}
                      {f.sha256 ? ` · SHA-256 ${f.sha256.slice(0, 12)}…` : ""}
                    </div>
                  </div>
                  <Button size="sm" variant="danger" onClick={() => void virtio.remove(f.name)}>
                    <Trash2 size={13} />
                    Delete
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Card>
    </div>
  );
}
