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
  username?: string;
}

interface AuthValue {
  phase: AuthPhase;
  username: string | null;
  signIn: (username: string, password: string) => Promise<void>;
  setup: (username: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<AuthPhase>("loading");
  const [username, setUsername] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api<StatusResponse>("/auth/status");
      setUsername(s.authenticated ? (s.username ?? null) : null);
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
      refresh,
      signIn: async (u, p) => {
        await api("/auth/login", {
          method: "POST",
          body: { username: u, password: p },
          quiet401: true,
        });
        await refresh();
      },
      setup: async (u, p) => {
        await api("/auth/setup", { method: "POST", body: { username: u, password: p } });
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
    [phase, username, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
