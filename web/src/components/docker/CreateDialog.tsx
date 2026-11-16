import { useEffect, useState } from "react";
import { Plus, X } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, IconButton, Modal, TextField, Toggle } from "../form";
import type { CreateRequest, EnvVar, Image, PortMapping, VolumeMapping } from "./types";

const RESTART = [
  { key: "no", label: "Never" },
  { key: "unless-stopped", label: "Unless stopped" },
  { key: "always", label: "Always" },
  { key: "on-failure", label: "On failure" },
];

export default function CreateDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (name: string) => void;
}) {
  const [name, setName] = useState("");
  const [image, setImage] = useState("");
  const [images, setImages] = useState<Image[]>([]);
  const [ports, setPorts] = useState<PortMapping[]>([]);
  const [volumes, setVolumes] = useState<VolumeMapping[]>([]);
  const [env, setEnv] = useState<EnvVar[]>([]);
  const [restartPolicy, setRestartPolicy] = useState("unless-stopped");
  const [autostart, setAutostart] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api<{ images: Image[] }>("/docker/images")
      .then((r) => setImages(r.images ?? []))
      .catch(() => {});
  }, []);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const body: CreateRequest = {
      name: name.trim(),
      image: image.trim(),
      ports: ports.filter((p) => p.host && p.container),
      volumes: volumes.filter((v) => v.host && v.container),
      env: env.filter((e) => e.key.trim()),
      restartPolicy,
      autostart,
    };
    try {
      await api("/docker/containers", { method: "POST", body });
      onCreated(body.name || body.image);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Modal
      title="Run a container"
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy}>
            {autostart ? "Create & start" : "Create"}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div>
          <TextField
            label="Image"
            value={image}
            onChange={(e) => setImage(e.target.value)}
            placeholder="nginx:latest"
            list="feldcore-images"
            hint="The image is pulled automatically if it isn't already here."
            autoCapitalize="none"
            spellCheck={false}
            required
            autoFocus
          />
          <datalist id="feldcore-images">
            {images.map((i) => (
              <option key={i.id + i.tag} value={i.tag} />
            ))}
          </datalist>
        </div>

        <TextField
          label="Name (optional)"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Docker picks one if you leave this blank"
          autoCapitalize="none"
          spellCheck={false}
        />

        <RowEditor<PortMapping>
          legend="Ports"
          addLabel="Add port"
          rows={ports}
          onChange={setPorts}
          blank={() => ({ host: 0, container: 0, proto: "tcp" })}
          render={(row, set) => (
            <div className="flex items-center gap-2">
              <NumberInput
                aria-label="Host port"
                placeholder="Host"
                value={row.host || ""}
                onChange={(n) => set({ ...row, host: n })}
              />
              <span className="text-ink-3">→</span>
              <NumberInput
                aria-label="Container port"
                placeholder="Container"
                value={row.container || ""}
                onChange={(n) => set({ ...row, container: n })}
              />
              <select
                aria-label="Protocol"
                value={row.proto}
                onChange={(e) => set({ ...row, proto: e.target.value })}
                className="rounded-xl border border-line bg-surface-2 px-2.5 py-2.5 text-sm font-medium text-ink"
              >
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
            </div>
          )}
        />

        <RowEditor<VolumeMapping>
          legend="Folders"
          addLabel="Add folder"
          hint="Map a folder on this server into the container. Share folders live under /srv/feldcore."
          rows={volumes}
          onChange={setVolumes}
          blank={() => ({ host: "", container: "", readOnly: false })}
          render={(row, set) => (
            <div className="flex items-center gap-2">
              <input
                aria-label="Host path"
                placeholder="/srv/feldcore/media"
                value={row.host}
                onChange={(e) => set({ ...row, host: e.target.value })}
                className="min-w-0 flex-1 rounded-xl border border-line bg-surface-2 px-3 py-2.5 font-mono text-xs text-ink"
              />
              <span className="text-ink-3">→</span>
              <input
                aria-label="Container path"
                placeholder="/data"
                value={row.container}
                onChange={(e) => set({ ...row, container: e.target.value })}
                className="min-w-0 flex-1 rounded-xl border border-line bg-surface-2 px-3 py-2.5 font-mono text-xs text-ink"
              />
              <button
                type="button"
                onClick={() => set({ ...row, readOnly: !row.readOnly })}
                aria-pressed={row.readOnly}
                title="Read-only"
                className={`shrink-0 rounded-lg px-2 py-2 text-[11px] font-bold ${
                  row.readOnly ? "accent-gradient text-white" : "bg-track text-ink-3"
                }`}
              >
                RO
              </button>
            </div>
          )}
        />

        <RowEditor<EnvVar>
          legend="Environment variables"
          addLabel="Add variable"
          rows={env}
          onChange={setEnv}
          blank={() => ({ key: "", value: "" })}
          render={(row, set) => (
            <div className="flex items-center gap-2">
              <input
                aria-label="Variable name"
                placeholder="KEY"
                value={row.key}
                onChange={(e) => set({ ...row, key: e.target.value })}
                className="min-w-0 flex-1 rounded-xl border border-line bg-surface-2 px-3 py-2.5 font-mono text-xs text-ink"
              />
              <span className="text-ink-3">=</span>
              <input
                aria-label="Variable value"
                placeholder="value"
                value={row.value}
                onChange={(e) => set({ ...row, value: e.target.value })}
                className="min-w-0 flex-1 rounded-xl border border-line bg-surface-2 px-3 py-2.5 font-mono text-xs text-ink"
              />
            </div>
          )}
        />

        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <label className="mb-1.5 block text-xs font-bold text-ink-2">Restart policy</label>
            <select
              value={restartPolicy}
              onChange={(e) => setRestartPolicy(e.target.value)}
              className="rounded-xl border border-line bg-surface-2 px-3 py-2.5 text-sm font-medium text-ink"
            >
              {RESTART.map((r) => (
                <option key={r.key} value={r.key}>
                  {r.label}
                </option>
              ))}
            </select>
          </div>
          <label className="flex items-center gap-3 pb-1 text-sm font-semibold text-ink">
            Start now
            <Toggle checked={autostart} onChange={setAutostart} label="Start the container now" />
          </label>
        </div>

        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}

/** A list of repeatable rows with add/remove controls. */
function RowEditor<T>({
  legend,
  addLabel,
  hint,
  rows,
  onChange,
  blank,
  render,
}: {
  legend: string;
  addLabel: string;
  hint?: string;
  rows: T[];
  onChange: (rows: T[]) => void;
  blank: () => T;
  render: (row: T, set: (next: T) => void) => React.ReactNode;
}) {
  const setAt = (i: number, next: T) => onChange(rows.map((r, j) => (j === i ? next : r)));
  const removeAt = (i: number) => onChange(rows.filter((_, j) => j !== i));
  return (
    <fieldset>
      <div className="mb-2 flex items-center justify-between">
        <legend className="text-xs font-bold text-ink-2">{legend}</legend>
        <Button size="sm" variant="ghost" onClick={() => onChange([...rows, blank()])}>
          <Plus size={13} strokeWidth={2.75} />
          {addLabel}
        </Button>
      </div>
      {hint && rows.length === 0 && <p className="text-xs font-medium text-ink-3">{hint}</p>}
      <div className="space-y-2">
        {rows.map((row, i) => (
          <div key={i} className="flex items-center gap-2">
            <div className="min-w-0 flex-1">{render(row, (next) => setAt(i, next))}</div>
            <IconButton label="Remove" onClick={() => removeAt(i)} className="h-9 w-9">
              <X size={15} />
            </IconButton>
          </div>
        ))}
      </div>
    </fieldset>
  );
}

function NumberInput({
  value,
  onChange,
  ...rest
}: {
  value: number | "";
  onChange: (n: number) => void;
} & Omit<React.InputHTMLAttributes<HTMLInputElement>, "value" | "onChange">) {
  return (
    <input
      type="number"
      min={1}
      max={65535}
      value={value}
      onChange={(e) => onChange(parseInt(e.target.value, 10) || 0)}
      className="w-24 rounded-xl border border-line bg-surface-2 px-3 py-2.5 text-sm font-medium text-ink"
      {...rest}
    />
  );
}
