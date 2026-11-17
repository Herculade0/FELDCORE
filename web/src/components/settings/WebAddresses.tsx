import { useEffect, useState } from "react";
import { Download, Globe, Lock } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";

interface Conflict {
  port: number;
  owner: string;
  explanation: string;
}

interface ProxyView {
  enabled: boolean;
  email: string;
  domain: string;
  uiHostname: string;
  uiEnabled: boolean;
  uiTls: string;
  status: string;
  summary: string;
  caReady: boolean;
  conflicts: Conflict[];
}

export default function WebAddresses() {
  const [view, setView] = useState<ProxyView | null>(null);
  const [email, setEmail] = useState("");
  const [domain, setDomain] = useState("home.arpa");
  const [host, setHost] = useState("feldcore.home.arpa");
  const [on, setOn] = useState(false);
  const [tls, setTls] = useState("internal");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  const apply = (v: ProxyView) => {
    setView(v);
    setEmail(v.email || "");
    setDomain(v.domain || "home.arpa");
    setHost(v.uiHostname || `feldcore.${v.domain || "home.arpa"}`);
    setOn(v.uiEnabled);
    setTls(v.uiTls || "internal");
  };

  useEffect(() => {
    api<ProxyView>("/proxy")
      .then(apply)
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const save = async () => {
    setBusy(true);
    setSaved(false);
    setError(null);
    try {
      apply(
        await api<ProxyView>("/proxy", {
          method: "PUT",
          body: { email, domain, uiHostname: host, uiEnabled: on, uiTls: tls },
        }),
      );
      setSaved(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div id="settings-web">
    <Card>
      <CardTitle icon={Globe} title="Web addresses" hint={view?.status === "up" ? "Caddy is running" : "Nothing published"} />
      <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
        Give this server, and each container, a name such as <span className="font-mono text-xs">valheim.home.arpa</span> or{" "}
        <span className="font-mono text-xs">app.example.com</span>. Caddy runs as a container FELDCORE manages and renews certificates on its own. On a home name it uses a local certificate authority. On a domain Cloudflare hosts, it asks Let's Encrypt with the DNS-01 challenge, using the API token from Remote access, so port 80 does not have to be open to the world.
      </p>

      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      {view?.conflicts?.map((c) => (
        <div key={c.port} className="mt-4">
          <Alert tone="warn">{c.explanation}</Alert>
        </div>
      ))}

      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <TextField
          label="Home domain"
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
          placeholder="home.arpa"
          hint="New containers are suggested as name.this-domain. home.arpa is a name that stays on your network."
        />
        <TextField
          label="Certificate email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@example.com"
          hint="Let's Encrypt sends expiry notices here. Home names do not need it."
        />
      </div>

      <div className="mt-6 flex items-center gap-3">
        <Toggle checked={on} onChange={setOn} label="Serve FELDCORE on a hostname" />
        <span className="text-sm font-bold text-ink">Serve this page on a hostname</span>
      </div>
      {on && (
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
          <TextField label="Hostname" value={host} onChange={(e) => setHost(e.target.value)} placeholder="feldcore.home.arpa" />
          <SelectField label="Certificate" value={tls} onChange={(e) => setTls(e.target.value)}>
            <option value="internal">Local certificate (recommended at home)</option>
            <option value="off">Plain HTTP</option>
            <option value="acme">Let's Encrypt (public domain)</option>
          </SelectField>
        </div>
      )}

      <div className="mt-4 flex flex-wrap items-center gap-3">
        <Button variant="primary" loading={busy} onClick={() => void save()}>
          Save web addresses
        </Button>
        {saved && <p className="text-xs font-semibold text-ok">Saved.</p>}
      </div>
      {view?.summary && <p className="mt-3 text-xs font-medium text-ink-3">{view.summary}</p>}

      <div className="mt-6 rounded-2xl border border-line bg-surface-2 p-4">
        <div className="flex items-start gap-3">
          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-accent/12 text-accent">
            <Lock size={16} />
          </span>
          <div className="min-w-0">
            <p className="text-sm font-bold text-ink">Trust the local certificate</p>
            <p className="mt-1 text-xs font-medium leading-relaxed text-ink-3">
              Phones and laptops warn about the local certificate until they trust FELDCORE's root. Download it once Caddy has created it, then install it as a trusted root.
            </p>
            <a
              href="/api/v1/proxy/ca"
              className={`mt-3 inline-flex items-center gap-2 rounded-xl border border-line bg-surface px-3 py-2 text-xs font-bold text-ink ${view?.caReady ? "" : "pointer-events-none opacity-50"}`}
            >
              <Download size={14} />
              Download root certificate
            </a>
            {!view?.caReady && <p className="mt-2 text-xs font-medium text-ink-3">Turn on a home-network name first. The file appears after Caddy starts.</p>}
            <ol className="mt-3 list-decimal space-y-1 pl-4 text-xs font-medium leading-relaxed text-ink-2">
              <li>Windows: double-click the file, choose Local machine, and place it in Trusted Root Certification Authorities.</li>
              <li>Mac: double-click, add it to the System keychain, then set Trust to Always Trust.</li>
              <li>iPhone: AirDrop or mail the file, install the profile, then enable it under Settings → General → About → Certificate Trust Settings.</li>
              <li>Android: Settings → Security → Install a certificate → CA certificate.</li>
              <li>Linux: copy it to /usr/local/share/ca-certificates/feldcore.crt and run update-ca-certificates.</li>
            </ol>
          </div>
        </div>
      </div>
    </Card>
    </div>
  );
}
