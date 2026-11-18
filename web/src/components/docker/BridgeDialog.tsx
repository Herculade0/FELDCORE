import { useEffect, useState } from "react";
import { Check, Copy, Eye, EyeOff } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, Modal, TextField } from "../form";
import type { Container } from "./types";

interface Endpoint {
  host: string;
  port: number;
  bind?: string;
  security: string;
  insecure: boolean;
  note?: string;
}

interface Mailbox {
  username: string;
  password: string;
}

interface BridgeStatus {
  purpose: string;
  image: string;
  installed: boolean;
  running: boolean;
  containerName?: string;
  configured: boolean;
  needsLogin: boolean;
  username?: string;
  password?: string;
  mailboxes: Mailbox[];
  imap: Endpoint;
  smtp: Endpoint;
  plan: string;
  intro: string;
  clients: string;
  login: { steps: string[]; commands: string[]; after: string[] };
  reauth: string[];
  notice?: string;
}

export function isProtonBridge(c: Container): boolean {
  if (c.name === "feldcore-proton-bridge") return false;
  if (c.app === "proton-bridge" || c.name === "proton-bridge") return true;
  return imageIsBridge(c.image);
}

function imageIsBridge(image: string): boolean {
  let ref = image.trim();
  const at = ref.lastIndexOf("@");
  if (at >= 0) ref = ref.slice(0, at);
  ref = ref.replace(/^(docker\.io|index\.docker\.io)\//, "");
  const slash = ref.lastIndexOf("/");
  const colon = ref.lastIndexOf(":");
  if (colon > slash) ref = ref.slice(0, colon);
  return ref === "shenxn/protonmail-bridge";
}

export default function BridgeDialog({
  container,
  onClose,
  onConsole,
}: {
  container: Container;
  onClose: () => void;
  onConsole: () => void;
}) {
  const [status, setStatus] = useState<BridgeStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");
  const [show, setShow] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [copied, setCopied] = useState("");

  const load = async () => {
    try {
      const st = await api<BridgeStatus>(`/integrations/proton-bridge?id=${encodeURIComponent(container.id)}`);
      setStatus(st);
      setUsername(st.username ?? "");
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  useEffect(() => {
    void load();
  }, [container.id]);

  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(label);
      window.setTimeout(() => setCopied(""), 1500);
    } catch {
      setError("The browser did not copy that value.");
    }
  };

  const save = async () => {
    setBusy("save");
    setError(null);
    try {
      const st = await api<BridgeStatus>(`/integrations/proton-bridge?id=${encodeURIComponent(container.id)}`, {
        method: "PUT",
        body: { username, password },
      });
      setStatus(st);
      setPassword("");
      setShow(false);
      setNotice(st.notice || "Bridge credentials are saved.");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const read = async () => {
    setBusy("read");
    setError(null);
    try {
      const st = await api<BridgeStatus>("/integrations/proton-bridge/read", {
        method: "POST",
        body: { id: container.id },
      });
      setStatus(st);
      setUsername(st.username ?? "");
      setPassword("");
      setNotice(st.notice || "Read the Bridge login.");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const running = container.state === "running";

  return (
    <Modal
      title="Mailbox access"
      size="lg"
      onClose={onClose}
      footer={
        <Button onClick={onClose}>Close</Button>
      }
    >
      <div className="space-y-4">
        <p className="text-sm font-semibold text-ink">
          {status?.purpose || "AI integration"}
          {status?.containerName ? ` · ${status.containerName}` : ` · ${container.name}`}
        </p>
        <p className="text-sm font-medium leading-relaxed text-ink-2">
          {status?.intro ||
            "Other FELDCORE apps and bots connect to IMAP and SMTP with the Bridge username and app password."}
        </p>
        {status?.plan && (
          <Alert tone="warn">{status.plan}</Alert>
        )}
        {error && <Alert>{error}</Alert>}
        {notice && <Alert tone="info">{notice}</Alert>}
        {status?.notice && status.notice !== notice && <Alert tone="info">{status.notice}</Alert>}

        <section className="space-y-2">
          <h3 className="text-sm font-extrabold text-ink">Endpoints</h3>
          <EndpointRow label="IMAP" ep={status?.imap} copied={copied} onCopy={copy} />
          <EndpointRow label="SMTP" ep={status?.smtp} copied={copied} onCopy={copy} />
          {status?.clients && <p className="text-xs font-medium leading-relaxed text-ink-3">{status.clients}</p>}
        </section>

        <section className="space-y-2">
          <h3 className="text-sm font-extrabold text-ink">Bridge credentials</h3>
          {status?.configured && status.username && status.password ? (
            <div className="space-y-2 rounded-2xl border border-line bg-surface-2 p-3">
              <SecretRow label="Username" value={status.username} copied={copied} onCopy={copy} />
              <SecretRow label="Password" value={status.password} secret={!show} copied={copied} onCopy={copy} />
              {(status.mailboxes ?? []).length > 1 && (
                <div className="space-y-2 border-t border-line pt-2">
                  {(status.mailboxes ?? []).map((box) => (
                    <SecretRow
                      key={box.username}
                      label={box.username}
                      value={box.password}
                      secret={!show}
                      copied={copied}
                      onCopy={copy}
                    />
                  ))}
                </div>
              )}
              <Button size="sm" onClick={() => setShow((v) => !v)}>
                {show ? <EyeOff size={14} /> : <Eye size={14} />}
                {show ? "Hide password" : "Show password"}
              </Button>
            </div>
          ) : (
            <p className="text-sm font-medium text-ink-2">
              No Bridge password is saved yet. Sign in from the console, then read it or paste it here.
            </p>
          )}
        </section>

        <section className="space-y-2">
          <h3 className="text-sm font-extrabold text-ink">First sign-in</h3>
          <p className="text-sm font-medium leading-relaxed text-ink-2">
            Proton asks for the account password and a 2FA code. That step stays in the container console, the same as docker exec. FELDCORE cannot submit it for you.
          </p>
          <ol className="list-decimal space-y-1.5 pl-5 text-sm font-medium leading-relaxed text-ink-2">
            {(status?.login.steps ?? []).map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
          <CommandBlock title="In the console" lines={status?.login.commands ?? []} />
          <CommandBlock title="Then, at the Bridge prompt" lines={status?.login.after ?? []} />
          <div className="flex flex-wrap gap-2">
            <Button onClick={onConsole} disabled={!running}>
              {running ? "Open console" : "Start the container to open the console"}
            </Button>
            <Button variant="primary" loading={busy === "read"} onClick={() => void read()}>
              Read from Bridge
            </Button>
          </div>
          <p className="text-xs font-medium leading-relaxed text-ink-3">
            Read from Bridge stops this container for a moment, runs info against the data folder, stores the Bridge password, and starts the container again. It does not log in.
          </p>
        </section>

        <section className="space-y-2">
          <h3 className="text-sm font-extrabold text-ink">Paste from info</h3>
          <TextField
            label="Bridge username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoCapitalize="none"
            spellCheck={false}
            className="[&_input]:font-mono [&_input]:!text-xs"
          />
          <TextField
            label="Bridge password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            hint="This is the password info prints, not the Proton account password."
            className="[&_input]:font-mono [&_input]:!text-xs"
          />
          <Button variant="primary" loading={busy === "save"} disabled={!username || !password} onClick={() => void save()}>
            Save credentials
          </Button>
        </section>

        <section className="space-y-2">
          <h3 className="text-sm font-extrabold text-ink">When Proton asks you to sign in again</h3>
          <ol className="list-decimal space-y-1.5 pl-5 text-sm font-medium leading-relaxed text-ink-2">
            {(status?.reauth ?? []).map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
        </section>
      </div>
    </Modal>
  );
}

function EndpointRow({
  label,
  ep,
  copied,
  onCopy,
}: {
  label: string;
  ep?: Endpoint;
  copied: string;
  onCopy: (label: string, value: string) => void;
}) {
  if (!ep || !ep.port) {
    return <p className="text-sm font-medium text-ink-2">{label}: {ep?.note || "Not published yet."}</p>;
  }
  const value = `${ep.host}:${ep.port}`;
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <span className="w-14 font-extrabold text-ink">{label}</span>
      <code className="rounded-md bg-surface-2 px-2 py-1 font-mono text-xs text-ink">{value}</code>
      <span className="text-xs font-semibold text-ink-3">{ep.security}{ep.insecure ? " · skip certificate check" : ""}</span>
      <CopyButton label={label} value={value} copied={copied} onCopy={onCopy} />
      {ep.note && <p className="w-full text-xs font-medium leading-relaxed text-ink-3">{ep.note}</p>}
    </div>
  );
}

function SecretRow({
  label,
  value,
  secret,
  copied,
  onCopy,
}: {
  label: string;
  value: string;
  secret?: boolean;
  copied: string;
  onCopy: (label: string, value: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <span className="w-24 font-bold text-ink-2">{label}</span>
      <code className="min-w-0 flex-1 truncate rounded-md bg-surface px-2 py-1 font-mono text-xs text-ink">
        {secret ? "••••••••••••" : value}
      </code>
      <CopyButton label={label} value={value} copied={copied} onCopy={onCopy} />
    </div>
  );
}

function CopyButton({
  label,
  value,
  copied,
  onCopy,
}: {
  label: string;
  value: string;
  copied: string;
  onCopy: (label: string, value: string) => void;
}) {
  const on = copied === label;
  return (
    <Button size="sm" onClick={() => void onCopy(label, value)}>
      {on ? <Check size={14} /> : <Copy size={14} />}
      {on ? "Copied" : "Copy"}
    </Button>
  );
}

function CommandBlock({ title, lines }: { title: string; lines: string[] }) {
  if (lines.length === 0) return null;
  return (
    <div>
      <p className="mb-1 text-xs font-bold uppercase tracking-wider text-ink-3">{title}</p>
      <pre className="overflow-x-auto rounded-2xl bg-ink px-3 py-2 font-mono text-xs leading-relaxed text-white">
        {lines.join("\n")}
      </pre>
    </div>
  );
}
