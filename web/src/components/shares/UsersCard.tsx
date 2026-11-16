import { useState } from "react";
import { KeyRound, Trash2, UserRound, Users } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, IconButton, Modal, TextField } from "../form";
import type { Overview } from "./types";

/** Accounts people use to open private SMB shares. */
export default function UsersCard({
  overview,
  onChanged,
}: {
  overview: Overview;
  onChanged: (notice: string) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [passwordFor, setPasswordFor] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const ready = overview.services.smb.installed && overview.services.privileged;

  return (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <CardTitle icon={Users} title="Share users" />
        <Button size="sm" onClick={() => setAdding(true)} disabled={!ready}>
          Add user
        </Button>
      </div>
      <p className="mt-3 text-xs font-medium leading-relaxed text-ink-3">
        People who can open private SMB shares. These accounts are separate from Citizen ID and
        cannot sign in to this server.
      </p>

      {!overview.services.smb.installed && (
        <p className="mt-4 text-sm font-medium text-ink-2">
          Install Samba above to add share users.
        </p>
      )}

      {overview.users.length > 0 && (
        <ul className="mt-4 divide-y divide-line">
          {overview.users.map((u) => (
            <li key={u} className="flex items-center gap-3 py-2.5">
              <span className="flex h-8 w-8 items-center justify-center rounded-xl bg-accent/12 text-accent">
                <UserRound size={15} />
              </span>
              <span className="min-w-0 flex-1 truncate text-sm font-bold text-ink">{u}</span>
              <IconButton label={`Change password for ${u}`} onClick={() => setPasswordFor(u)}>
                <KeyRound size={16} />
              </IconButton>
              <IconButton label={`Delete ${u}`} onClick={() => setDeleting(u)}>
                <Trash2 size={16} />
              </IconButton>
            </li>
          ))}
        </ul>
      )}

      {adding && (
        <CredentialDialog
          title="Add share user"
          askName
          submitLabel="Add user"
          onClose={() => setAdding(false)}
          onSubmit={async (name, password) => {
            await api("/shares/users", { method: "POST", body: { name, password } });
            setAdding(false);
            onChanged(`${name} can now be given access to private shares.`);
          }}
        />
      )}

      {passwordFor && (
        <CredentialDialog
          title={`New password for ${passwordFor}`}
          submitLabel="Change password"
          onClose={() => setPasswordFor(null)}
          onSubmit={async (_, password) => {
            await api(`/shares/users/${encodeURIComponent(passwordFor)}/password`, {
              method: "PUT",
              body: { password },
            });
            const who = passwordFor;
            setPasswordFor(null);
            onChanged(`Password changed for ${who}.`);
          }}
        />
      )}

      {deleting && (
        <DeleteUserDialog
          name={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            const who = deleting;
            setDeleting(null);
            onChanged(`${who} was deleted.`);
          }}
        />
      )}
    </Card>
  );
}

function CredentialDialog({
  title,
  askName = false,
  submitLabel,
  onClose,
  onSubmit,
}: {
  title: string;
  askName?: boolean;
  submitLabel: string;
  onClose: () => void;
  onSubmit: (name: string, password: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSubmit(name.trim(), password);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Modal
      title={title}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy}>
            {submitLabel}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {askName && (
          <TextField
            label="User name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="alice"
            hint="Lowercase letters, numbers, hyphens and underscores."
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={31}
            required
            autoFocus
          />
        )}
        <TextField
          label="Password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          hint="At least 8 characters. This is what they type when their computer asks."
          autoComplete="new-password"
          minLength={8}
          required
          autoFocus={!askName}
        />
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}

function DeleteUserDialog({
  name,
  onClose,
  onDeleted,
}: {
  name: string;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      await api(`/shares/users/${encodeURIComponent(name)}`, { method: "DELETE" });
      onDeleted();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <Modal
      title={`Delete ${name}?`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="danger" onClick={confirm} loading={busy}>
            Delete user
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-sm font-medium leading-relaxed text-ink-2">
          {name} will no longer be able to sign in to any share. Files they saved stay where they
          are.
        </p>
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}
