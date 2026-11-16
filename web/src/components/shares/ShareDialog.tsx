import { useState } from "react";
import { Eye, Globe, Lock, type LucideIcon } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, Modal, TextField, Toggle } from "../form";
import type { Overview, Share, ShareInput, SmbAccess } from "./types";

const ACCESS: { key: SmbAccess; label: string; desc: string; icon: LucideIcon }[] = [
  {
    key: "public",
    label: "Public",
    desc: "Anyone on your network can open and change files. No password.",
    icon: Globe,
  },
  {
    key: "readonly",
    label: "Read-only",
    desc: "Anyone on your network can open files. Nobody can change them over SMB.",
    icon: Eye,
  },
  {
    key: "private",
    label: "Private",
    desc: "Only the people you choose, each with their own password.",
    icon: Lock,
  },
];

/** Create a share (no `share`) or edit an existing one. */
export default function ShareDialog({
  share,
  overview,
  onClose,
  onSaved,
}: {
  share?: Share;
  overview: Overview;
  onClose: () => void;
  onSaved: (name: string, created: boolean) => void;
}) {
  const creating = !share;
  const [name, setName] = useState(share?.name ?? "");
  const [comment, setComment] = useState(share?.comment ?? "");
  const [smbOn, setSmbOn] = useState(share?.smb.enabled ?? true);
  const [access, setAccess] = useState<SmbAccess>(share?.smb.access ?? "public");
  const [users, setUsers] = useState<string[]>(share?.smb.users ?? []);
  const [nfsOn, setNfsOn] = useState(share?.nfs.enabled ?? false);
  const [clients, setClients] = useState(share?.nfs.clients || overview.suggestedClients);
  const [nfsRO, setNfsRO] = useState(share?.nfs.readOnly ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const { smb, nfs } = overview.services;

  const toggleUser = (u: string) =>
    setUsers((cur) => (cur.includes(u) ? cur.filter((x) => x !== u) : [...cur, u]));

  const submit = async () => {
    setBusy(true);
    setError(null);
    const body: ShareInput = {
      name: name.trim(),
      comment,
      smb: { enabled: smbOn, access, users: access === "private" ? users : [] },
      nfs: { enabled: nfsOn, clients, readOnly: nfsRO },
    };
    try {
      if (creating) {
        await api("/shares", { method: "POST", body });
      } else {
        await api(`/shares/${encodeURIComponent(share.name)}`, { method: "PUT", body });
      }
      onSaved(body.name, creating);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Modal
      title={creating ? "New share" : `Edit ${share.name}`}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy}>
            {creating ? "Create share" : "Save changes"}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        {creating ? (
          <TextField
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="media"
            hint={
              <>
                Letters, numbers, hyphens and underscores. The folder will be{" "}
                <code className="font-mono">
                  {overview.root}/{name.trim() || "name"}
                </code>
                .
              </>
            }
            maxLength={32}
            autoCapitalize="none"
            spellCheck={false}
            required
            autoFocus
          />
        ) : (
          <p className="text-xs font-medium text-ink-3">
            Folder: <code className="font-mono text-ink-2">{share.path}</code>
          </p>
        )}

        <TextField
          label="Description"
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder="Optional"
          maxLength={100}
        />

        {/* SMB */}
        <section className="rounded-2xl border border-line p-4">
          <div className="flex items-center justify-between gap-4">
            <div>
              <h3 className="text-sm font-bold text-ink">Windows file sharing (SMB)</h3>
              <p className="mt-0.5 text-xs font-medium text-ink-3">
                For Windows, macOS, phones and most media players.
              </p>
            </div>
            <Toggle checked={smbOn} onChange={setSmbOn} label="Share over SMB" />
          </div>

          {smbOn && (
            <div className="mt-4 space-y-3">
              {!smb.installed && (
                <Alert tone="info">
                  Samba isn't installed yet. You can save this now; the share goes live once you
                  install Samba from the Shares page.
                </Alert>
              )}
              <div role="radiogroup" aria-label="Who can access" className="space-y-2">
                {ACCESS.map(({ key, label, desc, icon: Icon }) => {
                  const on = access === key;
                  return (
                    <button
                      key={key}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      onClick={() => setAccess(key)}
                      className={`flex w-full items-start gap-3 rounded-xl border p-3 text-left transition-colors ${
                        on ? "border-accent bg-accent/8" : "border-line hover:border-accent/50"
                      }`}
                    >
                      <Icon size={17} className={`mt-0.5 shrink-0 ${on ? "text-accent" : "text-ink-3"}`} />
                      <span>
                        <span className="block text-sm font-bold text-ink">{label}</span>
                        <span className="block text-xs font-medium text-ink-3">{desc}</span>
                      </span>
                    </button>
                  );
                })}
              </div>

              {access === "private" && (
                <fieldset>
                  <legend className="mb-2 text-xs font-bold text-ink-2">Who has access</legend>
                  {overview.users.length ? (
                    <div className="flex flex-wrap gap-2">
                      {overview.users.map((u) => {
                        const on = users.includes(u);
                        return (
                          <button
                            key={u}
                            type="button"
                            role="checkbox"
                            aria-checked={on}
                            onClick={() => toggleUser(u)}
                            className={`rounded-xl border px-3 py-1.5 text-sm font-bold transition-colors ${
                              on
                                ? "border-accent bg-accent/12 text-accent"
                                : "border-line text-ink-2 hover:border-accent/50"
                            }`}
                          >
                            {u}
                          </button>
                        );
                      })}
                    </div>
                  ) : (
                    <Alert tone="info">
                      There are no share users yet. Close this, add one under "Share users", then
                      come back.
                    </Alert>
                  )}
                </fieldset>
              )}
            </div>
          )}
        </section>

        {/* NFS */}
        <section className="rounded-2xl border border-line p-4">
          <div className="flex items-center justify-between gap-4">
            <div>
              <h3 className="text-sm font-bold text-ink">NFS</h3>
              <p className="mt-0.5 text-xs font-medium text-ink-3">
                For Linux machines, hypervisors and containers.
              </p>
            </div>
            <Toggle checked={nfsOn} onChange={setNfsOn} label="Share over NFS" />
          </div>

          {nfsOn && (
            <div className="mt-4 space-y-4">
              {!nfs.installed && (
                <Alert tone="info">
                  The NFS server isn't installed yet. You can save this now; the export goes live
                  once you install it from the Shares page.
                </Alert>
              )}
              <TextField
                label="Computers allowed to connect"
                value={clients}
                onChange={(e) => setClients(e.target.value)}
                placeholder="192.168.0.0/24"
                hint="A network (192.168.0.0/24), single addresses or hostnames, separated by spaces."
                autoCapitalize="none"
                spellCheck={false}
              />
              {clients.split(/[\s,]+/).includes("*") && (
                <Alert tone="warn">
                  * lets any computer that can reach this server connect. NFS has no passwords, so
                  only use it on a network you trust.
                </Alert>
              )}
              <div className="flex items-center justify-between gap-4">
                <span className="text-sm font-semibold text-ink">Read-only</span>
                <Toggle checked={nfsRO} onChange={setNfsRO} label="Make the NFS export read-only" />
              </div>
            </div>
          )}
        </section>

        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}
