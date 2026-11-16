import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { api, UNAUTHORIZED_EVENT } from "./api";

/**
 * loading      checking with the server
 * setup        fresh install: no administrator yet
 * login        needs sign-in
 * ready        signed in
 * unreachable  the daemon is not answering
 */
export type AuthPhase = "loading" | "setup" | "login" | "ready" | "unreachable";

interface StatusResponse {
  setupRequired: boolean;
  authenticated: boolean;
  /** True for the administrator. A directory user is signed in with this false. */
  admin?: boolean;
  wizardPending?: boolean;
  username?: string;
}

interface LoginResponse {
  username?: string;
  admin?: boolean;
  totpRequired?: boolean;
}

interface AuthValue {
  phase: AuthPhase;
  username: string | null;
  /** False until status says this session is the administrator. */
  admin: boolean;
  wizardPending: boolean;
  /** "totp" means the password was accepted and an authenticator code is still required. */
  signIn: (username: string, password: string, totp?: string) => Promise<"ok" | "totp">;
  setup: (username: string, password: string, code: string) => Promise<void>;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<AuthPhase>("loading");
  const [username, setUsername] = useState<string | null>(null);
  const [admin, setAdmin] = useState(false);
  const [wizardPending, setWizardPending] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const s = await api<StatusResponse>("/auth/status");
      setUsername(s.authenticated ? (s.username ?? null) : null);
      setAdmin(s.authenticated ? Boolean(s.admin) : false);
      setWizardPending(Boolean(s.wizardPending));
      setPhase(s.authenticated ? "ready" : s.setupRequired ? "setup" : "login");
    } catch {
      setPhase("unreachable");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Any API call that comes back 401 means the session ended.
  useEffect(() => {
    const onUnauthorized = () => void refresh();
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [refresh]);

  // Keep retrying while the daemon is down, so the page recovers by itself.
  useEffect(() => {
    if (phase !== "unreachable") return;
    const t = setTimeout(() => void refresh(), 3000);
    return () => clearTimeout(t);
  }, [phase, refresh]);

  const value = useMemo<AuthValue>(
    () => ({
      phase,
      username,
      admin,
      wizardPending,
      refresh,
      signIn: async (u, p, totp) => {
        const res = await api<LoginResponse>("/citizen/login", {
          method: "POST",
          body: { username: u, password: p, totp: totp ?? "" },
          quiet401: true,
        });
        if (res.totpRequired) return "totp";
        await refresh();
        return "ok";
      },
      setup: async (u, p, code) => {
        await api("/auth/setup", { method: "POST", body: { username: u, password: p, code } });
        await refresh();
      },
      signOut: async () => {
        try {
          await api("/auth/logout", { method: "POST" });
        } finally {
          await refresh();
        }
      },
    }),
    [phase, username, admin, wizardPending, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
