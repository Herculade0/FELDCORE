import { useEffect, useState } from "react";
import { HardDrive } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, TextField, Toggle } from "../form";

interface Backup {
  enabled: boolean;
  configured: boolean;
  remotePath: string;
  schedule: string;
  retention: number;
  includeSnapshots: boolean;
  includeAppdata: boolean;
  username: string;
  hasPassword: boolean;
  hasMailbox: boolean;
  hasTwoFA: boolean;
  lastOk: boolean;
  lastError?: string;
  lastName?: string;
  experimental: boolean;
  note: string;
}

export default function DriveBackup() {
  const [data, setData] = useState<Backup | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [mailbox, setMailbox] = useState("");
  const [twoFa, setTwoFa] = useState("");
  const [remote, setRemote] = useState("feldcore");
  const [schedule, setSchedule] = useState("");
  const [retention, setRetention] = useState(7);
  const [snaps, setSnaps] = useState(true);
  const [appdata, setAppdata] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");

  const apply = (b: Backup) => {
    setData(b);
    setUsername(b.username || "");
    setRemote(b.remotePath || "feldcore");
    setSchedule(b.schedule || "");
    setRetention(b.retention || 7);
    setSnaps(b.configured ? b.includeSnapshots : true);
    setAppdata(b.includeAppdata);
    setEnabled(b.enabled);
    setPassword("");
    setMailbox("");
    setTwoFa("");
  };

  useEffect(() => {
    api<Backup>("/backups")
      .then(apply)
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const save = async () => {
    const body = {
      enabled,
      username,
      password,
      mailbox,
      twoFa,
      remotePath: remote,
      schedule,
      retention: Number(retention) || 0,
      includeSnapshots: snaps,
      includeAppdata: appdata,
    };
    const next = await api<Backup>("/backups", { method: "PUT", body });
    apply(next);
  };

  return (
    <Card>
      <div id="settings-drive">
        <CardTitle icon={HardDrive} title="Proton Drive backups" hint="Off until you turn them on" />
        <p className="mt-3 text-sm text-ink-2">
          Versioned backups, including a Proton Drive destination, are on the Backups page. This card still uploads one tar.gz archive.
        </p>
        <div className="mt-3">
          <Alert tone="warn">
            Experimental. rclone's Proton Drive backend is unofficial and is not supported by Proton. A 2FA code
            is only good for the next sign-in; an OTP secret keeps a schedule working.
          </Alert>
        </div>
        {error && (
          <div className="mt-3">
            <Alert>{error}</Alert>
          </div>
        )}
        {notice && (
          <div className="mt-3">
            <Alert tone="info">{notice}</Alert>
          </div>
        )}
        {data && (
          <>
            <label className="mt-4 flex items-center justify-between gap-4 text-sm font-semibold text-ink">
              Back up to Proton Drive
              <Toggle checked={enabled} onChange={setEnabled} label="Proton Drive backups" />
            </label>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <TextField label="Proton email" value={username} onChange={(e) => setUsername(e.target.value)} autoCapitalize="none" spellCheck={false} />
              <TextField
                label="Password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={data.hasPassword ? "Stored, encrypted" : ""}
                autoComplete="new-password"
              />
              <TextField
                label="Mailbox password"
                type="password"
                value={mailbox}
                onChange={(e) => setMailbox(e.target.value)}
                hint="Only for an account that has a separate mailbox password."
                placeholder={data.hasMailbox ? "Stored, encrypted" : "Optional"}
                autoComplete="new-password"
              />
              <TextField
                label="2FA code or OTP secret"
                type="password"
                value={twoFa}
                onChange={(e) => setTwoFa(e.target.value)}
                placeholder={data.hasTwoFA ? "Stored, encrypted" : "Optional"}
                autoComplete="off"
              />
              <TextField label="Folder on Proton Drive" value={remote} onChange={(e) => setRemote(e.target.value)} />
              <TextField
                label="Daily schedule"
                value={schedule}
                onChange={(e) => setSchedule(e.target.value)}
                placeholder="03:00"
                hint="Leave empty to back up only when you ask. 24-hour local time."
              />
              <TextField
                label="Copies to keep"
                type="number"
                min={0}
                max={100}
                value={retention}
                onChange={(e) => setRetention(Number(e.target.value))}
                hint="Zero keeps every backup. Older FELDCORE archives in that folder are removed."
              />
            </div>
            <div className="mt-4 space-y-3">
              <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
                Include snapshot archives when that folder exists
                <Toggle checked={snaps} onChange={setSnaps} label="Include snapshot archives" />
              </label>
              <label className="flex items-center justify-between gap-4 text-sm font-semibold text-ink">
                Include appdata
                <Toggle checked={appdata} onChange={setAppdata} label="Include appdata" />
              </label>
            </div>
            <p className="mt-3 text-xs font-medium text-ink-3">
              Each backup contains FELDCORE's state and its encrypted secrets, including the key needed to read them.
              Proton Drive encrypts the upload again. The password is never written to the log.
            </p>
            {data.lastError && <p className="mt-2 text-xs font-semibold text-bad">{data.lastError}</p>}
            {data.lastName && data.lastOk && (
              <p className="mt-2 text-xs font-semibold text-ok">Last backup: {data.lastName}</p>
            )}
            <div className="mt-4 flex flex-wrap gap-2">
              <Button
                variant="primary"
                loading={busy === "save"}
                onClick={() => {
                  setBusy("save");
                  setError(null);
                  save()
                    .then(() => setNotice("Saved."))
                    .catch((e) => setError(errorMessage(e)))
                    .finally(() => setBusy(""));
                }}
              >
                Save
              </Button>
              <Button
                loading={busy === "test"}
                onClick={() => {
                  setBusy("test");
                  setError(null);
                  save()
                    .then(() => api<{ message: string }>("/backups/test", { method: "POST" }))
                    .then((r) => setNotice(r.message))
                    .catch((e) => setError(errorMessage(e)))
                    .finally(() => setBusy(""));
                }}
              >
                Test connection
              </Button>
              <Button
                loading={busy === "run"}
                onClick={() => {
                  setBusy("run");
                  setError(null);
                  save()
                    .then(() => api<{ message: string }>("/backups/run", { method: "POST" }))
                    .then((r) => {
                      setNotice(r.message);
                      return api<Backup>("/backups").then(apply);
                    })
                    .catch((e) => setError(errorMessage(e)))
                    .finally(() => setBusy(""));
                }}
              >
                Back up now
              </Button>
            </div>
          </>
        )}
      </div>
    </Card>
  );
}
