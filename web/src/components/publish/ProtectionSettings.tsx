import { useEffect, useState } from "react";
import { Shield } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";
import PublishedApps from "./PublishedApps";

interface Protect {
  configured: boolean;
  ratePerSec: number;
  rateBurst: number;
  countryMode: string;
  countries: string[];
  geoEnabled: boolean;
  geoReady: boolean;
  geoError?: string;
  licenseSet: boolean;
  blockBots: boolean;
  blockScanners: boolean;
}

interface Overview {
  baseDomain: string;
  domain: string;
  webDomain: string;
  protect: Protect;
}

export default function PublishingSettings() {
  return (
    <>
      <PublishedApps />
      <ProtectionCard />
    </>
  );
}

function ProtectionCard() {
  const [data, setData] = useState<Overview | null>(null);
  const [baseDomain, setBaseDomain] = useState("");
  const [rate, setRate] = useState("10");
  const [burst, setBurst] = useState("30");
  const [mode, setMode] = useState("off");
  const [countries, setCountries] = useState("");
  const [geo, setGeo] = useState(false);
  const [bots, setBots] = useState(true);
  const [scanners, setScanners] = useState(false);
  const [license, setLicense] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const fill = (d: Overview) => {
    setData(d);
    setBaseDomain(d.baseDomain ?? "");
    setRate(String(d.protect.ratePerSec));
    setBurst(String(d.protect.rateBurst));
    setMode(d.protect.countryMode || "off");
    setCountries((d.protect.countries ?? []).join(", "));
    setGeo(d.protect.geoEnabled);
    setBots(d.protect.blockBots);
    setScanners(d.protect.blockScanners);
    setLicense("");
    setClearKey(false);
  };

  const load = () =>
    api<Overview>("/publish")
      .then((d) => {
        fill(d);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  const save = async () => {
    setBusy(true);
    setError(null);
    setSaved(null);
    try {
      const next = await api<Overview>("/publish", {
        method: "PUT",
        body: {
          baseDomain: baseDomain.trim(),
          ratePerSec: Number(rate) || 0,
          rateBurst: Number(burst) || 0,
          countryMode: mode,
          countries: countries
            .split(/[\s,;]+/)
            .map((c) => c.trim())
            .filter(Boolean),
          geoEnabled: geo,
          licenseKey: license,
          clearLicense: clearKey,
          blockBots: bots,
          blockScanners: scanners,
        },
      });
      fill(next);
      setSaved(geo ? "Saved. Country checks use the local database." : "Protection settings saved.");
    } catch (e) {
      setError(errorMessage(e));
      void load();
    } finally {
      setBusy(false);
    }
  };

  const geoLine = !data
    ? ""
    : !geo
      ? "Off. The database is not downloaded."
      : data.protect.geoReady
        ? "Country database is loaded on this server."
        : data.protect.geoError || "Not downloaded yet. Save to fetch GeoLite2-Country.";

  return (
    <div id="protection-settings">
      <Card>
        <CardTitle icon={Shield} title="Protection" hint="In front of every published app" />
        <p className="mt-4 text-sm font-medium leading-relaxed text-ink-2">
          Each published request is checked here, then handed to Citizen ID, then proxied to the app. Country lists
          stay off until you turn them on. The licence key is stored with the other secrets and is not written to the log.
        </p>

        {error && (
          <div className="mt-4">
            <Alert>{error}</Alert>
          </div>
        )}
        {saved && (
          <div className="mt-4">
            <Alert tone="info">{saved}</Alert>
          </div>
        )}

        {!data && !error && <p className="mt-5 text-sm font-medium text-ink-3">Loading…</p>}

        {data && (
          <div className="mt-5 space-y-5">
            <TextField
              label="Base domain"
              value={baseDomain}
              onChange={(e) => setBaseDomain(e.target.value)}
              placeholder={data.webDomain || "home.arpa"}
              autoCapitalize="none"
              spellCheck={false}
              hint={`New names look like plex.${data.domain}. Leave this blank to use the web-address domain (${data.webDomain || "home.arpa"}).`}
            />

            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                label="Requests per second"
                value={rate}
                onChange={(e) => setRate(e.target.value)}
                inputMode="decimal"
                hint="Per client IP, per app."
              />
              <TextField
                label="Burst"
                value={burst}
                onChange={(e) => setBurst(e.target.value)}
                inputMode="numeric"
                hint="How many requests can arrive at once before the limit applies."
              />
            </div>

            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-bold text-ink">Block known bots and empty user agents</div>
                <p className="text-xs font-medium text-ink-3">Scanners such as sqlmap, nikto and nmap, and requests with no user agent.</p>
              </div>
              <Toggle checked={bots} onChange={setBots} label="Block known bots and empty user agents" />
            </div>

            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-bold text-ink">Block common scanner paths</div>
                <p className="text-xs font-medium text-ink-3">Optional. Refuses /wp-admin, /.env, /.git and similar probes.</p>
              </div>
              <Toggle checked={scanners} onChange={setScanners} label="Block common scanner paths" />
            </div>

            <SelectField
              label="Country list"
              value={mode}
              onChange={(e) => setMode(e.target.value)}
              hint="Does nothing until the GeoLite2 database is on. Private addresses have no country, so an allow list blocks them."
            >
              <option value="off">Off</option>
              <option value="allow">Allow only these countries</option>
              <option value="block">Block these countries</option>
            </SelectField>
            <TextField
              label="Countries"
              value={countries}
              onChange={(e) => setCountries(e.target.value)}
              placeholder="US, CA, GB"
              autoCapitalize="characters"
              spellCheck={false}
              disabled={mode === "off"}
              hint="Two-letter codes, separated by commas."
            />

            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-bold text-ink">Download the country database</div>
                <p className="text-xs font-medium text-ink-3">{geoLine}</p>
              </div>
              <Toggle checked={geo} onChange={setGeo} label="Download the country database" />
            </div>
            <TextField
              label="MaxMind licence key"
              type="password"
              value={license}
              onChange={(e) => setLicense(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              placeholder={data.protect.licenseSet ? "Saved — leave blank to keep it" : "Paste a GeoLite2 licence key"}
              hint="Used only to download GeoLite2-Country. Off by default, and nothing is fetched until you enable it."
            />
            {data.protect.licenseSet && (
              <div className="flex items-center justify-between gap-4">
                <div className="text-sm font-bold text-ink">Remove the saved licence key</div>
                <Toggle checked={clearKey} onChange={setClearKey} label="Remove the saved licence key" />
              </div>
            )}

            {!data.protect.configured && (
              <p className="text-xs font-medium text-ink-3">
                These are the built-in defaults. They apply already; saving makes them yours to edit.
              </p>
            )}

            <div className="flex justify-end">
              <Button type="button" variant="primary" loading={busy} onClick={() => void save()}>
                Save protection
              </Button>
            </div>
          </div>
        )}
      </Card>
    </div>
  );
}
