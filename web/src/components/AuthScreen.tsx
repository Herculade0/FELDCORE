import { useState, type FormEvent, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { useAuth } from "../lib/auth";
import { errorMessage } from "../lib/api";
import { loginWithPasskey, passkeysAvailable } from "../lib/webauthn";
import { LogoMark, Wordmark } from "./Logo";
import { Alert, Button, TextField } from "./form";

/** Full-page frame shared by the sign-in, setup and status screens. */
function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center px-4 py-10">
      <div className="mb-8 flex items-center gap-3.5">
        <LogoMark size={48} />
        <div className="scale-110 origin-left">
          <Wordmark />
        </div>
      </div>
      {children}
    </div>
  );
}

export function LoadingScreen({ unreachable = false }: { unreachable?: boolean }) {
  return (
    <Frame>
      <div className="flex items-center gap-3 text-sm font-semibold text-ink-2" role="status">
        <Loader2 size={17} className="animate-spin text-accent" />
        {unreachable ? "Can't reach the server. Retrying…" : "Establishing link…"}
      </div>
    </Frame>
  );
}

/** First run: create the administrator account. */
export function SetupScreen() {
  const { setup } = useAuth();
  const [code, setCode] = useState("");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mismatch = confirm.length > 0 && confirm !== password;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (password !== confirm) {
      setError("The two passwords don't match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await setup(username.trim(), password, code.trim());
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <Frame>
      <form onSubmit={submit} className="glass w-full max-w-sm rounded-[2rem] p-7">
        <h1 className="text-xl font-extrabold tracking-tight text-ink">Welcome to FELDCORE</h1>
        <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
          Enter the setup code printed in the server console, then create the administrator account.
          The code works once, so someone else on the network cannot claim this server first.
        </p>
        <div className="mt-6 space-y-4">
          <TextField
            label="Setup code"
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            placeholder="ABCD-EFGH"
            hint="Printed as “FELDCORE setup code” when the server starts."
            required
            autoFocus
          />
          <TextField
            label="Username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            required
          />
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
          <TextField
            label="Confirm password"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
            error={mismatch ? "This doesn't match the password above." : null}
            required
          />
          {error && <Alert>{error}</Alert>}
        </div>
        <Button type="submit" variant="primary" loading={busy} className="mt-6 w-full">
          Create account
        </Button>
      </form>
    </Frame>
  );
}

export function LoginScreen() {
  const { signIn, refresh } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needsCode, setNeedsCode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const result = await signIn(username.trim(), password, needsCode ? code.trim() : undefined);
      if (result === "totp") {
        setNeedsCode(true);
        setBusy(false);
      }
    } catch (err) {
      setError(errorMessage(err));
      if (!needsCode) setPassword("");
      setCode("");
      setBusy(false);
    }
  };

  const passkey = async () => {
    setBusy(true);
    setError(null);
    try {
      await loginWithPasskey();
      await refresh();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <Frame>
      <form onSubmit={submit} className="glass w-full max-w-sm rounded-[2rem] p-7">
        <h1 className="text-xl font-extrabold tracking-tight text-ink">Citizen ID</h1>
        <p className="mt-2 text-[11px] font-bold uppercase tracking-[0.2em] text-ink-3">FELDCORE</p>
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          {needsCode
            ? `Enter the 6-digit code from the authenticator for ${username.trim()}.`
            : "One sign-in for this server and the apps published on it."}
        </p>
        <div className="mt-6 space-y-4">
          {!needsCode && (
            <>
              <TextField
                label="Username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                required
                autoFocus
              />
              <TextField
                label="Password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </>
          )}
          {needsCode && (
            <TextField
              label="Authenticator code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
              required
              autoFocus
            />
          )}
          {error && <Alert>{error}</Alert>}
        </div>
        <Button type="submit" variant="primary" loading={busy} className="mt-6 w-full">
          {needsCode ? "Verify" : "Sign in"}
        </Button>
        {needsCode ? (
          <Button
            type="button"
            variant="ghost"
            className="mt-2 w-full"
            disabled={busy}
            onClick={() => {
              setNeedsCode(false);
              setCode("");
              setError(null);
            }}
          >
            Use a different account
          </Button>
        ) : (
          passkeysAvailable() && (
            <Button type="button" variant="secondary" className="mt-3 w-full" disabled={busy} onClick={() => void passkey()}>
              Sign in with a passkey
            </Button>
          )
        )}
      </form>
    </Frame>
  );
}
