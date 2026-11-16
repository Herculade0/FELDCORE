import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type ReactNode,
} from "react";
import "@fontsource/cascadia-mono/latin-400.css";
import "@fontsource/cascadia-mono/latin-700.css";
import "@fontsource/cascadia-code/latin-400.css";
import { api, errorMessage } from "../lib/api";
import type { Container, DockerStatus } from "./docker/types";
import type { Overview } from "./shares/types";
import { cdCommand, HOST_COMMANDS } from "./terminal/commands";
import { TerminalSession, type SessionHandle } from "./terminal/session";
import "./terminal/terminal.css";

/** Same glass pill as the Online status, with a monospace prompt mark. */
export function TerminalButton({
  label,
  disabled,
  onClick,
}: {
  label: string;
  disabled?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="glass inline-flex items-center justify-center rounded-full px-3.5 py-1.5 font-mono text-xs font-bold text-ink-2 hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
    >
      &gt;_
    </button>
  );
}

interface TermTab {
  id: string;
  kind: "host" | "container";
  title: string;
  path: string;
  icon?: string;
}

interface TermState {
  tabs: TermTab[];
  activeId: string | null;
  minimized: boolean;
  maximized: boolean;
}

type Action =
  | { type: "show-host" }
  | { type: "new-host" }
  | { type: "open-container"; id: string; name: string; icon?: string }
  | { type: "select"; id: string }
  | { type: "close"; id: string }
  | { type: "close-all" }
  | { type: "cycle"; delta: number }
  | { type: "reorder"; from: string; to: string; place: "before" | "after" }
  | { type: "minimize" }
  | { type: "restore" }
  | { type: "toggle-max" };

interface TerminalAPI {
  openHost: () => void;
  openContainer: (target: { id: string; name: string; icon?: string }) => void;
}

const Ctx = createContext<TerminalAPI | null>(null);

export function useTerminal(): TerminalAPI {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useTerminal must be used inside TerminalProvider");
  return ctx;
}

function uid(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `t${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
}

function hostTab(): TermTab {
  return { id: uid(), kind: "host", title: "Host", path: "/terminal" };
}

function containerTab(target: { id: string; name: string; icon?: string }): TermTab {
  const title = target.name.trim() || "container";
  return {
    id: uid(),
    kind: "container",
    title,
    path: `/docker/containers/${encodeURIComponent(target.id)}/terminal`,
    icon: target.icon,
  };
}

function withTab(state: TermState, tab: TermTab): TermState {
  return { ...state, tabs: [...state.tabs, tab], activeId: tab.id, minimized: false };
}

function reducer(state: TermState, action: Action): TermState {
  switch (action.type) {
    case "show-host": {
      const existing = state.tabs.find((tab) => tab.kind === "host");
      if (existing) return { ...state, activeId: existing.id, minimized: false };
      return withTab(state, hostTab());
    }
    case "new-host":
      return withTab(state, hostTab());
    case "open-container":
      return withTab(state, containerTab(action));
    case "select":
      return state.tabs.some((tab) => tab.id === action.id) ? { ...state, activeId: action.id, minimized: false } : state;
    case "close": {
      const index = state.tabs.findIndex((tab) => tab.id === action.id);
      if (index < 0) return state;
      const tabs = state.tabs.filter((tab) => tab.id !== action.id);
      let activeId = state.activeId;
      if (state.activeId === action.id) {
        activeId = tabs[Math.min(index, tabs.length - 1)]?.id ?? null;
      }
      if (tabs.length === 0) return { ...state, tabs, activeId: null, minimized: false };
      return { ...state, tabs, activeId };
    }
    case "close-all":
      return { ...state, tabs: [], activeId: null, minimized: false };
    case "cycle": {
      if (state.tabs.length === 0 || !state.activeId) return state;
      const index = state.tabs.findIndex((tab) => tab.id === state.activeId);
      const next = (index + action.delta + state.tabs.length) % state.tabs.length;
      return { ...state, activeId: state.tabs[next].id, minimized: false };
    }
    case "reorder": {
      if (action.from === action.to) return state;
      const from = state.tabs.findIndex((tab) => tab.id === action.from);
      if (from < 0 || state.tabs.every((tab) => tab.id !== action.to)) return state;
      const tabs = state.tabs.slice();
      const [item] = tabs.splice(from, 1);
      let to = tabs.findIndex((tab) => tab.id === action.to);
      if (action.place === "after") to += 1;
      tabs.splice(to, 0, item);
      return { ...state, tabs };
    }
    case "minimize":
      return state.tabs.length === 0 ? state : { ...state, minimized: true };
    case "restore":
      return { ...state, minimized: false };
    case "toggle-max":
      return { ...state, maximized: !state.maximized, minimized: false };
    default:
      return state;
  }
}

const initial: TermState = { tabs: [], activeId: null, minimized: false, maximized: false };

export function TerminalProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initial);
  const api = useMemo<TerminalAPI>(
    () => ({
      openHost: () => dispatch({ type: "show-host" }),
      openContainer: (target) => dispatch({ type: "open-container", ...target }),
    }),
    [],
  );
  return (
    <Ctx.Provider value={api}>
      {children}
      <TerminalWindow state={state} dispatch={dispatch} />
    </Ctx.Provider>
  );
}

function TerminalWindow({ state, dispatch }: { state: TermState; dispatch: (action: Action) => void }) {
  const handles = useRef(new Map<string, SessionHandle>());
  const [drag, setDrag] = useState<{ id: string; over: string; place: "before" | "after" } | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [match, setMatch] = useState<{ index: number; count: number } | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const activeRef = useRef(state.activeId);
  activeRef.current = state.activeId;

  const onHandle = useCallback((id: string, handle: SessionHandle | null) => {
    if (handle) handles.current.set(id, handle);
    else handles.current.delete(id);
  }, []);
  const onSearchResults = useCallback((id: string, index: number, count: number) => {
    if (id !== activeRef.current) return;
    setMatch(count > 0 ? { index, count } : null);
  }, []);

  const active = state.tabs.find((tab) => tab.id === state.activeId) ?? null;
  const activeHandle = () => (state.activeId ? handles.current.get(state.activeId) : undefined);

  const sendLine = (line: string) => {
    const handle = activeHandle();
    if (!handle) return;
    // Ctrl-U drops a half-typed line so the command is not glued to it.
    handle.send(`\u0015${line}\n`);
    handle.focus();
  };

  useEffect(() => {
    if (state.tabs.length === 0 || state.minimized) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [state.tabs.length, state.minimized]);

  useEffect(() => {
    if (!searchOpen) {
      activeHandle()?.clearSearch();
      setMatch(null);
      return;
    }
    const frame = requestAnimationFrame(() => {
      searchRef.current?.focus();
      searchRef.current?.select();
    });
    return () => cancelAnimationFrame(frame);
  }, [searchOpen]);

  useEffect(() => {
    if (state.tabs.length === 0) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && searchOpen) {
        event.preventDefault();
        setSearchOpen(false);
        activeHandle()?.focus();
        return;
      }
      if (!event.ctrlKey || event.altKey || event.metaKey) return;
      const key = event.key.toLowerCase();
      const typing = event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement;
      if (event.shiftKey && key === "t") {
        event.preventDefault();
        dispatch({ type: "new-host" });
      } else if (event.shiftKey && key === "f") {
        event.preventDefault();
        if (state.minimized) dispatch({ type: "restore" });
        setSearchOpen((open) => !open);
      } else if (key === "tab") {
        event.preventDefault();
        dispatch({ type: "cycle", delta: event.shiftKey ? -1 : 1 });
      } else if (event.shiftKey && key === "w") {
        event.preventDefault();
        dispatch({ type: "close-all" });
      } else if (!event.shiftKey && key === "w" && !typing) {
        event.preventDefault();
        if (state.activeId) dispatch({ type: "close", id: state.activeId });
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [state.tabs.length, state.activeId, state.minimized, searchOpen, dispatch]);

  const runSearch = (direction: "next" | "prev", incremental = false) => {
    const handle = activeHandle();
    if (!handle) return;
    if (!query) {
      handle.clearSearch();
      setMatch(null);
      return;
    }
    const found =
      direction === "next"
        ? handle.searchNext(query, caseSensitive, incremental)
        : handle.searchPrev(query, caseSensitive);
    if (!found) setMatch(null);
  };

  if (state.tabs.length === 0) return null;

  return (
    <>
      {!state.minimized && !state.maximized && (
        <div className="wt-backdrop" onMouseDown={() => dispatch({ type: "minimize" })} />
      )}
      <div
        className="wt-window"
        data-max={state.maximized ? "true" : "false"}
        data-min={state.minimized ? "true" : "false"}
        role="dialog"
        aria-modal={state.minimized ? undefined : true}
        aria-hidden={state.minimized || undefined}
        aria-label="Terminal"
      >
        <div className="wt-tabstrip">
          <div className="wt-tabs" role="tablist" aria-label="Terminal sessions">
            {state.tabs.map((tab) => (
              <TabButton
                key={tab.id}
                tab={tab}
                selected={tab.id === state.activeId}
                drop={drag?.over === tab.id ? drag.place : undefined}
                dragging={drag?.id === tab.id}
                onSelect={() => dispatch({ type: "select", id: tab.id })}
                onClose={() => dispatch({ type: "close", id: tab.id })}
                onDragStart={() => setDrag({ id: tab.id, over: tab.id, place: "before" })}
                onDragOver={(place) => drag && setDrag({ id: drag.id, over: tab.id, place })}
                onDrop={() => {
                  if (drag && drag.over === tab.id) {
                    dispatch({ type: "reorder", from: drag.id, to: tab.id, place: drag.place });
                  }
                  setDrag(null);
                }}
                onDragEnd={() => setDrag(null)}
              />
            ))}
            <button type="button" className="wt-new" title="New host shell (Ctrl+Shift+T)" aria-label="New host shell" onClick={() => dispatch({ type: "new-host" })}>
              <PlusIcon />
            </button>
            <NewTabMenu
              onHost={() => dispatch({ type: "new-host" })}
              onContainer={(container) =>
                dispatch({ type: "open-container", id: container.id, name: container.name, icon: container.icon })
              }
            />
          </div>
          <div className="wt-caps">
            <button type="button" className="wt-cap" aria-label="Minimize" title="Minimize" onClick={() => dispatch({ type: "minimize" })}>
              <span className="wt-glyph wt-glyph-min" />
            </button>
            <button
              type="button"
              className="wt-cap"
              aria-label={state.maximized ? "Restore" : "Maximize"}
              title={state.maximized ? "Restore" : "Maximize"}
              onClick={() => dispatch({ type: "toggle-max" })}
            >
              <span className={state.maximized ? "wt-glyph wt-glyph-restore" : "wt-glyph wt-glyph-max"} />
            </button>
            <button type="button" className="wt-cap wt-cap-close" aria-label="Close" title="Close" onClick={() => dispatch({ type: "close-all" })}>
              <span className="wt-x" />
            </button>
          </div>
        </div>

        <ActionBar
          host={active?.kind === "host"}
          label={active?.kind === "container" ? active.title : "Host"}
          onCommand={sendLine}
          onClear={() => activeHandle()?.clear()}
          onCopy={() => activeHandle()?.copy() ?? Promise.resolve("empty")}
          onPaste={() => activeHandle()?.paste() ?? Promise.resolve("blocked")}
        />

        <div className="wt-screens">
          {state.tabs.map((tab) => (
            <TerminalSession
              key={tab.id}
              id={tab.id}
              path={tab.path}
              active={tab.id === state.activeId && !state.minimized}
              onHandle={onHandle}
              onSearchResults={onSearchResults}
            />
          ))}
          {searchOpen && !state.minimized && (
            <form
              className="wt-search"
              data-miss={query && match === null ? "true" : "false"}
              role="search"
              onSubmit={(event) => {
                event.preventDefault();
                runSearch("next");
              }}
            >
              <input
                ref={searchRef}
                value={query}
                placeholder="Find"
                aria-label="Find in terminal"
                spellCheck={false}
                autoCapitalize="off"
                onChange={(event) => {
                  const value = event.target.value;
                  setQuery(value);
                  const handle = activeHandle();
                  if (!handle) return;
                  if (!value) {
                    handle.clearSearch();
                    setMatch(null);
                    return;
                  }
                  if (!handle.searchNext(value, caseSensitive, true)) setMatch(null);
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    runSearch(event.shiftKey ? "prev" : "next");
                  } else if (event.key === "Escape") {
                    event.preventDefault();
                    setSearchOpen(false);
                    activeHandle()?.focus();
                  }
                }}
              />
              <span className="wt-search-count">
                {match && match.count > 0 ? `${match.index >= 0 ? match.index + 1 : match.count}/${match.count}` : ""}
              </span>
              <button type="button" aria-label="Previous match" title="Previous match (Shift+Enter)" onClick={() => runSearch("prev")}>
                <Caret dir="up" />
              </button>
              <button type="button" aria-label="Next match" title="Next match (Enter)" onClick={() => runSearch("next")}>
                <Caret dir="down" />
              </button>
              <button
                type="button"
                aria-label="Match case"
                aria-pressed={caseSensitive}
                title="Match case"
                onClick={() => {
                  const next = !caseSensitive;
                  setCaseSensitive(next);
                  const handle = activeHandle();
                  if (handle && query && !handle.searchNext(query, next, true)) setMatch(null);
                }}
              >
                Aa
              </button>
              <button
                type="button"
                aria-label="Close search"
                title="Close search (Esc)"
                onClick={() => {
                  setSearchOpen(false);
                  activeHandle()?.focus();
                }}
              >
                <span className="wt-x wt-x-sm" />
              </button>
            </form>
          )}
        </div>
      </div>

      {state.minimized && (
        <div className="wt-dock">
          <button type="button" className="wt-dock-main" onClick={() => dispatch({ type: "restore" })}>
            <HostGlyph />
            <span>Terminal</span>
            <span className="wt-dock-count">{state.tabs.length}</span>
          </button>
          <button type="button" className="wt-cap" aria-label="Restore terminal" title="Restore" onClick={() => dispatch({ type: "restore" })}>
            <span className="wt-glyph wt-glyph-restore" />
          </button>
          <button type="button" className="wt-cap wt-cap-close" aria-label="Close terminal" title="Close" onClick={() => dispatch({ type: "close-all" })}>
            <span className="wt-x" />
          </button>
        </div>
      )}
    </>
  );
}

function ActionBar({
  host,
  label,
  onCommand,
  onClear,
  onCopy,
  onPaste,
}: {
  host: boolean;
  label: string;
  onCommand: (line: string) => void;
  onClear: () => void;
  onCopy: () => Promise<"copied" | "empty" | "blocked">;
  onPaste: () => Promise<"pasted" | "empty" | "blocked">;
}) {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [shareState, setShareState] = useState<"loading" | "ready" | "error">("loading");
  const [copyLabel, setCopyLabel] = useState("Copy");
  const [pasteLabel, setPasteLabel] = useState("Paste");
  const flash = useRef(0);

  useEffect(() => {
    if (!host) return;
    let cancel = false;
    setShareState("loading");
    api<Overview>("/shares")
      .then((data) => {
        if (cancel) return;
        setOverview(data);
        setShareState("ready");
      })
      .catch(() => {
        if (cancel) return;
        setOverview(null);
        setShareState("error");
      });
    return () => {
      cancel = true;
    };
  }, [host]);

  useEffect(() => () => window.clearTimeout(flash.current), []);

  const root = overview?.root ?? "";
  const shares = overview?.shares ?? [];
  const appdata = shares.find((share) => share.name === "appdata")?.path || (root ? `${root.replace(/\/$/, "")}/appdata` : "");

  const pulse = (set: (value: string) => void, value: string, reset: string) => {
    window.clearTimeout(flash.current);
    set(value);
    flash.current = window.setTimeout(() => set(reset), 1200);
  };

  return (
    <div className="wt-actions">
      {host ? (
        <>
          <SharesControl
            root={root}
            state={shareState}
            shares={shares.map((share) => ({ name: share.name, path: share.path }))}
            onCommand={onCommand}
          />
          <button
            type="button"
            className="wt-action"
            disabled={!appdata}
            title={appdata ? `Go to ${appdata}` : "Appdata share is not available yet"}
            onClick={() => appdata && onCommand(cdCommand(appdata))}
          >
            Appdata
          </button>
          <button type="button" className="wt-action" title="List containers" onClick={() => onCommand(HOST_COMMANDS.dockerPs)}>
            docker ps
          </button>
          <button type="button" className="wt-action" title="Follow the feldcored journal" onClick={() => onCommand(HOST_COMMANDS.logs)}>
            FELDCORE logs
          </button>
          <button type="button" className="wt-action" title="Run htop, or top if htop is not installed" onClick={() => onCommand(HOST_COMMANDS.htop)}>
            htop
          </button>
        </>
      ) : (
        <span className="wt-kind">{label}</span>
      )}
      <span className="wt-spacer" />
      <span className="wt-sep" />
      <button type="button" className="wt-action" title="Clear the screen" onClick={onClear}>
        Clear
      </button>
      <button
        type="button"
        className="wt-action"
        title="Copy selection (Ctrl+Shift+C)"
        onClick={() => {
          void onCopy().then((result) => {
            pulse(setCopyLabel, result === "copied" ? "Copied" : result === "empty" ? "No selection" : "Blocked", "Copy");
          });
        }}
      >
        {copyLabel}
      </button>
      <button
        type="button"
        className="wt-action"
        title="Paste (Ctrl+Shift+V)"
        onClick={() => {
          void onPaste().then((result) => {
            if (result === "pasted") pulse(setPasteLabel, "Pasted", "Paste");
            else pulse(setPasteLabel, result === "empty" ? "Empty" : "Blocked", "Paste");
          });
        }}
      >
        {pasteLabel}
      </button>
    </div>
  );
}

function SharesControl({
  root,
  state,
  shares,
  onCommand,
}: {
  root: string;
  state: "loading" | "ready" | "error";
  shares: { name: string; path: string }[];
  onCommand: (line: string) => void;
}) {
  const menu = useMenu();
  return (
    <div className="wt-split">
      <button
        type="button"
        className="wt-action"
        disabled={!root}
        title={root ? `Go to ${root}` : "Share root is not available yet"}
        onClick={() => root && onCommand(cdCommand(root))}
      >
        Shares
      </button>
      <button
        type="button"
        className="wt-action wt-split-chev"
        ref={menu.buttonRef}
        aria-label="Choose a share"
        aria-expanded={menu.open}
        aria-haspopup="menu"
        title="Choose a share"
        onClick={menu.toggle}
      >
        <ChevronIcon />
      </button>
      {menu.open && (
        <div className="wt-menu" role="menu" ref={menu.menuRef} style={{ top: menu.pos.top, left: menu.pos.left }}>
          <div className="wt-menu-label">Shares</div>
          {shares.length === 0 && (
            <div className="wt-menu-empty">
              {state === "loading" ? "Loading shares…" : state === "error" ? "Couldn't load shares" : "No shares yet"}
            </div>
          )}
          {shares.map((share) => (
            <button
              type="button"
              role="menuitem"
              key={share.name}
              title={share.path}
              onClick={() => {
                onCommand(cdCommand(share.path));
                menu.close();
              }}
            >
              <span>{share.name}</span>
              <span className="wt-menu-path">{share.path}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function NewTabMenu({
  onHost,
  onContainer,
}: {
  onHost: () => void;
  onContainer: (container: Container) => void;
}) {
  const menu = useMenu();
  const [containers, setContainers] = useState<Container[] | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    if (!menu.open) return;
    let cancel = false;
    setMessage(null);
    setContainers(null);
    (async () => {
      try {
        const status = await api<DockerStatus>("/docker/status");
        if (cancel) return;
        if (!status.running) {
          setContainers([]);
          setMessage(status.installed ? "Docker isn't running" : "Docker isn't installed");
          return;
        }
        const res = await api<{ containers: Container[] }>("/docker/containers");
        if (cancel) return;
        const running = (res.containers ?? []).filter((container) => container.state === "running");
        running.sort((a, b) => a.name.localeCompare(b.name));
        setContainers(running);
      } catch (error: unknown) {
        if (cancel) return;
        setContainers([]);
        setMessage(errorMessage(error));
      }
    })();
    return () => {
      cancel = true;
    };
  }, [menu.open]);

  return (
    <>
      <button
        type="button"
        className="wt-new"
        ref={menu.buttonRef}
        aria-label="Open a shell or container"
        aria-expanded={menu.open}
        aria-haspopup="menu"
        title="New tab"
        onClick={menu.toggle}
      >
        <ChevronIcon />
      </button>
      {menu.open && (
        <div className="wt-menu" role="menu" ref={menu.menuRef} style={{ top: menu.pos.top, left: menu.pos.left }}>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              onHost();
              menu.close();
            }}
          >
            <HostGlyph />
            <span>Host shell</span>
            <span className="wt-menu-key">Ctrl+Shift+T</span>
          </button>
          <div className="wt-menu-label">Containers</div>
          {containers === null && <div className="wt-menu-empty">Checking containers…</div>}
          {containers !== null && message && <div className="wt-menu-empty">{message}</div>}
          {containers !== null && !message && containers.length === 0 && <div className="wt-menu-empty">No running containers</div>}
          {containers?.map((container) => (
            <button
              type="button"
              role="menuitem"
              key={container.id}
              onClick={() => {
                onContainer(container);
                menu.close();
              }}
            >
              <ContainerGlyph src={container.icon} title={container.name} />
              <span>{container.name}</span>
            </button>
          ))}
        </div>
      )}
    </>
  );
}

function TabButton({
  tab,
  selected,
  drop,
  dragging,
  onSelect,
  onClose,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
}: {
  tab: TermTab;
  selected: boolean;
  drop?: "before" | "after";
  dragging: boolean;
  onSelect: () => void;
  onClose: () => void;
  onDragStart: () => void;
  onDragOver: (place: "before" | "after") => void;
  onDrop: () => void;
  onDragEnd: () => void;
}) {
  return (
    <div
      role="tab"
      aria-selected={selected}
      title={tab.title}
      className="wt-tab"
      data-drop={drop}
      data-dragging={dragging ? "true" : "false"}
      draggable
      onClick={onSelect}
      onAuxClick={(event) => {
        if (event.button === 1) {
          event.preventDefault();
          onClose();
        }
      }}
      onMouseDown={(event) => {
        if (event.button === 1) event.preventDefault();
      }}
      onDragStart={(event) => {
        if ((event.target as HTMLElement).closest(".wt-tab-x")) {
          event.preventDefault();
          return;
        }
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/plain", tab.id);
        onDragStart();
      }}
      onDragOver={(event) => {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        const rect = event.currentTarget.getBoundingClientRect();
        onDragOver(event.clientX < rect.left + rect.width / 2 ? "before" : "after");
      }}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
      onDragEnd={onDragEnd}
    >
      {tab.kind === "host" ? <HostGlyph /> : <ContainerGlyph src={tab.icon} title={tab.title} />}
      <span className="wt-tab-title">{tab.title}</span>
      <button
        type="button"
        className="wt-tab-x"
        aria-label={`Close ${tab.title}`}
        title="Close"
        onClick={(event) => {
          event.stopPropagation();
          onClose();
        }}
      >
        <span className="wt-x wt-x-sm" />
      </button>
    </div>
  );
}

function useMenu() {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState({ top: 0, left: 0 });
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const toggle = () => {
    const button = buttonRef.current;
    if (!button) return;
    const rect = button.getBoundingClientRect();
    const width = 280;
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
    setPos({ top: rect.bottom + 4, left });
    setOpen((value) => !value);
  };

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: MouseEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target) || buttonRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", onPointer);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onPointer);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return { open, pos, buttonRef, menuRef, toggle, close: () => setOpen(false) };
}

function HostGlyph() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      <rect width="16" height="16" rx="3" fill="#3B78FF" />
      <path d="M4 5.2 7.1 8 4 10.8" fill="none" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M8.3 11.1H12" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

function ContainerGlyph({ src, title }: { src?: string; title: string }) {
  const [failed, setFailed] = useState(false);
  if (src && !failed) {
    return <img src={src} alt="" referrerPolicy="no-referrer" className="wt-tab-img" onError={() => setFailed(true)} />;
  }
  return <span className="wt-tab-letter">{(title.trim()[0] || "C").toUpperCase()}</span>;
}

function PlusIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
      <path d="M6 1.2v9.6M1.2 6h9.6" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  );
}

function ChevronIcon() {
  return (
    <svg width="10" height="6" viewBox="0 0 10 6" aria-hidden="true">
      <path d="M1 1.2 5 4.8 9 1.2" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function Caret({ dir }: { dir: "up" | "down" }) {
  return (
    <svg width="10" height="6" viewBox="0 0 10 6" aria-hidden="true" style={{ transform: dir === "up" ? "rotate(180deg)" : undefined }}>
      <path d="M1 1.2 5 4.8 9 1.2" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
