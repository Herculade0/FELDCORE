import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ArrowLeft, Download, Plus, Trash2 } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes } from "../../lib/format";
import { Card } from "../ui";
import { Alert, Button, SelectField, TextField, Toggle, inputClass } from "../form";
import { useVirtio } from "./useVirtio";
import { suggest, type Detail, type Disk, type Host, type PCIDevice, type Spec } from "./types";

export default function VMForm({
  editing,
  initial,
  host,
  onCancel,
  onSaved,
}: {
  editing?: string;
  initial: Spec;
  host: Host;
  onCancel: () => void;
  onSaved: (message: string) => void;
}) {
  const [spec, setSpec] = useState<Spec>(initial);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [loading, setLoading] = useState(!!editing);
  const [saving, setSaving] = useState(false);
  const [start, setStart] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hint, setHint] = useState<string | null>(null);
  const virtio = useVirtio();

  useEffect(() => {
    if (!editing) return;
    void api<Detail>(`/vms/${encodeURIComponent(editing)}`)
      .then((d) => {
        setSpec({
          ...d.spec,
          cpus: d.spec.cpus ?? [],
          disks: d.spec.disks?.length ? d.spec.disks : initial.disks,
          usbs: d.spec.usbs ?? [],
          pcis: d.spec.pcis ?? [],
          shares: d.spec.shares ?? [],
          cloudInit: d.spec.cloudInit ?? { enabled: false },
        });
        setWarnings(d.warnings ?? []);
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setLoading(false));
  }, [editing, initial.disks]);

  const displays = useMemo(
    () => host.iommu.flatMap((g) => g.devices).filter((d) => d.class === "Display"),
    [host.iommu],
  );
  const virtioISOs = useMemo(() => {
    const fromHost = (host.isos ?? []).filter((f) => f.virtio);
    const extra = (virtio.view?.installed ?? [])
      .filter((f) => !fromHost.some((h) => h.name === f.name))
      .map((f) => ({ name: f.name, size: f.size, virtio: true }));
    return [...fromHost, ...extra];
  }, [host.isos, virtio.view]);
  const groupNote = partialGroup(spec, host);

  const set = (patch: Partial<Spec>) => setSpec((s) => ({ ...s, ...patch }));

  const toggleCPU = (id: number) => {
    setSpec((s) => {
      const has = s.cpus.includes(id);
      return { ...s, cpus: has ? s.cpus.filter((c) => c !== id) : [...s.cpus, id].sort((a, b) => a - b) };
    });
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const body = { ...spec, start: !editing && start };
      const res = await api<{ name: string; warning?: string }>(
        editing ? `/vms/${encodeURIComponent(editing)}` : "/vms",
        { method: editing ? "PUT" : "POST", body },
      );
      onSaved(res.warning || (editing ? `${res.name} was updated.` : `${res.name} was created.`));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <header>
        <button
          type="button"
          onClick={onCancel}
          className="mb-3 inline-flex items-center gap-1.5 text-xs font-bold text-ink-3 hover:text-accent"
        >
          <ArrowLeft size={14} />
          Virtual machines
        </button>
        <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">
          {editing ? `Edit ${editing}` : "Add VM"}
        </h1>
        <p className="mt-2 max-w-2xl text-sm font-semibold text-ink-2">
          Each selected CPU core is one vCPU, pinned to that core. Disks are created on the domains
          share. VNC stays on this machine and opens in the browser.
        </p>
      </header>

      {!host.kvm && host.kvmMessage && <Alert tone="warn">{host.kvmMessage}</Alert>}
      {error && <Alert>{error}</Alert>}
      {virtio.error && <Alert>{virtio.error}</Alert>}
      {warnings.map((w) => (
        <Alert key={w} tone="warn">
          {w} Saving this form rewrites the domain, so anything it does not show is removed. Use Edit
          XML to keep those devices.
        </Alert>
      ))}
      {loading && <p className="text-sm font-semibold text-ink-3">Reading the VM…</p>}

      <Section title="Basics">
        <div className="grid gap-4 sm:grid-cols-2">
          <TextField
            label="Name"
            value={spec.name}
            disabled={!!editing}
            onChange={(e) => set({ name: e.target.value })}
            placeholder="debian"
            hint="Letters, numbers, dots and hyphens."
          />
          <SelectField
            label="Operating system"
            value={spec.os || "other"}
            onChange={(e) => set({ os: e.target.value })}
            hint="This only picks suggested hardware. Nothing is downloaded."
          >
            <option value="linux">Linux</option>
            <option value="windows">Windows 10 / 11</option>
            <option value="windows-legacy">Windows (legacy)</option>
            <option value="other">Other</option>
          </SelectField>
        </div>
        <div className="mt-4">
          <TextField
            label="Description"
            value={spec.description || ""}
            onChange={(e) => set({ description: e.target.value })}
            placeholder="Optional"
          />
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <Button
            type="button"
            onClick={() => {
              setSpec((s) => suggest(s.os || "other", s));
              setHint("Suggested hardware applied. Review the disks, firmware and network before saving.");
            }}
          >
            Use suggested hardware
          </Button>
          {hint && <span className="text-xs font-semibold text-ink-3">{hint}</span>}
        </div>
      </Section>

      <Section title="CPU pinning" hint={`${spec.cpus.length} vCPU${spec.cpus.length === 1 ? "" : "s"}`}>
        <p className="mb-3 text-xs font-medium text-ink-3">
          Tick the host cores this VM may run on. The number of ticks is the number of vCPUs, and
          each vCPU is pinned to the core you chose.
          {host.cores.some((c) => c.sibling >= 0) && " Cores that share a number are hyperthreads of one physical core."}
        </p>
        <div className="grid grid-cols-4 gap-2 sm:grid-cols-8 lg:grid-cols-12">
          {host.cores.map((c) => {
            const on = spec.cpus.includes(c.id);
            return (
              <button
                key={c.id}
                type="button"
                aria-pressed={on}
                onClick={() => toggleCPU(c.id)}
                className={`rounded-xl px-1 py-2 text-center transition ${
                  on ? "accent-gradient text-white shadow-md shadow-accent/25" : "border border-line bg-surface-2 text-ink"
                }`}
              >
                <div className="text-sm font-extrabold">{c.id}</div>
                <div className={`text-[10px] font-semibold ${on ? "text-white/80" : "text-ink-3"}`}>
                  {c.sibling >= 0 ? `core ${c.core}` : "cpu"}
                </div>
              </button>
            );
          })}
        </div>
      </Section>

      <Section title="Memory">
        <div className="grid gap-4 sm:grid-cols-2">
          <TextField
            label="Initial"
            type="number"
            min={128}
            value={spec.memoryMB}
            onChange={(e) => set({ memoryMB: Number(e.target.value) })}
            hint="MB given to the guest at boot."
          />
          <TextField
            label="Maximum"
            type="number"
            min={128}
            value={spec.maxMemoryMB}
            onChange={(e) => set({ maxMemoryMB: Number(e.target.value) })}
            hint="The balloon can grow the guest up to this."
          />
        </div>
        {host.memoryMB > 0 && spec.maxMemoryMB > host.memoryMB && (
          <p className="mt-3 text-xs font-semibold text-warn">
            The maximum is above this host's {Math.round(host.memoryMB / 1024)} GB of RAM.
          </p>
        )}
      </Section>

      <Section title="Machine and firmware">
        <div className="grid gap-4 sm:grid-cols-2">
          <Choice
            on={spec.machine === "q35"}
            title="Q35"
            body="PCIe. Use this for Linux and for Windows 10 or 11."
            onClick={() => set({ machine: "q35" })}
          />
          <Choice
            on={spec.machine === "i440fx"}
            title="i440fx"
            body="The older PC machine. Use this for legacy Windows and DOS."
            onClick={() => set({ machine: "i440fx", tpm: false })}
          />
        </div>
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <Choice
            on={spec.bios === "ovmf"}
            title="OVMF (UEFI)"
            body={host.ovmf ? "UEFI firmware is installed." : "The ovmf package is not installed yet."}
            onClick={() => set({ bios: "ovmf" })}
          />
          <Choice
            on={spec.bios === "seabios"}
            title="SeaBIOS"
            body="Legacy BIOS. No UEFI variables and no TPM."
            onClick={() => set({ bios: "seabios", tpm: false })}
          />
        </div>
        <label className="mt-4 flex items-center justify-between gap-4 rounded-2xl border border-line px-4 py-3">
          <span>
            <span className="block text-sm font-bold text-ink">TPM 2.0 (swtpm)</span>
            <span className="mt-0.5 block text-xs font-medium text-ink-3">
              {host.swtpm
                ? "Windows 11 expects a TPM. It requires OVMF."
                : "swtpm is not installed. Turn on libvirt in Settings, or leave this off."}
            </span>
          </span>
          <Toggle
            label="TPM"
            checked={spec.tpm}
            disabled={spec.bios !== "ovmf"}
            onChange={(tpm) => set({ tpm })}
          />
        </label>
      </Section>

      <Section title="Virtual disks">
        <div className="space-y-3">
          {spec.disks.map((d, i) => (
            <DiskRow
              key={d.path || i}
              disk={d}
              onChange={(disk) =>
                setSpec((s) => ({ ...s, disks: s.disks.map((x, n) => (n === i ? disk : x)) }))
              }
              onRemove={() => setSpec((s) => ({ ...s, disks: s.disks.filter((_, n) => n !== i) }))}
            />
          ))}
        </div>
        <Button
          type="button"
          className="mt-3"
          onClick={() =>
            setSpec((s) => ({
              ...s,
              disks: [...s.disks, { sizeGB: 20, format: "qcow2", bus: s.machine === "i440fx" ? "ide" : "virtio" }],
            }))
          }
        >
          <Plus size={14} />
          Add disk
        </Button>
      </Section>

      <Section title="Install media">
        <div className="grid gap-4 sm:grid-cols-2">
          <SelectField
            label="ISO"
            value={spec.installISO || ""}
            onChange={(e) => set({ installISO: e.target.value })}
            hint={`Files in ${host.isosPath}. Copy an ISO there to see it here.`}
          >
            <option value="">None</option>
            {host.isos.filter((f) => !f.virtio).map((f) => (
              <option key={f.name} value={f.name}>
                {f.name} ({bytes(f.size)})
              </option>
            ))}
          </SelectField>
          <SelectField
            label="VirtIO drivers ISO"
            value={spec.virtioISO || ""}
            onChange={(e) => set({ virtioISO: e.target.value })}
            hint="Windows needs this to see a virtio disk and NIC."
          >
            <option value="">{virtioISOs.length ? "None" : "None downloaded yet"}</option>
            {virtioISOs.map((f) => (
              <option key={f.name} value={f.name}>
                {f.name}
              </option>
            ))}
          </SelectField>
        </div>
        {virtioISOs.length === 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <span className="text-xs font-semibold text-ink-3">No VirtIO ISO in the ISO folder.</span>
            <Button type="button" onClick={() => void virtio.download("stable")} disabled={!!virtio.job && !virtio.job.finished}>
              <Download size={14} />
              Download now
            </Button>
          </div>
        )}
        {virtio.job && (
          <p className="mt-2 text-xs font-semibold text-ink-2">
            {virtio.job.error ||
              (virtio.job.finished ? `Saved ${virtio.job.filename}. Reopen this list after it appears.` : virtio.job.label)}
            {!virtio.job.finished && virtio.job.total > 0 && ` · ${virtio.job.percent}%`}
          </p>
        )}
        {spec.installISO && (
          <label className="mt-4 flex items-center gap-3 text-sm font-semibold text-ink">
            <input
              type="checkbox"
              checked={spec.bootISO}
              onChange={(e) => set({ bootISO: e.target.checked })}
              className="h-4 w-4 accent-accent"
            />
            Boot from the ISO first
          </label>
        )}
      </Section>

      <Section title="Cloud-init">
        <label className="flex items-center justify-between gap-4">
          <span className="text-sm font-medium text-ink-2">
            Attach a NoCloud seed. Use it with a cloud image you already have in the ISO or domains
            folder. The password is stored only in that seed, next to the disk.
          </span>
          <Toggle label="Cloud-init" checked={spec.cloudInit.enabled} onChange={(enabled) => set({ cloudInit: { ...spec.cloudInit, enabled } })} />
        </label>
        {spec.cloudInit.enabled && (
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            <TextField
              label="User"
              value={spec.cloudInit.user || ""}
              onChange={(e) => set({ cloudInit: { ...spec.cloudInit, user: e.target.value } })}
              placeholder="core"
            />
            <TextField
              label="Password"
              type="password"
              value={spec.cloudInit.password || ""}
              autoComplete="new-password"
              onChange={(e) => set({ cloudInit: { ...spec.cloudInit, password: e.target.value } })}
              hint={editing ? "Leave blank to keep the current seed." : "Or provide an SSH key."}
            />
            <TextField
              className="sm:col-span-2"
              label="SSH public key"
              value={spec.cloudInit.sshKey || ""}
              onChange={(e) => set({ cloudInit: { ...spec.cloudInit, sshKey: e.target.value } })}
              placeholder="ssh-ed25519 AAAA…"
            />
            <SelectField
              className="sm:col-span-2"
              label="Base image"
              value={spec.cloudInit.baseImage || ""}
              onChange={(e) => set({ cloudInit: { ...spec.cloudInit, baseImage: e.target.value } })}
              hint="Optional. The first disk is created as a qcow2 layer on this image."
            >
              <option value="">Empty disk</option>
              {host.images.map((f) => (
                <option key={f.name} value={f.name}>
                  {f.name}
                </option>
              ))}
            </SelectField>
          </div>
        )}
      </Section>

      <Section title="Graphics and sound">
        <div className="grid gap-4 sm:grid-cols-2">
          <Choice
            on={spec.graphics === "vnc"}
            title="VNC"
            body="Console in the browser. The server listens on 127.0.0.1 only."
            onClick={() => set({ graphics: "vnc", gpu: "" })}
          />
          <Choice
            on={spec.graphics === "gpu"}
            title="Physical GPU"
            body={displays.length ? "The passed-through card is the display. No VNC console." : "No display device was found."}
            onClick={() => displays[0] && set({ graphics: "gpu", gpu: displays[0].address })}
          />
        </div>
        {spec.graphics === "vnc" ? (
          <div className="mt-4">
            <SelectField label="Video" value={spec.video || "qxl"} onChange={(e) => set({ video: e.target.value })}>
              <option value="qxl">QXL</option>
              <option value="virtio">VirtIO</option>
              <option value="cirrus">Cirrus</option>
              <option value="vga">Standard VGA</option>
              <option value="bochs">Bochs</option>
            </SelectField>
          </div>
        ) : (
          <div className="mt-4">
            <SelectField label="GPU" value={spec.gpu || ""} onChange={(e) => set({ gpu: e.target.value, graphics: "gpu" })}>
              {displays.map((d) => (
                <option key={d.address} value={d.address}>
                  {d.name} ({d.address})
                </option>
              ))}
            </SelectField>
          </div>
        )}
        <div className="mt-4">
          <SelectField label="Sound card" value={spec.sound || "none"} onChange={(e) => set({ sound: e.target.value })}>
            <option value="ich9">ICH9</option>
            <option value="ich6">ICH6</option>
            <option value="ac97">AC97</option>
            <option value="sb16">Sound Blaster 16</option>
            <option value="none">None</option>
          </SelectField>
        </div>
      </Section>

      <Section title="Network">
        <div className="grid gap-4 sm:grid-cols-2">
          <Choice
            on={spec.network === "nat"}
            title="NAT"
            body="libvirt's default network (virbr0). The guest is not on your LAN."
            onClick={() => set({ network: "nat", bridge: "" })}
          />
          <Choice
            on={spec.network === "bridge"}
            title={spec.bridge || "Bridge"}
            body={
              host.bridges.length
                ? "Put the guest on a LAN bridge such as br0."
                : "No LAN bridge was found. Create br0 on the host, or use NAT."
            }
            onClick={() => host.bridges[0] && set({ network: "bridge", bridge: host.bridges.includes("br0") ? "br0" : host.bridges[0] })}
          />
        </div>
        {spec.network === "bridge" && host.bridges.length > 0 && (
          <div className="mt-4">
            <SelectField label="Bridge" value={spec.bridge || host.bridges[0]} onChange={(e) => set({ bridge: e.target.value })}>
              {host.bridges.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </SelectField>
          </div>
        )}
        <div className="mt-4">
          <SelectField label="NIC model" value={spec.nic || "virtio"} onChange={(e) => set({ nic: e.target.value })}>
            <option value="virtio">VirtIO</option>
            <option value="e1000">e1000</option>
            <option value="e1000e">e1000e</option>
            <option value="rtl8139">rtl8139</option>
          </SelectField>
        </div>
      </Section>

      <Section title="USB devices">
        {(host.usb ?? []).length === 0 ? (
          <p className="text-sm font-medium text-ink-2">No USB devices to pass through.</p>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            {host.usb.map((u) => {
              const on = (spec.usbs ?? []).some((x) => x.vendor === u.vendor && x.product === u.product);
              return (
                <label key={`${u.vendor}:${u.product}:${u.name}`} className="flex items-start gap-3 rounded-2xl border border-line px-3 py-2.5">
                  <input
                    type="checkbox"
                    className="mt-1 h-4 w-4 accent-accent"
                    checked={on}
                    onChange={() =>
                      set({
                        usbs: on
                          ? (spec.usbs ?? []).filter((x) => !(x.vendor === u.vendor && x.product === u.product))
                          : [...(spec.usbs ?? []), { vendor: u.vendor, product: u.product, name: u.name }],
                      })
                    }
                  />
                  <span>
                    <span className="block text-sm font-bold text-ink">{u.name}</span>
                    <span className="text-[11px] font-semibold text-ink-3">
                      {u.vendor}:{u.product}
                    </span>
                  </span>
                </label>
              );
            })}
          </div>
        )}
      </Section>

      <Section title="PCI devices" hint="IOMMU groups">
        {host.iommuMessage && <Alert tone="warn">{host.iommuMessage}</Alert>}
        {groupNote && (
          <p className="mb-3 text-xs font-semibold text-warn">{groupNote}</p>
        )}
        <div className="space-y-3">
          {host.iommu.map((g) => (
            <div key={g.id} className="rounded-2xl border border-line p-3">
              <div className="mb-2 flex items-center justify-between gap-3">
                <span className="text-xs font-extrabold uppercase tracking-wider text-ink-3">
                  {g.id < 0 ? "No IOMMU group" : `Group ${g.id}`}
                </span>
                {g.id >= 0 && (
                  <button
                    type="button"
                    className="text-xs font-bold text-accent"
                    onClick={() => passGroup(g.devices, spec, setSpec)}
                  >
                    Pass this group
                  </button>
                )}
              </div>
              <div className="space-y-1.5">
                {g.devices.map((d) => {
                  const isGPU = spec.graphics === "gpu" && spec.gpu === d.address;
                  const on = isGPU || (spec.pcis ?? []).includes(d.address);
                  return (
                    <label key={d.address} className="flex items-start gap-3 text-sm">
                      <input
                        type="checkbox"
                        className="mt-1 h-4 w-4 accent-accent"
                        checked={on}
                        disabled={isGPU}
                        onChange={() =>
                          set({
                            pcis: on
                              ? (spec.pcis ?? []).filter((p) => p !== d.address)
                              : [...(spec.pcis ?? []), d.address],
                          })
                        }
                      />
                      <span className="min-w-0">
                        <span className="block font-bold text-ink">{d.name}</span>
                        <span className="text-[11px] font-semibold text-ink-3">
                          {d.class} · {d.address}
                          {isGPU ? " · display" : ""}
                        </span>
                      </span>
                    </label>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Host shares">
        <p className="mb-3 text-xs font-medium text-ink-3">
          virtio-fs mounts a share inside the guest. The tag is the share name.
          {!host.virtiofsd && " virtiofsd is not installed, so this stays off until it is."}
        </p>
        <div className="grid gap-2 sm:grid-cols-2">
          {(host.shares ?? []).map((sh) => {
            const on = (spec.shares ?? []).includes(sh.name);
            return (
              <label key={sh.name} className="flex items-start gap-3 rounded-2xl border border-line px-3 py-2.5">
                <input
                  type="checkbox"
                  className="mt-1 h-4 w-4 accent-accent"
                  disabled={!host.virtiofsd}
                  checked={on}
                  onChange={() =>
                    set({
                      shares: on ? (spec.shares ?? []).filter((n) => n !== sh.name) : [...(spec.shares ?? []), sh.name],
                    })
                  }
                />
                <span>
                  <span className="block text-sm font-bold text-ink">{sh.name}</span>
                  <span className="block truncate text-[11px] font-semibold text-ink-3">{sh.path}</span>
                </span>
              </label>
            );
          })}
        </div>
      </Section>

      <Section title="Start up">
        <label className="flex items-center justify-between gap-4">
          <span className="text-sm font-medium text-ink-2">Start this VM when the host boots.</span>
          <Toggle label="Autostart" checked={spec.autostart} onChange={(autostart) => set({ autostart })} />
        </label>
        {!editing && (
          <label className="mt-3 flex items-center gap-3 text-sm font-semibold text-ink">
            <input type="checkbox" className="h-4 w-4 accent-accent" checked={start} onChange={(e) => setStart(e.target.checked)} />
            Start after creating
          </label>
        )}
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <SelectField label="Clock" value={spec.clock || "utc"} onChange={(e) => set({ clock: e.target.value })}>
            <option value="utc">UTC (Linux)</option>
            <option value="localtime">Local time (Windows)</option>
          </SelectField>
          <label className="flex items-center justify-between gap-4 rounded-2xl border border-line px-4 py-3">
            <span className="text-sm font-bold text-ink">Hyper-V enlightenments</span>
            <Toggle label="Hyper-V" checked={spec.hyperv} onChange={(hyperv) => set({ hyperv })} />
          </label>
        </div>
      </Section>

      <div className="flex flex-wrap justify-end gap-3">
        <Button type="button" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" loading={saving}>
          {editing ? "Save changes" : "Create VM"}
        </Button>
      </div>
    </form>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <Card>
      <div className="mb-4 flex items-baseline justify-between gap-3">
        <h2 className="text-sm font-extrabold text-ink">{title}</h2>
        {hint && <span className="text-xs font-semibold text-ink-3">{hint}</span>}
      </div>
      {children}
    </Card>
  );
}

function Choice({ on, title, body, onClick }: { on: boolean; title: string; body: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      className={`rounded-2xl border px-4 py-3 text-left transition ${
        on ? "border-accent bg-accent/10" : "border-line bg-surface-2 hover:border-accent/50"
      }`}
    >
      <span className="block text-sm font-extrabold text-ink">{title}</span>
      <span className="mt-1 block text-xs font-medium leading-relaxed text-ink-2">{body}</span>
    </button>
  );
}

function DiskRow({
  disk,
  onChange,
  onRemove,
}: {
  disk: Disk;
  onChange: (d: Disk) => void;
  onRemove: () => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-[1fr_8rem_8rem_auto] sm:items-end">
      <label className="col-span-2 sm:col-span-1">
        <span className="mb-1.5 block text-xs font-bold text-ink-2">Size (GB)</span>
        <input
          type="number"
          min={1}
          value={disk.sizeGB}
          onChange={(e) => onChange({ ...disk, sizeGB: Number(e.target.value) })}
          className={inputClass}
        />
      </label>
      <label>
        <span className="mb-1.5 block text-xs font-bold text-ink-2">Format</span>
        <select value={disk.format} onChange={(e) => onChange({ ...disk, format: e.target.value })} className={inputClass}>
          <option value="qcow2">qcow2</option>
          <option value="raw">raw</option>
        </select>
      </label>
      <label>
        <span className="mb-1.5 block text-xs font-bold text-ink-2">Bus</span>
        <select value={disk.bus} onChange={(e) => onChange({ ...disk, bus: e.target.value })} className={inputClass}>
          <option value="virtio">VirtIO</option>
          <option value="sata">SATA</option>
          <option value="scsi">SCSI</option>
          <option value="ide">IDE</option>
        </select>
      </label>
      <button type="button" onClick={onRemove} aria-label="Remove disk" className="mb-1 flex h-11 w-11 items-center justify-center rounded-xl text-ink-3 hover:bg-bad/10 hover:text-bad">
        <Trash2 size={16} />
      </button>
      {disk.path && <p className="col-span-full truncate text-[11px] font-semibold text-ink-3">{disk.path}</p>}
    </div>
  );
}

function passGroup(devices: PCIDevice[], spec: Spec, setSpec: (fn: (s: Spec) => Spec) => void) {
  const gpu = spec.graphics === "gpu" ? spec.gpu : "";
  const add = devices.map((d) => d.address).filter((a) => a !== gpu);
  setSpec((s) => ({ ...s, pcis: Array.from(new Set([...(s.pcis ?? []), ...add])) }));
}

function partialGroup(spec: Spec, host: Host): string | null {
  const selected = new Set([...(spec.pcis ?? []), spec.graphics === "gpu" && spec.gpu ? spec.gpu : ""]);
  selected.delete("");
  for (const g of host.iommu) {
    if (g.id < 0) continue;
    const addrs = g.devices.map((d) => d.address);
    const hit = addrs.filter((a) => selected.has(a));
    if (hit.length > 0 && hit.length < addrs.length) {
      return `IOMMU group ${g.id} is only partly selected. Devices in one group are isolated together, so passing one and leaving the others on the host often fails.`;
    }
  }
  return null;
}
