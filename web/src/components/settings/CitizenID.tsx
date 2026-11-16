import { useEffect, useState, type FormEvent } from "react";
import { Fingerprint, KeyRound, Shield, Users } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { passkeysAvailable, registerPasskey } from "../../lib/webauthn";
import { asBrand, BrandFields, type SignInBrand } from "../citizen/SignInBrand";
import { Card, CardTitle } from "../ui";
import { Alert, Button, SelectField, TextField } from "../form";

interface UserInfo {
  id: string;
  username: string;
  displayName?: string;
  disabled: boolean;
  groupIds: string[];
  totp: boolean;
  passkeys: number;
  admin?: boolean;
}

interface GroupInfo {
  id: string;
  name: string;
  description?: string;
}

interface AppInfo {
  container: string;
  host: string;
  url: string;
  level: string;
  groupIds: string[];
}

interface Directory {
  admin: UserInfo;
  users: UserInfo[];
  groups: GroupInfo[];
  apps: AppInfo[];
  domain: string;
  brand?: SignInBrand;
}

interface Profile {
  username: string;
  displayName?: string;
  admin: boolean;
  totp: boolean;
  passkeys: { id: string; name: string; createdAt: string }[];
}

interface TOTPSetup {
  secret: string;
  url: string;
  qr?: string;
}

const LEVELS: { value: string; label: string }[] = [
  { value: "public", label: "Anyone" },
  { value: "users", label: "Signed-in users" },
  { value: "groups", label: "Specific groups" },
  { value: "admin", label: "Administrators only" },
];

/** The signed-in person's authenticator and passkeys. */
export function OwnSignIn({ allowReset = false }: { allowReset?: boolean }) {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState("");
  const [proof, setProof] = useState("");
  const [proofCode, setProofCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () =>
    api<Profile>("/citizen/me")
      .then((p) => {
        setProfile(p);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  const begin = async () => {
    setBusy(true);
    setError(null);
    try {
      setSetup(
        await api<TOTPSetup>("/citizen/totp/begin", {
          method: "POST",
          body: profile?.totp ? { password: proof, totp: proofCode } : {},
        }),
      );
      setCode("");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const confirm = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/citizen/totp/confirm", { method: "POST", body: { code } });
      setSetup(null);
      setCode("");
      await load();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/citizen/users/admin/totp/reset", { method: "POST", body: { password: proof, totp: proofCode } });
      setSetup(null);
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const addPasskey = async () => {
    setBusy(true);
    setError(null);
    try {
      await registerPasskey();
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const removePasskey = async (id: string) => {
    setBusy(true);
    setError(null);
    try {
      await api(`/citizen/passkeys/${id}`, { method: "DELETE", body: { password: proof, totp: proofCode } });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardTitle icon={KeyRound} title="Your sign-in" hint={profile?.username} />
      <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
        An authenticator adds a 6-digit code to every sign-in, including the administrator
        password. Replacing or removing a sign-in method asks for your password and the current code.
      </p>
      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      {profile?.totp && !setup && (
        <p className="mt-4 text-sm font-semibold text-ink">An authenticator is enrolled.</p>
      )}
      {(profile?.totp || (profile?.passkeys.length ?? 0) > 0) && (
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <TextField
            label="Your password"
            type="password"
            value={proof}
            onChange={(e) => setProof(e.target.value)}
            autoComplete="current-password"
          />
          {profile?.totp && (
            <TextField
              label="Current authenticator code"
              value={proofCode}
              onChange={(e) => setProofCode(e.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
            />
          )}
        </div>
      )}
      <div className="mt-4 flex flex-wrap gap-2">
        <Button size="sm" onClick={() => void begin()} loading={busy && !setup}>
          {profile?.totp ? "Replace authenticator" : "Set up authenticator"}
        </Button>
        {allowReset && profile?.totp && (
          <Button size="sm" variant="danger" onClick={() => void reset()} disabled={busy}>
            Reset authenticator
          </Button>
        )}
      </div>
      {setup && (
        <form onSubmit={confirm} className="mt-5 space-y-4">
          <p className="text-xs font-medium leading-relaxed text-ink-3">
            Scan the code, or type the secret. The current authenticator keeps working until this one is confirmed.
          </p>
          {setup.qr && (
            <img src={setup.qr} alt="Authenticator QR code" className="h-40 w-40 rounded-2xl bg-white p-2" />
          )}
          <p className="font-mono text-sm font-bold tracking-wide text-ink [overflow-wrap:anywhere]">{setup.secret}</p>
          <TextField
            label="Code from the app"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            inputMode="numeric"
            autoComplete="one-time-code"
            required
          />
          <Button type="submit" variant="primary" size="sm" loading={busy}>
            Confirm
          </Button>
        </form>
      )}

      <div className="mt-6 border-t border-line pt-5">
        <h3 className="text-sm font-bold text-ink">Passkeys</h3>
        {passkeysAvailable() ? (
          <p className="mt-2 text-xs font-medium leading-relaxed text-ink-3">
            A passkey signs you in without the password. It needs this page on localhost or HTTPS.
          </p>
        ) : (
          <p className="mt-2 text-xs font-medium leading-relaxed text-ink-3">
            This browser does not offer passkeys.
          </p>
        )}
        <ul className="mt-3 divide-y divide-line">
          {(profile?.passkeys ?? []).map((p) => (
            <li key={p.id} className="flex items-center justify-between gap-3 py-2.5">
              <span className="min-w-0 truncate text-sm font-semibold text-ink">{p.name}</span>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => void removePasskey(p.id)}>
                Remove
              </Button>
            </li>
          ))}
        </ul>
        {passkeysAvailable() && (
          <Button size="sm" className="mt-3" disabled={busy} onClick={() => void addPasskey()}>
            <Fingerprint size={14} />
            Add a passkey
          </Button>
        )}
      </div>
    </Card>
  );
}

export default function CitizenID() {
  const [dir, setDir] = useState<Directory | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = () =>
    api<Directory>("/citizen")
      .then((d) => {
        setDir(d);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  return (
    <div className="space-y-6">
      <Card>
        <CardTitle icon={Shield} title="Citizen ID" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Users, groups, and who may open each published app. Once an authenticator is enrolled,
          the administrator sign-in asks for the code as well.
        </p>
        <p className="mt-2 text-xs font-medium leading-relaxed text-ink-3">
          {dir?.domain
            ? `One sign-in on ${dir.domain} covers the apps published on that domain.`
            : "Set a parent domain under Network so one sign-in covers every published app. Until then, the session cookie stays on this host."}
        </p>
      </Card>

      {error && <Alert>{error}</Alert>}
      <Defaults brand={asBrand(dir?.brand)} onChanged={load} />
      <OwnSignIn allowReset />

      {dir && (
        <>
          <Groups groups={dir.groups} onChanged={load} />
          <People users={dir.users} groups={dir.groups} callerTotp={dir.admin.totp} onChanged={load} />
          <Apps apps={dir.apps} groups={dir.groups} onChanged={load} />
        </>
      )}
    </div>
  );
}

function Groups({ groups, onChanged }: { groups: GroupInfo[]; onChanged: () => void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/citizen/groups", { method: "POST", body: { name, description } });
      setName("");
      setDescription("");
      onChanged();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (id: string) => {
    setError(null);
    try {
      await api(`/citizen/groups/${id}`, { method: "DELETE" });
      onChanged();
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <Card>
      <CardTitle icon={Users} title="Groups" hint={groups.length ? `${groups.length}` : undefined} />
      <form onSubmit={create} className="mt-5 grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
        <TextField label="Name" value={name} onChange={(e) => setName(e.target.value)} required autoComplete="off" />
        <TextField label="Description" value={description} onChange={(e) => setDescription(e.target.value)} autoComplete="off" />
        <Button type="submit" variant="primary" loading={busy}>
          Add group
        </Button>
      </form>
      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      {groups.length === 0 ? (
        <p className="mt-4 text-sm font-medium text-ink-3">No groups yet. A group is who may open an app.</p>
      ) : (
        <ul className="mt-4 divide-y divide-line">
          {groups.map((g) => (
            <li key={g.id} className="flex items-center justify-between gap-3 py-3">
              <div className="min-w-0">
                <div className="truncate text-sm font-bold text-ink">{g.name}</div>
                {g.description && <div className="truncate text-xs font-medium text-ink-3">{g.description}</div>}
              </div>
              <Button size="sm" variant="ghost" onClick={() => void remove(g.id)}>
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function Defaults({ brand, onChanged }: { brand: SignInBrand; onChanged: () => void }) {
  const stamp = JSON.stringify(brand);
  const [value, setValue] = useState(brand);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setValue(asBrand(JSON.parse(stamp) as SignInBrand));
  }, [stamp]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/citizen/defaults", { method: "PUT", body: value });
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardTitle icon={Shield} title="Sign-in page" hint="Defaults for every app" />
      <div className="mt-4">
        <BrandFields
          value={value}
          onChange={setValue}
          hint="This is the landing page, and the fallback when a published app leaves a field blank."
        />
      </div>
      {error && (
        <div className="mt-4">
          <Alert>{error}</Alert>
        </div>
      )}
      <div className="mt-4 flex justify-end">
        <Button variant="primary" size="sm" loading={busy} onClick={() => void save()}>
          Save defaults
        </Button>
      </div>
    </Card>
  );
}

function People({
  users,
  groups,
  callerTotp,
  onChanged,
}: {
  users: UserInfo[];
  groups: GroupInfo[];
  callerTotp: boolean;
  onChanged: () => void;
}) {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [groupIds, setGroupIds] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/citizen/users", {
        method: "POST",
        body: { username, displayName, password, groupIds },
      });
      setUsername("");
      setDisplayName("");
      setPassword("");
      setGroupIds([]);
      onChanged();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardTitle icon={Users} title="Users" hint="Other than the administrator" />
      <form onSubmit={create} className="mt-5 space-y-4">
        <div className="grid gap-4 sm:grid-cols-3">
          <TextField label="Username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" required />
          <TextField label="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} autoComplete="off" />
          <TextField
            label="Password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            hint="At least 8 characters."
            minLength={8}
            required
          />
        </div>
        <GroupChecks groups={groups} selected={groupIds} onChange={setGroupIds} />
        {error && <Alert>{error}</Alert>}
        <Button type="submit" variant="primary" loading={busy}>
          Add user
        </Button>
      </form>
      {users.length === 0 ? (
        <p className="mt-5 text-sm font-medium text-ink-3">No one else has a Citizen ID yet.</p>
      ) : (
        <ul className="mt-5 space-y-3">
          {users.map((u) => (
            <UserRow key={u.id} user={u} groups={groups} callerTotp={callerTotp} onChanged={onChanged} />
          ))}
        </ul>
      )}
    </Card>
  );
}

function GroupChecks({
  groups,
  selected,
  onChange,
}: {
  groups: GroupInfo[];
  selected: string[];
  onChange: (ids: string[]) => void;
}) {
  if (groups.length === 0) return null;
  const toggle = (id: string) =>
    onChange(selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]);
  return (
    <fieldset>
      <legend className="mb-2 text-xs font-bold text-ink-2">Groups</legend>
      <div className="flex flex-wrap gap-3">
        {groups.map((g) => (
          <label key={g.id} className="flex items-center gap-2 text-sm font-semibold text-ink">
            <input type="checkbox" checked={selected.includes(g.id)} onChange={() => toggle(g.id)} />
            {g.name}
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function UserRow({
  user,
  groups,
  callerTotp,
  onChanged,
}: {
  user: UserInfo;
  groups: GroupInfo[];
  callerTotp: boolean;
  onChanged: () => void;
}) {
  const [groupIds, setGroupIds] = useState(user.groupIds);
  const [password, setPassword] = useState("");
  const [proof, setProof] = useState("");
  const [proofCode, setProofCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);

  useEffect(() => {
    setGroupIds(user.groupIds);
  }, [user.groupIds]);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="rounded-2xl border border-line p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-bold text-ink">{user.displayName || user.username}</div>
          <div className="text-xs font-medium text-ink-3">
            {user.username}
            {user.disabled ? " · disabled" : ""}
            {user.totp ? " · authenticator" : ""}
            {user.passkeys ? ` · ${user.passkeys} passkey${user.passkeys === 1 ? "" : "s"}` : ""}
          </div>
        </div>
        <Button
          size="sm"
          disabled={busy}
          onClick={() => void run(() => api(`/citizen/users/${user.id}`, { method: "PUT", body: { disabled: !user.disabled } }))}
        >
          {user.disabled ? "Enable" : "Disable"}
        </Button>
      </div>
      <div className="mt-4">
        <GroupChecks groups={groups} selected={groupIds} onChange={setGroupIds} />
        {groups.length > 0 && (
          <Button
            size="sm"
            className="mt-3"
            disabled={busy}
            onClick={() => void run(() => api(`/citizen/users/${user.id}`, { method: "PUT", body: { groupIds } }))}
          >
            Save groups
          </Button>
        )}
      </div>
      <div className="mt-4 flex flex-wrap items-end gap-3">
        <TextField
          className="w-56"
          label="New password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          minLength={8}
        />
        <Button
          size="sm"
          disabled={busy || password.length < 8}
          onClick={() =>
            void run(async () => {
              await api(`/citizen/users/${user.id}/password`, { method: "POST", body: { password } });
              setPassword("");
            })
          }
        >
          Reset password
        </Button>
        {user.totp && (
          <>
            <TextField
              className="w-48"
              label="Your password"
              type="password"
              value={proof}
              onChange={(e) => setProof(e.target.value)}
              autoComplete="current-password"
            />
            {callerTotp && (
              <TextField
                className="w-44"
                label="Your code"
                value={proofCode}
                onChange={(e) => setProofCode(e.target.value)}
                inputMode="numeric"
                autoComplete="one-time-code"
              />
            )}
            <Button
              size="sm"
              disabled={busy}
              onClick={() =>
                void run(() =>
                  api(`/citizen/users/${user.id}/totp/reset`, {
                    method: "POST",
                    body: { password: proof, totp: proofCode },
                  }),
                )
              }
            >
              Reset authenticator
            </Button>
          </>
        )}
        {confirmRemove ? (
          <Button size="sm" variant="danger" disabled={busy} onClick={() => void run(() => api(`/citizen/users/${user.id}`, { method: "DELETE" }))}>
            Confirm remove
          </Button>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => setConfirmRemove(true)}>
            Remove
          </Button>
        )}
      </div>
      {error && (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      )}
    </li>
  );
}

function Apps({ apps, groups, onChanged }: { apps: AppInfo[]; groups: GroupInfo[]; onChanged: () => void }) {
  return (
    <Card>
      <CardTitle icon={Shield} title="Published apps" hint="Who can open them" />
      {apps.length === 0 ? (
        <p className="mt-4 text-sm font-medium leading-relaxed text-ink-3">
          No hostname is published yet. Add one on a container, then choose who can open it. Until you do, a published app stays open to anyone who can reach it.
        </p>
      ) : (
        <ul className="mt-4 space-y-4">
          {apps.map((app) => (
            <AppRow key={app.host} app={app} groups={groups} onChanged={onChanged} />
          ))}
        </ul>
      )}
    </Card>
  );
}

function AppRow({ app, groups, onChanged }: { app: AppInfo; groups: GroupInfo[]; onChanged: () => void }) {
  const [level, setLevel] = useState(app.level || "public");
  const [groupIds, setGroupIds] = useState(app.groupIds);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setLevel(app.level || "public");
    setGroupIds(app.groupIds);
  }, [app.level, app.groupIds]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await api(`/citizen/apps/${encodeURIComponent(app.container)}`, {
        method: "PUT",
        body: { level, groupIds: level === "groups" ? groupIds : [] },
      });
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="rounded-2xl border border-line p-4">
      <div className="min-w-0">
        <div className="truncate text-sm font-bold text-ink">{app.host}</div>
        <div className="truncate text-xs font-medium text-ink-3">{app.container}</div>
      </div>
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <SelectField label="Who can open it" value={level} onChange={(e) => setLevel(e.target.value)}>
          {LEVELS.map((l) => (
            <option key={l.value} value={l.value}>
              {l.label}
            </option>
          ))}
        </SelectField>
      </div>
      {level === "groups" && (
        <div className="mt-4">
          <GroupChecks groups={groups} selected={groupIds} onChange={setGroupIds} />
          {groups.length === 0 && (
            <p className="text-xs font-medium text-ink-3">Add a group before limiting this app to one.</p>
          )}
        </div>
      )}
      {error && (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      )}
      <Button size="sm" variant="primary" className="mt-4" loading={busy} onClick={() => void save()}>
        Save access
      </Button>
    </li>
  );
}
