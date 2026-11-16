import { useEffect, useState } from "react";
import { Cpu, Loader2, Pencil, Plus, X } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, IconButton, Modal, SelectField, TextField, Toggle, inputClass } from "../form";
import ConfigDialog from "./ConfigDialog";
import {
  blankConfig,
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
  webUi: "",
  icon: "",
  extraParams: "",
  privileged: false,
  vpn: "",
};

/** Fill fields the server omits when they are empty. */
function hydrate(s: Spec): Spec {
  return {
    ...NEW_SPEC,
    ...s,
    vpn: s.vpn ?? "",
    configs: (s.configs ?? []).map((c) => ({ ...blankConfig(c.type), ...c })),
  };
}

function configFilled(c: ConfigItem): boolean {
  switch (c.type) {
    case "path":
      return c.host.trim() !== "" && c.container.trim() !== "";
    case "port":
      return c.containerPort > 0 && c.hostPort > 0;
    case "device":
      return c.host.trim().startsWith("/dev/");
    default:
      return c.key.trim() !== "" && c.value.trim() !== "";
  }
}

/**
 * Create a container, or edit an existing one. Editing loads the container's
 * current settings; saving replaces the container with one that has the new
 * settings (Docker cannot change them in place).
 */
export default function ContainerDialog({
  container,
  preset,
  notes,
  heading,
  requires,
  onClose,
  onSaved,
}: {
  /** The container to edit, or undefined to create one. */
  container?: Container;
  /** A template to create from. Nothing is installed until the form is submitted. */
  preset?: Spec;
  notes?: string[];
  heading?: string;
  /** Unraid Requires text, shown only when the form was opened from the App Store. */
  requires?: string;
  onClose: () => void;
  onSaved: (name: string, edited: boolean) => void;
}) {
  const editing = !!container;
  const fromApp = preset !== undefined;
  const [spec, setSpec] = useState<Spec | null>(editing ? null : hydrate(preset ?? NEW_SPEC));
  const [advanced, setAdvanced] = useState(() =>
    (preset?.configs ?? []).some((c) => c.required && c.display && c.display !== "always" && !configFilled(c)),
  );
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
      .catch(() => setOptions({ networks: ["bridge", "host", "none"], gpus: [], gpuAvailable: false, vpns: [] }));
    api<{ images: Image[] }>("/docker/images")
      .then((r) => setImages(r.images ?? []))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!container) return;
    api<SpecInfo>(`/docker/containers/${encodeURIComponent(container.id)}/spec`)
      .then((res) => {
        setInfo(res);
        setSpec(hydrate(res.spec));
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
    const missing = spec.configs.find((c) => c.required && !configFilled(c));
    if (missing) {
      if (missing.display && missing.display !== "always") setAdvanced(true);
      setError(`${missing.name || missing.key || "A setting"} is required.`);
      return;
    }
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

  const title = container ? `Edit ${container.name}` : (heading ?? "Run a container");
  const showTemplate =
    fromApp || !!(spec && (spec.webUi || spec.extraParams || spec.privileged));
  const hiddenCount = spec
    ? spec.configs.filter((c) => c.display === "advanced" || c.display === "hidden").length
    : 0;

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
          <fieldset disabled={locked || busy} className="min-w-0 space-y-5 [overflow-wrap:anywhere]">
            {fromApp && requires && (
              <div className="rounded-2xl border border-warn/40 bg-warn/15 px-3 py-2 text-sm font-medium leading-relaxed text-ink">
                <span className="font-bold text-warn">This app needs: </span>
                {requires}
              </div>
            )}
            {locked && <Alert tone="warn">{info?.reason}</Alert>}
            {editing && !locked && (
              <Alert tone="info">
                Applying replaces this container with a new one using these settings
                {info?.running ? ", then starts it again" : ""}. FELDCORE takes a snapshot first,
                so you can roll back from the Snapshots panel. Files in mapped folders are kept.
                Anything saved only inside the container is lost.
              </Alert>
            )}

            <div className="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 [&>*]:min-w-0">
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
                className="sm:col-span-2"
                label="Route through VPN"
                value={spec.vpn}
                onChange={(e) => set({ vpn: e.target.value })}
                hint={
                  spec.vpn
                    ? "Uses the tunnel's gateway network. If the tunnel drops, this container has no traffic."
                    : "Leave this off to use the network type above. Add tunnels in Settings → VPN."
                }
              >
                <option value="">Do not use a VPN</option>
                {(options?.vpns ?? []).map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.provider === "proton" ? "Proton" : "WireGuard"}
                    {v.name ? ` · ${v.name}` : ""}
                    {v.country ? ` · ${v.country}` : ""}
                    {v.server ? ` · ${v.server}` : ""}
                    {!v.enabled ? " (off)" : ""}
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

            {spec.vpn && (
              <Alert tone="info">
                This container runs through the VPN gateway
                {(options?.vpns ?? []).some((v) => v.id === spec.vpn && v.provider === "proton")
                  ? " (Proton)"
                  : ""}
                . The kill switch leaves it with no network if the tunnel drops. Published ports move onto the gateway.
                {(options?.vpns ?? [])
                  .filter((v) => v.id === spec.vpn)
                  .map((v) =>
                    v.server || v.country ? ` ${[v.country, v.server].filter(Boolean).join(" · ")}.` : "",
                  )}
              </Alert>
            )}

            <div className="flex min-w-0 items-start gap-3">
              <TextField
                className="min-w-0 flex-1"
                label="Icon URL"
                value={spec.icon}
                onChange={(e) => set({ icon: e.target.value })}
                placeholder="https://example.com/icon.png"
                hint="Shown next to this container. Leave blank to use the default icon."
                autoCapitalize="none"
                spellCheck={false}
              />
              <div className="mt-6">
                <IconPreview key={spec.icon} src={spec.icon} />
              </div>
            </div>

            {showTemplate && <TemplateFields spec={spec} notes={fromApp ? notes : undefined} onChange={set} />}

            <GpuRow spec={spec} options={options} onChange={(gpu) => set({ gpu })} />

            <section>
              <div className="mb-2 flex min-w-0 flex-wrap items-center justify-between gap-2">
                <h3 className="text-xs font-bold text-ink-2">
                  Paths, ports, variables, labels and devices
                </h3>
                <div className="flex flex-wrap items-center gap-2">
                  {hiddenCount > 0 && (
                    <Button size="sm" onClick={() => setAdvanced((v) => !v)}>
                      {advanced ? "Hide advanced" : `Show advanced (${hiddenCount})`}
                    </Button>
                  )}
                  <Button size="sm" onClick={() => setConfigAt(-1)}>
                    <Plus size={13} strokeWidth={2.75} />
                    Add configuration
                  </Button>
                </div>
              </div>
              {spec.configs.length === 0 ? (
                <p className="rounded-2xl border border-dashed border-line px-4 py-5 text-center text-xs font-medium text-ink-3">
                  Nothing mapped yet. Add a path to give the container a folder, or a port to reach
                  it from your network.
                </p>
              ) : (
                <ul className="space-y-2">
                  {spec.configs.map((c, i) => {
                    if (!advanced && (c.display === "advanced" || c.display === "hidden")) return null;
                    return (
                      <ConfigRow
                        key={i}
                        item={c}
                        onEdit={() => setConfigAt(i)}
                        onRemove={() => set({ configs: spec.configs.filter((_, j) => j !== i) })}
                      />
                    );
                  })}
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

function TemplateFields({
  spec,
  notes,
  onChange,
}: {
  spec: Spec;
  notes?: string[];
  onChange: (patch: Partial<Spec>) => void;
}) {
  return (
    <section className="min-w-0 space-y-4 rounded-2xl border border-line p-4">
      <div>
        <h3 className="text-sm font-bold text-ink">From the template</h3>
        <p className="text-xs font-medium text-ink-3">
          Review these settings, then apply. Nothing is installed until you do.
        </p>
      </div>
      {notes && notes.length > 0 && (
        <Alert tone="info">
          <ul className="space-y-1">
            {notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </Alert>
      )}
      <TextField
        label="WebUI"
        value={spec.webUi}
        onChange={(e) => onChange({ webUi: e.target.value })}
        placeholder="http://[IP]:[PORT:8080]/"
        hint="Unraid's link template. [IP] is this server and [PORT:n] is the published port."
        autoCapitalize="none"
        spellCheck={false}
      />
      <div>
        <label htmlFor="feldcore-extra" className="mb-1.5 block text-xs font-bold text-ink-2">
          Extra parameters
        </label>
        <textarea
          id="feldcore-extra"
          value={spec.extraParams}
          onChange={(e) => onChange({ extraParams: e.target.value })}
          rows={2}
          spellCheck={false}
          placeholder="--cap-add=NET_ADMIN"
          className={`${inputClass} resize-y font-mono text-xs [overflow-wrap:anywhere]`}
        />
        <p className="mt-1.5 text-xs font-medium text-ink-3">
          Capabilities, memory, shared memory, DNS, tmpfs and logging options here are applied.
          Anything else is kept so you can see it.
        </p>
      </div>
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-sm font-bold text-ink">Privileged</div>
          <p className="text-xs font-medium text-ink-3">
            Gives the container full access to this server. Leave this off unless the app needs it.
          </p>
        </div>
        <Toggle checked={spec.privileged} onChange={(privileged) => onChange({ privileged })} label="Privileged" />
      </div>
    </section>
  );
}

function IconPreview({ src }: { src: string }) {
  const [failed, setFailed] = useState(false);
  const show = src.trim() !== "" && !failed;
  return (
    <span className="flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded-2xl border border-line bg-surface-2">
      {show ? (
        <img
          src={src}
          alt=""
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
          className="h-full w-full object-contain"
        />
      ) : (
        <span className="text-[9px] font-bold uppercase tracking-wider text-ink-3">Icon</span>
      )}
    </span>
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
    <li className="flex min-w-0 items-start gap-3 rounded-2xl border border-line bg-surface-2 py-2 pl-3 pr-1.5">
      <span className="mt-0.5 w-14 shrink-0 rounded-lg bg-accent/12 py-1 text-center text-[9px] font-extrabold tracking-wider text-accent">
        {TYPE_CHIP[item.type]}
      </span>
      <div className="min-w-0 flex-1">
        {item.name && (
          <div className="text-sm font-bold text-ink [overflow-wrap:anywhere]">
            {item.name}
            {item.required && <span className="text-bad"> *</span>}
          </div>
        )}
        <div className="font-mono text-xs text-ink-2 [overflow-wrap:anywhere]">
          <span>{left}</span>
          <span className="text-ink-3"> {sep} </span>
          <span>{right}</span>
          {readOnly && (
            <span className="ml-1.5 rounded bg-track px-1 py-px font-sans text-[9px] font-bold text-ink-3">
              READ ONLY
            </span>
          )}
          {item.display === "hidden" && (
            <span className="ml-1.5 rounded bg-track px-1 py-px font-sans text-[9px] font-bold text-ink-3">
              HIDDEN
            </span>
          )}
        </div>
        {item.description && (
          <div className="text-[11px] font-medium leading-relaxed text-ink-3 [overflow-wrap:anywhere]">
            {item.description}
          </div>
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
    <section className="flex min-w-0 items-start gap-3.5 rounded-2xl border border-line p-4">
      <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent/12 text-accent">
        <Cpu size={17} />
      </span>
      <div className="min-w-0 flex-1">
        <h3 className="text-sm font-bold text-ink">Use the GPU</h3>
        <p className="mt-0.5 text-xs font-medium leading-relaxed text-ink-3 [overflow-wrap:anywhere]">{detail}</p>
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
