import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export type Mode = "light" | "dark" | "system";

export const ACCENTS = [
  { key: "feldcorp", name: "Feldcorp", from: "#0b5cff", to: "#38bdf8" },
  { key: "violet", name: "Violet", from: "#7c5cff", to: "#22c8ee" },
  { key: "sunset", name: "Sunset", from: "#ff5f6d", to: "#ffb347" },
  { key: "forest", name: "Forest", from: "#10b981", to: "#a3e635" },
  { key: "rose", name: "Rose", from: "#f43f8e", to: "#a855f7" },
] as const;

export type AccentKey = (typeof ACCENTS)[number]["key"];

interface ThemeState {
  mode: Mode;
  accent: AccentKey;
}

interface ThemeContextValue extends ThemeState {
  /** The mode actually on screen once "system" is resolved. */
  resolved: "light" | "dark";
  setMode: (m: Mode) => void;
  setAccent: (a: AccentKey) => void;
}

const KEY = "feldcore.theme";
const DEFAULT: ThemeState = { mode: "system", accent: "feldcorp" };

function load(): ThemeState {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? "{}");
    return {
      mode: ["light", "dark", "system"].includes(raw.mode) ? raw.mode : DEFAULT.mode,
      accent: ACCENTS.some((a) => a.key === raw.accent) ? raw.accent : DEFAULT.accent,
    };
  } catch {
    return DEFAULT;
  }
}

function systemPrefersDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<ThemeState>(load);
  const [sysDark, setSysDark] = useState<boolean>(systemPrefersDark);

  // Follow the OS setting while in "system" mode.
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e: MediaQueryListEvent) => setSysDark(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const resolved: "light" | "dark" =
    state.mode === "system" ? (sysDark ? "dark" : "light") : state.mode;

  useEffect(() => {
    const el = document.documentElement;
    el.dataset.theme = resolved;
    el.dataset.accent = state.accent;
    try {
      localStorage.setItem(KEY, JSON.stringify(state));
    } catch {
      /* storage unavailable (private mode); theme still applies for this visit */
    }
  }, [state, resolved]);

  const value = useMemo<ThemeContextValue>(
    () => ({
      ...state,
      resolved,
      setMode: (mode) => setState((s) => ({ ...s, mode })),
      setAccent: (accent) => setState((s) => ({ ...s, accent })),
    }),
    [state, resolved],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used inside <ThemeProvider>");
  return ctx;
}
