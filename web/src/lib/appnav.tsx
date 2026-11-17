import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { NavKey } from "../nav";
import { api, errorMessage } from "./api";

export interface HelpRoute {
  page: string;
  tab?: "general" | "network" | "remote" | "publishing" | "citizen";
  anchor?: string;
  view?: string;
  help?: string;
  available: boolean;
  note?: string;
}

export interface HelpTopic {
  id: string;
  kind: "guide" | "reference";
  title: string;
  summary: string;
  icon: string;
  order: number;
  popular: boolean;
  comingSoon: boolean;
  related: string[];
  references: string[];
  page?: string;
  body: string;
}

interface Catalog {
  topics: HelpTopic[];
  routes: Record<string, HelpRoute>;
}

export interface Destination {
  page: NavKey;
  tab?: HelpRoute["tab"];
  anchor?: string;
  view?: string;
  topic?: string;
  heading?: string;
  nonce: number;
}

const PAGES: readonly NavKey[] = [
  "dashboard",
  "launcher",
  "storage",
  "shares",
  "docker",
  "stacks",
  "apps",
  "vms",
  "backups",
  "health",
  "audit",
  "settings",
  "help",
];

function isPage(value: string): value is NavKey {
  return (PAGES as readonly string[]).includes(value);
}

interface NavApi {
  dest: Destination;
  topics: HelpTopic[] | null;
  routes: Record<string, HelpRoute>;
  helpError: string | null;
  reloadHelp: () => void;
  go: (page: NavKey) => void;
  openHelp: (topic?: string, heading?: string) => void;
  openRoute: (id: string) => void;
}

const Ctx = createContext<NavApi | null>(null);

export function AppNavProvider({ children }: { children: ReactNode }) {
  const [dest, setDest] = useState<Destination>({ page: "dashboard", nonce: 0 });
  const [topics, setTopics] = useState<HelpTopic[] | null>(null);
  const [routes, setRoutes] = useState<Record<string, HelpRoute>>({});
  const [helpError, setHelpError] = useState<string | null>(null);

  const reloadHelp = useCallback(() => {
    setHelpError(null);
    api<Catalog>("/help")
      .then((cat) => {
        setTopics(cat.topics ?? []);
        setRoutes(cat.routes ?? {});
      })
      .catch((e) => setHelpError(errorMessage(e)));
  }, []);

  useEffect(() => {
    reloadHelp();
  }, [reloadHelp]);

  const go = useCallback((page: NavKey) => {
    setDest((d) => ({ page, nonce: d.nonce + 1 }));
  }, []);

  const openHelp = useCallback((topic?: string, heading?: string) => {
    setDest((d) => ({ page: "help", topic, heading, nonce: d.nonce + 1 }));
  }, []);

  const openRoute = useCallback(
    (id: string) => {
      const route = routes[id];
      if (!route) return;
      if (!route.available) {
        openHelp(route.help);
        return;
      }
      const page = route.page;
      if (!isPage(page) || page === "help") return;
      setDest((d) => ({
        page,
        tab: route.tab,
        anchor: route.anchor,
        view: route.view,
        nonce: d.nonce + 1,
      }));
    },
    [openHelp, routes],
  );

  const value = useMemo(
    () => ({ dest, topics, routes, helpError, reloadHelp, go, openHelp, openRoute }),
    [dest, topics, routes, helpError, reloadHelp, go, openHelp, openRoute],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAppNav(): NavApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAppNav must be used inside AppNavProvider");
  return ctx;
}

/** Scroll to the anchor a help button asked for, once this page is showing. */
export function useHelpAnchor(page: NavKey) {
  const { dest } = useAppNav();
  useEffect(() => {
    if (dest.page !== page || !dest.anchor) return;
    const timer = window.setTimeout(() => {
      const el = document.getElementById(dest.anchor!);
      if (!el) return;
      el.style.scrollMarginTop = "1.5rem";
      el.scrollIntoView({ behavior: "smooth", block: "start" });
      el.classList.add("help-target");
      window.setTimeout(() => el.classList.remove("help-target"), 1800);
    }, 60);
    return () => window.clearTimeout(timer);
  }, [dest.nonce, dest.anchor, dest.page, page]);
}
