import { useState, type FormEvent, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { useAuth } from "../lib/auth";
import { errorMessage } from "../lib/api";
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
      await setup(username.trim(), password);
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
          Create the administrator account. You'll use it to sign in to this server from now on.
        </p>
        <div className="mt-6 space-y-4">
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
            autoFocus
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
  const { signIn } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await signIn(username.trim(), password);
    } catch (err) {
      setError(errorMessage(err));
      setPassword("");
      setBusy(false);
    }
  };

  return (
    <Frame>
      <form onSubmit={submit} className="glass w-full max-w-sm rounded-[2rem] p-7">
        <h1 className="text-xl font-extrabold tracking-tight text-ink">Sign in</h1>
        <p className="mt-2 text-[11px] font-bold uppercase tracking-[0.2em] text-ink-3">
          Authorised personnel only
        </p>
        <div className="mt-6 space-y-4">
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
          {error && <Alert>{error}</Alert>}
        </div>
        <Button type="submit" variant="primary" loading={busy} className="mt-6 w-full">
          Sign in
        </Button>
      </form>
    </Frame>
  );
}
