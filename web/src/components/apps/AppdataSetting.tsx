import { useEffect, useState } from "react";
import { FolderTree } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, TextField } from "../form";
import type { AppSettings } from "./types";

/**
 * Where Unraid's /mnt/user/appdata paths are placed. A share named appdata is
 * used when nothing has been saved.
 */
export default function AppdataSetting() {
  const [settings, setSettings] = useState<AppSettings | null>(null);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api<AppSettings>("/apps/settings")
      .then((s) => {
        setSettings(s);
        setValue(s.appdata);
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const save = async (path: string) => {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const s = await api<AppSettings>("/apps/settings", { method: "PUT", body: { appdata: path } });
      setSettings(s);
      setValue(s.appdata);
      setSaved(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const dirty = settings !== null && value.trim() !== settings.appdata;

  return (
    <Card>
      <CardTitle icon={FolderTree} title="App data" hint="Used by the App Store" />
      <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
        Unraid templates store configuration under <span className="font-mono text-xs">/mnt/user/appdata</span>.
        FELDCORE puts those folders here
        {settings && !settings.appdataCustom ? ", using a share named appdata when you have one" : ""}.
      </p>
      <div className="mt-4 flex flex-col gap-3 sm:flex-row sm:items-end">
        <TextField
          label="App data folder"
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            setSaved(false);
          }}
          placeholder={settings?.appdataDefault ?? "/srv/feldcore/appdata"}
          autoCapitalize="none"
          spellCheck={false}
          className="min-w-0 flex-1 [&_input]:font-mono [&_input]:!text-xs"
        />
        <div className="flex gap-2">
          <Button variant="primary" disabled={!dirty || busy} loading={busy} onClick={() => void save(value.trim())}>
            Save
          </Button>
          {settings?.appdataCustom && (
            <Button disabled={busy} onClick={() => void save("")}>
              Use default
            </Button>
          )}
        </div>
      </div>
      {saved && <p className="mt-3 text-xs font-semibold text-ok">Saved.</p>}
      {error && (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      )}
    </Card>
  );
}
