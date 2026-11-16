import { useEffect, useState } from "react";
import { Check, Copy, KeyRound, Trash2 } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { Card, CardTitle } from "./ui";
import { Alert, Button, Modal, SelectField, TextField, Toggle } from "./form";

type Scope = "read" | "containers" | "vms" | "files:read" | "files:write" | "storage" | "backups:read" | "backups:write" | "admin";

interface Token {
  id: string;
  name: string;
  prefix: string;
  scopes: Scope[];
  createdAt: string;
  expiresAt?: string;
  lastUsedAt?: string;
  revoked: boolean;
}

interface Created extends Token {
  token: string;
}

const SCOPES: { key: Scope; label: string; hint: string }[] = [
  {
    key: "read",
    label: "Read",
    hint: "System, shares, the App Store, containers, VMs, health, and the notification inbox. The MCP server needs this.",
  },
  {
    key: "containers",
    label: "Containers",
    hint: "Create, start, stop, restart and delete containers over the API.",
  },
  {
    key: "vms",
    label: "VMs",
    hint: "Change virtual machines over the API, once that layer exists.",
  },
  {
    key: "files:read",
    label: "Files, read",
    hint: "List, preview and download files in a share.",
  },
  {
    key: "files:write",
    label: "Files, write",
    hint: "Upload, rename, move, copy and delete files in a share. Includes reading them.",
  },
  {
    key: "storage",
    label: "Storage",
    hint: "Create and change pools, mount them, and run scrub, balance and trim. Looking at disks is included in Read.",
  },
  {
    key: "backups:read",
    label: "Backups, read",
    hint: "See destinations, jobs, copies and the files inside a copy.",
  },
  {
    key: "backups:write",
    label: "Backups, write",
    hint: "Run backups, checks and restores, and change destinations. Includes reading them.",
  },
  {
    key: "admin",
    label: "Admin",
    hint: "Settings, shares, tokens and every other API call. Includes the scopes above.",
  },
];

const EXPIRY = [
  { value: "0", label: "Never" },
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
];

function when(iso: string | null | undefined, empty: string): string {
  if (!iso) return empty;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return empty;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function isExpired(iso?: string): boolean {
  if (!iso) return false;
  const t = new Date(iso).getTime();
  return !Number.isNaN(t) && t <= Date.now();
}

export default function AgentAccessCard({ onOpenAudit }: { onOpenAudit?: () => void }) {
  const [tokens, setTokens] = useState<Token[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [days, setDays] = useState("30");
  const [scopes, setScopes] = useState<Record<Scope, boolean>>({
    read: true,
    containers: false,
    vms: false,
    "files:read": false,
    "files:write": false,
    storage: false,
    "backups:read": false,
    "backups:write": false,
    admin: false,
  });
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Created | null>(null);
  const [copied, setCopied] = useState(false);
  const [revoke, setRevoke] = useState<Token | null>(null);
  const [revokeAll, setRevokeAll] = useState(false);
  const [revoking, setRevoking] = useState(false);

  const load = () =>
    api<{ tokens: Token[] }>("/agent/tokens")
      .then((r) => {
        setTokens(r.tokens);
        setLoadError(null);
      })
      .catch((e) => setLoadError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  const chosen = SCOPES.map((s) => s.key).filter((k) => scopes[k]);
  const active = (tokens ?? []).filter((t) => !t.revoked);

  const create = () => {
    if (!name.trim()) {
      setFormError("Name the token.");
      return;
    }
    if (chosen.length === 0) {
      setFormError("Choose at least one scope.");
      return;
    }
    setBusy(true);
    setFormError(null);
    api<Created>("/agent/tokens", {
      method: "POST",
      body: { name: name.trim(), scopes: chosen, expiresInDays: Number(days) },
    })
      .then((created) => {
        setRevealed(created);
        setCopied(false);
        setName("");
        void load();
      })
      .catch((e) => setFormError(errorMessage(e)))
      .finally(() => setBusy(false));
  };

  const doRevoke = (id: string) => {
    setRevoking(true);
    api("/agent/tokens/" + encodeURIComponent(id), { method: "DELETE" })
      .then(() => {
        setRevoke(null);
        void load();
      })
      .catch((e) => setFormError(errorMessage(e)))
      .finally(() => setRevoking(false));
  };

  const doRevokeAll = () => {
    setRevoking(true);
    api("/agent/tokens/revoke-all", { method: "POST" })
      .then(() => {
        setRevokeAll(false);
        void load();
      })
      .catch((e) => setFormError(errorMessage(e)))
      .finally(() => setRevoking(false));
  };

  const copySecret = () => {
    if (!revealed) return;
    navigator.clipboard
      .writeText(revealed.token)
      .then(() => setCopied(true))
      .catch(() => setCopied(false));
  };

  return (
    <Card>
      <div id="settings-agent">
        <CardTitle icon={KeyRound} title="Agent access" hint="Bearer tokens" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Named tokens let Claude Code and other agents call this server. The secret is shown once and only a hash is stored. The MCP server at <span className="font-bold text-ink">/mcp</span> is read-only. No token can open a shell.
        </p>

        {loadError && (
          <div className="mt-4">
            <Alert>{loadError}</Alert>
          </div>
        )}

        <div className="mt-5 grid gap-4 sm:grid-cols-2">
          <TextField
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Claude Code"
            autoComplete="off"
            spellCheck={false}
          />
          <SelectField label="Expires" value={days} onChange={(e) => setDays(e.target.value)}>
            {EXPIRY.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </SelectField>
        </div>

        <div className="mt-4 space-y-2">
          {SCOPES.map((s) => (
            <div key={s.key} className="flex items-start gap-3 rounded-2xl border border-line bg-surface-2 px-4 py-3">
              <Toggle checked={scopes[s.key]} onChange={(on) => setScopes((cur) => ({ ...cur, [s.key]: on }))} label={s.label} />
              <div className="min-w-0">
                <p className="text-sm font-bold text-ink">{s.label}</p>
                <p className="text-xs font-medium leading-relaxed text-ink-3">{s.hint}</p>
              </div>
            </div>
          ))}
        </div>

        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button variant="primary" loading={busy} onClick={create}>
            Create token
          </Button>
          {onOpenAudit && (
            <Button variant="ghost" onClick={onOpenAudit}>
              View audit log
            </Button>
          )}
        </div>
        {formError && (
          <div className="mt-3">
            <Alert>{formError}</Alert>
          </div>
        )}

        <div className="mt-6 border-t border-line pt-5">
          {tokens === null ? (
            <p className="text-sm font-medium text-ink-3">Loading tokens…</p>
          ) : tokens.length === 0 ? (
            <p className="text-sm font-medium text-ink-3">No agent tokens yet.</p>
          ) : (
            <ul className="space-y-2">
              {tokens.map((t) => {
                const dead = t.revoked || isExpired(t.expiresAt);
                return (
                  <li
                    key={t.id}
                    className={`flex flex-col gap-3 rounded-2xl border border-line bg-surface-2 px-4 py-3 sm:flex-row sm:items-center ${
                      dead ? "opacity-70" : ""
                    }`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-bold text-ink">{t.name}</span>
                        <span className="font-mono text-[11px] font-semibold text-ink-3">{t.prefix}…</span>
                        {t.revoked && <Pill tone="bad">Revoked</Pill>}
                        {!t.revoked && isExpired(t.expiresAt) && <Pill tone="warn">Expired</Pill>}
                      </div>
                      <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {t.scopes.map((s) => (
                          <span
                            key={s}
                            className="rounded-full bg-accent/12 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-accent"
                          >
                            {s}
                          </span>
                        ))}
                      </div>
                      <p className="mt-1.5 text-xs font-medium text-ink-3">
                        Created {when(t.createdAt, "—")} · Expires {t.expiresAt ? when(t.expiresAt, "—") : "never"} · Last used{" "}
                        {when(t.lastUsedAt, "not yet")}
                      </p>
                    </div>
                    {!t.revoked && (
                      <Button variant="ghost" size="sm" onClick={() => setRevoke(t)}>
                        <Trash2 size={14} />
                        Revoke
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}

          <div className="mt-4">
            <Button variant="danger" disabled={active.length === 0} onClick={() => setRevokeAll(true)}>
              Revoke all agent access
            </Button>
          </div>
        </div>
      </div>

      {revealed && (
        <Modal
          title="Copy this token"
          onClose={() => setRevealed(null)}
          footer={
            <>
              <Button variant="secondary" onClick={copySecret}>
                {copied ? <Check size={15} /> : <Copy size={15} />}
                {copied ? "Copied" : "Copy"}
              </Button>
              <Button variant="primary" onClick={() => setRevealed(null)}>
                Done
              </Button>
            </>
          }
        >
          <Alert tone="warn">This is the only time the token is shown. Store it somewhere safe. FELDCORE keeps the hash, not the token.</Alert>
          <input
            readOnly
            value={revealed.token}
            onFocus={(e) => e.currentTarget.select()}
            spellCheck={false}
            aria-label="Agent token"
            className="mt-4 w-full rounded-xl border border-line bg-surface-2 px-3.5 py-2.5 font-mono text-sm font-semibold text-ink"
          />
          <p className="mt-3 text-xs font-medium text-ink-3">
            {revealed.name} · {revealed.scopes.join(", ")}
            {revealed.expiresAt ? ` · expires ${when(revealed.expiresAt, "")}` : " · does not expire"}
          </p>
        </Modal>
      )}

      {revoke && (
        <Modal
          title={`Revoke ${revoke.name}?`}
          onClose={() => setRevoke(null)}
          footer={
            <>
              <Button variant="secondary" onClick={() => setRevoke(null)}>
                Cancel
              </Button>
              <Button variant="danger" loading={revoking} onClick={() => doRevoke(revoke.id)}>
                Revoke
              </Button>
            </>
          }
        >
          <p className="text-sm font-medium leading-relaxed text-ink-2">
            {revoke.name} stops working immediately. Clients that still have the secret will be refused.
          </p>
        </Modal>
      )}

      {revokeAll && (
        <Modal
          title="Revoke all agent access?"
          onClose={() => setRevokeAll(false)}
          footer={
            <>
              <Button variant="secondary" onClick={() => setRevokeAll(false)}>
                Cancel
              </Button>
              <Button variant="danger" loading={revoking} onClick={doRevokeAll}>
                Revoke all agent access
              </Button>
            </>
          }
        >
          <p className="text-sm font-medium leading-relaxed text-ink-2">
            Every agent token stops working. Claude Code and other clients will fail until you create a new token.
          </p>
        </Modal>
      )}
    </Card>
  );
}

function Pill({ tone, children }: { tone: "bad" | "warn"; children: string }) {
  const cls = tone === "bad" ? "bg-bad/12 text-bad" : "bg-warn/15 text-warn";
  return <span className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${cls}`}>{children}</span>;
}
