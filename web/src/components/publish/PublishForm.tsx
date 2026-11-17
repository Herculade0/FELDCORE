import { useEffect, useState } from "react";
import { api, errorMessage } from "../../lib/api";
import { asBrand, BrandFields, emptyBrand, type SignInBrand } from "../citizen/SignInBrand";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";

interface PublishFormData {
  container: string;
  domain: string;
  suggestion: { subdomain: string; port: number; from: string };
  ports: number[];
  enabled: boolean;
  subdomain: string;
  hostname: string;
  port: number;
  access: string;
  groups: string[];
  tls: string;
  brand?: SignInBrand;
  tunnelMode: string;
  from: string;
}

const LOCAL_SUFFIXES = [".home.arpa", ".local", ".lan", ".home", ".internal", ".localdomain"];

function localName(host: string): boolean {
  const name = host.trim().toLowerCase().replace(/\.$/, "");
  return LOCAL_SUFFIXES.some((suffix) => name.endsWith(suffix));
}

const ACCESS = [
  ["public", "public"],
  ["signed-in", "signed-in"],
  ["groups", "groups"],
  ["admins", "admins"],
] as const;

/**
 * The one Publish switch. Saving turns the HTTPS name, certificate and
 * optional Cloudflare Tunnel hostname on or off together.
 */
export default function PublishForm({
  containerId,
  containerName,
}: {
  containerId: string;
  containerName: string;
}) {
  const [data, setData] = useState<PublishFormData | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [sub, setSub] = useState("");
  const [port, setPort] = useState("");
  const [access, setAccess] = useState("admins");
  const [groups, setGroups] = useState("");
  const [tls, setTls] = useState("acme");
  const [brand, setBrand] = useState<SignInBrand>(emptyBrand);
  const [error, setError] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const apply = (d: PublishFormData) => {
    setData(d);
    setEnabled(d.enabled);
    setSub(d.subdomain || d.suggestion.subdomain);
    setPort(String(d.port || d.suggestion.port || ""));
    setAccess(d.access || "admins");
    setGroups((d.groups ?? []).join(", "));
    setTls(d.tls === "internal" ? "internal" : "acme");
    setBrand(asBrand(d.brand));
  };

  useEffect(() => {
    api<PublishFormData>(`/docker/containers/${encodeURIComponent(containerId)}/publish`)
      .then(apply)
      .catch((e) => setError(errorMessage(e)));
  }, [containerId]);

  const save = async () => {
    setBusy(true);
    setError(null);
    setWarning(null);
    try {
      const res = await api<{ warning?: string; publish: PublishFormData }>(
        `/docker/containers/${encodeURIComponent(containerId)}/publish`,
        {
          method: "PUT",
          body: {
            enabled,
            subdomain: sub,
            port: Number(port),
            access,
            groups: groups
              .split(",")
              .map((g) => g.trim())
              .filter(Boolean),
            tls: localName(`${(sub || "app").trim() || "app"}.${data?.domain || "home.arpa"}`) ? "internal" : tls,
            brand,
          },
        },
      );
      apply(res.publish);
      if (res.warning) setWarning(res.warning);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const domain = data?.domain || "home.arpa";
  const preview = `${(sub || "app").trim() || "app"}.${domain}`;

  return (
    <section
      id="publish-toggle"
      className="rounded-2xl border border-line p-4"
      onKeyDown={(e) => {
        if (e.key === "Enter") e.preventDefault();
      }}
    >
      <div className="flex items-start gap-3">
        <Toggle checked={enabled} onChange={setEnabled} label="Publish" disabled={!data || busy} />
        <div className="min-w-0">
          <p className="text-sm font-bold text-ink">Publish</p>
          <p className="text-xs font-medium text-ink-3">
            One switch publishes {preview}: the HTTPS name, the certificate, and the tunnel hostname when Remote access is on.
          </p>
        </div>
      </div>

      {!data && !error && <p className="mt-4 text-sm font-semibold text-ink-2">Reading {containerName}…</p>}
      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      {warning && (
        <div className="mt-4">
          <Alert tone="warn">{warning}</Alert>
        </div>
      )}

      {data && (
        <div className="mt-4 space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <TextField
              label="Subdomain"
              value={sub}
              onChange={(e) => setSub(e.target.value)}
              placeholder={data.suggestion.subdomain}
              disabled={!enabled}
              autoCapitalize="none"
              spellCheck={false}
              hint={data.from || `Becomes ${preview}`}
            />
            <TextField
              label="Target port"
              value={port}
              onChange={(e) => setPort(e.target.value)}
              inputMode="numeric"
              disabled={!enabled}
              hint="The port the app listens on inside the container."
            />
          </div>
          {data.ports.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {data.ports.map((p) => (
                <button
                  key={p}
                  type="button"
                  disabled={!enabled}
                  onClick={() => setPort(String(p))}
                  className="rounded-lg bg-surface-2 px-2 py-1 font-mono text-[11px] font-bold text-ink-2 disabled:opacity-50"
                >
                  {p}
                </button>
              ))}
            </div>
          )}
          <SelectField
            label="Citizen ID"
            value={access}
            onChange={(e) => setAccess(e.target.value)}
            disabled={!enabled}
            hint="public / signed-in / groups / admins. Citizen ID enforces this on the published name."
          >
            {ACCESS.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </SelectField>
          <p className="text-sm font-bold text-ink">Citizen ID: {access}</p>
          {access === "groups" && (
            <TextField
              label="Groups"
              value={groups}
              onChange={(e) => setGroups(e.target.value)}
              disabled={!enabled}
              placeholder="family, kids"
              hint="Citizen ID group names, separated by commas."
              autoCapitalize="none"
              spellCheck={false}
            />
          )}
          {!localName(preview) && (
            <SelectField
              label="Certificate"
              value={tls}
              onChange={(e) => setTls(e.target.value)}
              disabled={!enabled}
              hint="Let's Encrypt needs the Cloudflare API token and is for a name reached on its own. Tunnel only uses the local certificate: Cloudflare terminates public HTTPS, and this server does not ask Let's Encrypt."
            >
              <option value="acme">Let's Encrypt (directly public)</option>
              <option value="internal">Local certificate (tunnel only)</option>
            </SelectField>
          )}
          <p className="text-xs font-medium leading-relaxed text-ink-3">
            {data.tunnelMode === "off"
              ? "Remote access is off, so a public name is served from this server only. No ports are opened on your router."
              : "When the name is public and Remote access is on, the Cloudflare Tunnel hostname is created and removed with this switch."}{" "}
            Home-network names such as {data.suggestion.subdomain}.home.arpa use the local certificate and skip the tunnel. A public name can use Let's Encrypt, or the local certificate when it is only opened through the tunnel.
          </p>
          <BrandFields
            value={brand}
            onChange={setBrand}
            previewHost={preview}
            hint="Shown on this app's sign-in page. Blank fields use the Citizen ID defaults."
          />
          <div className="flex justify-end">
            <Button type="button" variant="primary" loading={busy} onClick={() => void save()}>
              {enabled ? "Publish" : "Save"}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
