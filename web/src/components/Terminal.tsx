import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { X } from "lucide-react";
import { IconButton } from "./form";

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

/**
 * Full-height terminal. `path` is an API path such as /terminal or a container console.
 */
export function TerminalModal({ title, path, onClose }: { title: string; path: string; onClose: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      fontSize: 14,
      theme: termTheme(),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    termRef.current = term;
    fit.fit();

    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/v1${path}`);
    ws.binaryType = "arraybuffer";

    const sendResize = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      }
    };
    ws.onopen = sendResize;
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) term.write(new Uint8Array(ev.data));
    };
    ws.onclose = () => term.write("\r\n\r\nConnection closed.\r\n");
    const data = term.onData((chunk) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(chunk));
    });
    term.attachCustomKeyEventHandler((e) => {
      if (e.type === "keydown" && e.ctrlKey && e.shiftKey && (e.key === "C" || e.key === "c")) {
        void copySelection(term);
        return false;
      }
      if (e.type === "keydown" && e.ctrlKey && e.shiftKey && (e.key === "V" || e.key === "v")) {
        void pasteInto(term);
        return false;
      }
      return true;
    });

    const onResize = () => sendResize();
    window.addEventListener("resize", onResize);
    const observer = new ResizeObserver(onResize);
    observer.observe(el);

    return () => {
      observer.disconnect();
      window.removeEventListener("resize", onResize);
      data.dispose();
      ws.close();
      term.dispose();
      termRef.current = null;
    };
  }, [path]);

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center sm:items-center sm:p-6" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-black/55 backdrop-blur-sm" onClick={onClose} />
      <div className="panel relative flex h-[100dvh] w-full min-w-0 flex-col overflow-hidden rounded-none sm:h-[min(100%,48rem)] sm:max-w-5xl sm:rounded-3xl">
        <header className="flex min-w-0 items-center justify-between gap-3 px-5 py-3">
          <h2 className="min-w-0 truncate text-lg font-extrabold tracking-tight text-ink">{title}</h2>
          <div className="flex shrink-0 items-center gap-1">
            <button type="button" className="rounded-xl px-3 py-1.5 text-xs font-bold text-ink-2 hover:bg-accent/10 hover:text-ink" onClick={() => termRef.current && void copySelection(termRef.current)}>
              Copy
            </button>
            <button type="button" className="rounded-xl px-3 py-1.5 text-xs font-bold text-ink-2 hover:bg-accent/10 hover:text-ink" onClick={() => termRef.current && void pasteInto(termRef.current)}>
              Paste
            </button>
            <IconButton label="Close" onClick={onClose}>
              <X size={18} />
            </IconButton>
          </div>
        </header>
        <div ref={hostRef} className="min-h-0 flex-1 px-3 pb-3" />
      </div>
    </div>
  );
}

function termTheme() {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string) => css.getPropertyValue(name).trim();
  return {
    background: v("--panel") || "#0b1428",
    foreground: v("--ink") || "#f3f6fc",
    cursor: v("--accent") || "#0b5cff",
    selectionBackground: v("--accent") || "#0b5cff",
  };
}

async function copySelection(term: Terminal) {
  const text = term.getSelection();
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    /* the browser blocked the clipboard */
  }
}

async function pasteInto(term: Terminal) {
  try {
    const text = await navigator.clipboard.readText();
    if (text) term.paste(text);
  } catch {
    /* the browser blocked the clipboard */
  }
}
