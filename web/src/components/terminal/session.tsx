import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { SearchAddon } from "@xterm/addon-search";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import "@xterm/xterm/css/xterm.css";
import { campbell, searchDecorations, TERM_FONT } from "./theme";

export interface SessionHandle {
  send: (text: string) => void;
  copy: () => Promise<"copied" | "empty" | "blocked">;
  paste: () => Promise<"pasted" | "empty" | "blocked">;
  clear: () => void;
  searchNext: (query: string, caseSensitive: boolean, incremental?: boolean) => boolean;
  searchPrev: (query: string, caseSensitive: boolean) => boolean;
  clearSearch: () => void;
  fit: () => void;
  focus: () => void;
}

/**
 * One xterm.js session: its own PTY WebSocket, WebGL renderer (canvas if WebGL
 * cannot start), clickable links, search, and Unicode 11 widths.
 * Inactive sessions stay mounted so their shells keep running.
 */
export function TerminalSession({
  id,
  path,
  active,
  onHandle,
  onSearchResults,
}: {
  id: string;
  path: string;
  active: boolean;
  onHandle: (id: string, handle: SessionHandle | null) => void;
  onSearchResults: (id: string, index: number, count: number) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const apiRef = useRef<SessionHandle | null>(null);
  const onHandleRef = useRef(onHandle);
  const onResultsRef = useRef(onSearchResults);
  onHandleRef.current = onHandle;
  onResultsRef.current = onSearchResults;
  const [tip, setTip] = useState<{ text: string; x: number; y: number } | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const term = new Terminal({
      allowProposedApi: true,
      cursorBlink: true,
      cursorStyle: "bar",
      cursorWidth: 2,
      cursorInactiveStyle: "outline",
      fontFamily: TERM_FONT,
      fontSize: 15,
      fontWeight: "400",
      fontWeightBold: "700",
      lineHeight: 1,
      letterSpacing: 0,
      scrollback: 10000,
      drawBoldTextInBrightColors: true,
      minimumContrastRatio: 1,
      theme: campbell,
      overviewRuler: { width: 8 },
    });
    const fit = new FitAddon();
    const search = new SearchAddon();
    term.loadAddon(fit);
    term.loadAddon(search);
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = "11";
    term.loadAddon(
      new WebLinksAddon(
        (event, uri) => {
          event.preventDefault();
          window.open(uri, "_blank", "noopener,noreferrer");
        },
        {
          hover: (event, text) => {
            const x = Math.min(event.clientX + 12, window.innerWidth - 440);
            const y = Math.min(event.clientY + 16, window.innerHeight - 36);
            setTip({ text, x: Math.max(8, x), y: Math.max(8, y) });
          },
          leave: () => setTip(null),
        },
      ),
    );
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => {
        webgl.dispose();
      });
      term.loadAddon(webgl);
    } catch {
      // The canvas renderer is already active. WebGL is an acceleration, not a requirement.
    }

    term.open(host);
    const resultsSub = search.onDidChangeResults((event) => {
      onResultsRef.current(id, event.resultIndex, event.resultCount);
    });

    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/v1${path}`);
    ws.binaryType = "arraybuffer";
    const encoder = new TextEncoder();
    const pending: string[] = [];
    let alive = true;

    const sendRaw = (text: string) => {
      const bytes = encoder.encode(text);
      if (ws.readyState === WebSocket.OPEN) ws.send(bytes);
      else if (alive) pending.push(text);
    };
    const sendResize = () => {
      if (host.clientWidth < 20 || host.clientHeight < 20) return;
      try {
        fit.fit();
      } catch {
        return;
      }
      if (ws.readyState === WebSocket.OPEN && term.cols > 0 && term.rows > 0) {
        ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      }
    };

    const options = (caseSensitive: boolean, incremental?: boolean) => ({
      caseSensitive,
      incremental,
      decorations: searchDecorations,
    });

    const handle: SessionHandle = {
      send: sendRaw,
      copy: async () => {
        const text = term.getSelection();
        if (!text) return "empty";
        try {
          await navigator.clipboard.writeText(text);
          return "copied";
        } catch {
          return "blocked";
        }
      },
      paste: async () => {
        try {
          const text = await navigator.clipboard.readText();
          if (!text) return "empty";
          term.paste(text);
          term.focus();
          return "pasted";
        } catch {
          return "blocked";
        }
      },
      clear: () => {
        term.clear();
        sendRaw("\x0c");
        term.focus();
      },
      searchNext: (query, caseSensitive, incremental) =>
        search.findNext(query, options(caseSensitive, incremental)),
      searchPrev: (query, caseSensitive) => search.findPrevious(query, options(caseSensitive)),
      clearSearch: () => search.clearDecorations(),
      fit: sendResize,
      focus: () => term.focus(),
    };
    apiRef.current = handle;
    onHandleRef.current(id, handle);

    ws.onopen = () => {
      sendResize();
      for (const text of pending.splice(0)) sendRaw(text);
    };
    ws.onmessage = (ev) => {
      if (!alive) return;
      if (ev.data instanceof ArrayBuffer) term.write(new Uint8Array(ev.data));
      else if (typeof ev.data === "string") term.write(ev.data);
    };
    ws.onclose = () => {
      if (!alive) return;
      term.write("\r\n\r\nConnection closed.\r\n");
    };
    const data = term.onData((chunk) => sendRaw(chunk));
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown" || !event.ctrlKey || event.altKey || event.metaKey) return true;
      const key = event.key.toLowerCase();
      // Tab, search, and close are handled for the whole window.
      if (key === "tab" || (event.shiftKey && (key === "t" || key === "f" || key === "w")) || (!event.shiftKey && key === "w")) {
        return false;
      }
      if (event.shiftKey && key === "c") {
        event.preventDefault();
        void handle.copy();
        return false;
      }
      if (event.shiftKey && key === "v") {
        event.preventDefault();
        void handle.paste();
        return false;
      }
      return true;
    });

    const onContext = (event: MouseEvent) => {
      event.preventDefault();
      if (term.hasSelection()) void handle.copy();
      else void handle.paste();
    };
    host.addEventListener("contextmenu", onContext);

    const observer = new ResizeObserver(() => sendResize());
    observer.observe(host);
    const onResize = () => sendResize();
    window.addEventListener("resize", onResize);
    void document.fonts?.ready.then(() => {
      if (alive) sendResize();
    });
    requestAnimationFrame(() => {
      if (alive) {
        sendResize();
        if (active) term.focus();
      }
    });

    return () => {
      alive = false;
      observer.disconnect();
      window.removeEventListener("resize", onResize);
      host.removeEventListener("contextmenu", onContext);
      data.dispose();
      resultsSub.dispose();
      ws.close();
      term.dispose();
      apiRef.current = null;
      onHandleRef.current(id, null);
    };
    // The socket follows the tab. Focusing the active session is a separate effect.
  }, [id, path]);

  useEffect(() => {
    if (!active) return;
    apiRef.current?.fit();
    apiRef.current?.focus();
  }, [active]);

  return (
    <div className="wt-screen" data-active={active ? "true" : "false"}>
      <div ref={hostRef} className="wt-host" />
      {tip && active && (
        <div className="wt-linktip" style={{ left: tip.x, top: tip.y }}>
          {tip.text}
        </div>
      )}
    </div>
  );
}
