import { useEffect, useState } from "react";
import { Cpu, Loader2, Pencil, Plus, X } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, IconButton, Modal, SelectField, TextField, Toggle } from "../form";
import ConfigDialog from "./ConfigDialog";
import {
  configSummary,
  type ConfigItem,
  type Container,
  type Image,
  type Options,
  type Spec,
  type SpecInfo,
} from "./types";

const RESTART = [
  { key: "no", label: "Never" },
  { key: "unless-stopped", label: "Unless stopped" },
  { key: "always", label: "Always" },
  { key: "on-failure", label: "On failure" },
];

const TYPE_CHIP: Record<ConfigItem["type"], string> = {
  path: "PATH",
  port: "PORT",
  variable: "VAR",
  label: "LABEL",
  device: "DEVICE",
};

const NEW_SPEC: Spec = {
  name: "",
  image: "",
  network: "bridge",
  restartPolicy: "unless-stopped",
  gpu: false,
  configs: [],
  autostart: true,
};

/**
 * Create a container, or edit an existing one. Editing loads the container's
 * current settings; saving replaces the container with one that has the new
 * settings (Docker cannot change them in place).
 */
export default function ContainerDialog({
  container,
  onClose,
  onSaved,
}: {
  /** The container to edit, or undefined to create one. */
  container?: Container;
  onClose: () => void;
  onSaved: (name: string, edited: boolean) => void;
}) {
  const editing = !!container;
  const [spec, setSpec] = useState<Spec | null>(editing ? null : NEW_SPEC);
  const [info, setInfo] = useState<SpecInfo | null>(null);
  const [options, setOptions] = useState<Options | null>(null);
  const [images, setImages] = useState<Image[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Index of the setting open in the sub-dialog; -1 adds a new one.
  const [configAt, setConfigAt] = useState<number | null>(null);

  useEffect(() => {
    api<Options>("/docker/options")
      .then(setOptions)
      .catch(() => setOptions({ networks: ["bridge", "host", "none"], gpus: [], gpuAvailable: false }));
    api<{ images: Image[] }>("/docker/images")
      .then((r) => setImages(r.images ?? []))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!container) return;
    api<SpecInfo>(`/docker/containers/${encodeURIComponent(container.id)}/spec`)
      .then((res) => {
        setInfo(res);
        setSpec({ ...res.spec, configs: res.spec.configs ?? [] });
      })
      .catch((e) => setLoadError(errorMessage(e)));
  }, [container]);

  const set = (patch: Partial<Spec>) => setSpec((cur) => (cur ? { ...cur, ...patch } : cur));
  const locked = editing && info !== null && !info.editable;

  const saveConfig = (item: ConfigItem) => {
    if (!spec || configAt === null) return;
    const configs =
      configAt < 0
        ? [...spec.configs, item]
        : spec.configs.map((c, i) => (i === configAt ? item : c));
    set({ configs });
    setConfigAt(null);
  };

  const submit = async () => {
    if (!spec || locked) return;
    setBusy(true);
    setError(null);
    try {
      if (container) {
        await api(`/docker/containers/${encodeURIComponent(container.id)}`, {
          method: "PUT",
          body: spec,
        });
      } else {
        await api("/docker/containers", { method: "POST", body: spec });
      }
      onSaved(spec.name || spec.image, editing);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  // Offer the container's current network even if Docker no longer lists it.
  const networks = [...(options?.networks ?? ["bridge", "host", "none"])];
  if (spec && !networks.includes(spec.network)) networks.push(spec.network);

  const title = container ? `Edit ${container.name}` : "Run a container";

  return (
    <>
      <Modal
        title={title}
        size="lg"
        onClose={onClose}
        onSubmit={submit}
        footer={
          <>
            <Button onClick={onClose} disabled={busy}>
              {locked ? "Close" : "Cancel"}
            </Button>
            {!locked && (
              <Button type="submit" variant="primary" loading={busy} disabled={!spec}>
                {busy
                  ? "Applying…"
                  : editing
                    ? "Apply"
                    : spec?.autostart
                      ? "Create & start"
                      : "Create"}
              </Button>
            )}
          </>
        }
      >
        {loadError && <Alert>{loadError}</Alert>}

        {!spec && !loadError && (
          <div className="flex items-center justify-center gap-3 py-12 text-sm font-semibold text-ink-2">
            <Loader2 size={17} className="animate-spin text-accent" />
            Reading the container's settings…
          </div>
        )}

        {spec && (
          <fieldset disabled={locked || busy} className="space-y-5">
            {locked && <Alert tone="warn">{info?.reason}</Alert>}
            {editing && !locked && (
              <Alert tone="info">
                Applying replaces this container with a new one using these settings
                {info?.running ? ", then starts it again" : ""}. Files in mapped folders are kept.
                Anything saved only inside the container is lost.
              </Alert>
            )}

            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                label={editing ? "Name" : "Name (optional)"}
                value={spec.name}
                onChange={(e) => set({ name: e.target.value })}
                placeholder={editing ? "" : "Docker picks one if blank"}
                autoCapitalize="none"
                spellCheck={false}
              />
              <div>
                <TextField
                  label="Image"
                  value={spec.image}
                  onChange={(e) => set({ image: e.target.value })}
                  placeholder="nginx:latest"
                  list="feldcore-images"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  autoFocus={!editing}
                />
                <datalist id="feldcore-images">
                  {images.map((i) => (
                    <option key={i.id + i.tag} value={i.tag} />
                  ))}
                </datalist>
              </div>
              <SelectField
                label="Network type"
                value={spec.network}
                onChange={(e) => set({ network: e.target.value })}
                hint={NETWORK_HINT[spec.network] ?? "A network you created in Docker."}
              >
                {networks.map((n) => (
                  <option key={n} value={n}>
                    {n === "bridge" ? "Bridge" : n === "host" ? "Host" : n === "none" ? "None" : n}
                  </option>
                ))}
              </SelectField>
              <SelectField
                label="Restart policy"
                value={spec.restartPolicy}
                onChange={(e) => set({ restartPolicy: e.target.value })}
              >
                {RESTART.map((r) => (
                  <option key={r.key} value={r.key}>
                    {r.label}
                  </option>
                ))}
              </SelectField>
            </div>

            <GpuRow spec={spec} options={options} onChange={(gpu) => set({ gpu })} />

            <section>
              <div className="mb-2 flex items-center justify-between">
                <h3 className="text-xs font-bold text-ink-2">
                  Paths, ports, variables, labels and devices
                </h3>
                <Button size="sm" onClick={() => setConfigAt(-1)}>
                  <Plus size={13} strokeWidth={2.75} />
                  Add configuration
                </Button>
              </div>
              {spec.configs.length === 0 ? (
                <p className="rounded-2xl border border-dashed border-line px-4 py-5 text-center text-xs font-medium text-ink-3">
                  Nothing mapped yet. Add a path to give the container a folder, or a port to reach
                  it from your network.
                </p>
              ) : (
                <ul className="space-y-2">
                  {spec.configs.map((c, i) => (
                    <ConfigRow
                      key={i}
                      item={c}
                      onEdit={() => setConfigAt(i)}
                      onRemove={() => set({ configs: spec.configs.filter((_, j) => j !== i) })}
                    />
                  ))}
                </ul>
              )}
            </section>

            {!editing && (
              <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
                Start the container now
                <Toggle
                  checked={spec.autostart}
                  onChange={(autostart) => set({ autostart })}
                  label="Start the container now"
                />
              </label>
            )}

            {error && <Alert>{error}</Alert>}
          </fieldset>
        )}
      </Modal>

      {/* A sibling of the dialog above, not a child: forms cannot nest. */}
      {spec && configAt !== null && (
        <ConfigDialog
          initial={configAt >= 0 ? spec.configs[configAt] : undefined}
          onClose={() => setConfigAt(null)}
          onSave={saveConfig}
        />
      )}
    </>
  );
}

const NETWORK_HINT: Record<string, string> = {
  bridge: "Isolated from your network; reachable through the ports you map.",
  host: "Shares this server's network directly. Port mappings are ignored.",
  none: "No network access at all.",
};

function ConfigRow({
  item,
  onEdit,
  onRemove,
}: {
  item: ConfigItem;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const { left, right } = configSummary(item);
  const sep = item.type === "variable" || item.type === "label" ? "=" : "→";
  const readOnly = item.type === "path" && item.mode.split(",").includes("ro");
  return (
    <li className="flex items-center gap-3 rounded-2xl border border-line bg-surface-2 py-2 pl-3 pr-1.5">
      <span className="w-14 shrink-0 rounded-lg bg-accent/12 py-1 text-center text-[9px] font-extrabold tracking-wider text-accent">
        {TYPE_CHIP[item.type]}
      </span>
      <div className="min-w-0 flex-1">
        {item.name && <div className="truncate text-sm font-bold text-ink">{item.name}</div>}
        <div className="flex min-w-0 items-center gap-1.5 font-mono text-xs text-ink-2">
          <span className="truncate">{left}</span>
          <span className="shrink-0 text-ink-3">{sep}</span>
          <span className="truncate">{right}</span>
          {readOnly && (
            <span className="shrink-0 rounded bg-track px-1 py-px font-sans text-[9px] font-bold text-ink-3">
              READ ONLY
            </span>
          )}
        </div>
        {item.description && (
          <div className="truncate text-[11px] font-medium text-ink-3">{item.description}</div>
        )}
      </div>
      <IconButton label={`Edit ${item.name || TYPE_CHIP[item.type].toLowerCase()}`} onClick={onEdit}>
        <Pencil size={14} />
      </IconButton>
      <IconButton label={`Remove ${item.name || TYPE_CHIP[item.type].toLowerCase()}`} onClick={onRemove}>
        <X size={15} />
      </IconButton>
    </li>
  );
}

function GpuRow({
  spec,
  options,
  onChange,
}: {
  spec: Spec;
  options: Options | null;
  onChange: (gpu: boolean) => void;
}) {
  const gpus = options?.gpus ?? [];
  const usable = gpus.filter((g) => g.supported);
  const blocked = gpus.filter((g) => !g.supported);
  const available = options?.gpuAvailable ?? false;

  let detail: string;
  if (!options) detail = "Checking for a GPU…";
  else if (usable.length) {
    detail = `Share ${usable.map((g) => g.name).join(" and ")} with this container, for hardware transcoding and acceleration. Other containers can use it at the same time.`;
  } else if (blocked.length) detail = blocked[0].note ?? "This GPU can't be shared yet.";
  else detail = "No GPU was found on this server.";

  return (
    <section className="flex items-start gap-3.5 rounded-2xl border border-line p-4">
      <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent/12 text-accent">
        <Cpu size={17} />
      </span>
      <div className="min-w-0 flex-1">
        <h3 className="text-sm font-bold text-ink">Use the GPU</h3>
        <p className="mt-0.5 text-xs font-medium leading-relaxed text-ink-3">{detail}</p>
        {usable.length > 0 && blocked.length > 0 && (
          <p className="mt-1 text-xs font-medium text-ink-3">
            {blocked.map((g) => `${g.name}: ${g.note}`).join(" ")}
          </p>
        )}
      </div>
      <Toggle
        checked={spec.gpu}
        onChange={onChange}
        label="Use the GPU"
        // Allow switching it off even if the GPU has since disappeared.
        disabled={!available && !spec.gpu}
      />
    </section>
  );
}
