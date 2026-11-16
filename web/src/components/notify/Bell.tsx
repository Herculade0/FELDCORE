import { useEffect, useRef, useState } from "react";
import { Bell as BellIcon } from "lucide-react";
import { api } from "../../lib/api";
import { IconButton } from "../form";

interface Note {
  id: string;
  name: string;
  severity: string;
  title: string;
  body: string;
  at: string;
  read: boolean;
}

interface Inbox {
  unread: number;
  items: Note[];
}

export default function Bell() {
  const [inbox, setInbox] = useState<Inbox>({ unread: 0, items: [] });
  const [open, setOpen] = useState(false);
  const [box, setBox] = useState({ top: 0, left: 0 });
  const root = useRef<HTMLDivElement>(null);

  const load = () => {
    api<Inbox>("/notifications/inbox")
      .then((n) => setInbox({ unread: n.unread ?? 0, items: n.items ?? [] }))
      .catch(() => undefined);
  };

  useEffect(() => {
    load();
    const t = setInterval(load, 20_000);
    return () => clearInterval(t);
  }, []);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  const place = () => {
    const r = root.current?.getBoundingClientRect();
    if (!r) return;
    const width = 320;
    // The desktop rail is a fixed card on the left. Open the list beside it
    // so it does not cover the navigation. On a phone the bell is in the header.
    let left = r.left < 300 ? 292 : r.left;
    if (left + width > window.innerWidth - 12) left = Math.max(12, window.innerWidth - width - 12);
    let top = r.bottom + 8;
    if (top + 360 > window.innerHeight) top = Math.max(12, r.top - 368);
    setBox({ top, left });
  };

  const mark = (all: boolean, ids: string[]) => {
    api<Inbox>("/notifications/inbox/read", { method: "POST", body: { all, ids } })
      .then((n) => setInbox({ unread: n.unread ?? 0, items: n.items ?? [] }))
      .catch(() => undefined);
  };

  const count = inbox.unread;

  return (
    <div ref={root} className="relative">
      <IconButton
        label={count ? `${count} unread notifications` : "Notifications"}
        onClick={() => {
          place();
          setOpen((v) => !v);
          load();
        }}
      >
        <BellIcon size={17} />
      </IconButton>
      {count > 0 && (
        <span className="pointer-events-none absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-bad px-1 text-[9px] font-bold text-white">
          {count > 99 ? "99+" : count}
        </span>
      )}
      {open && (
        <div
          className="panel fixed z-40 w-80 rounded-2xl p-3"
          style={{ top: box.top, left: box.left }}
        >
          <div className="mb-2 flex items-center justify-between gap-2">
            <div className="text-xs font-bold text-ink">Notifications</div>
            <button
              type="button"
              className="text-[11px] font-bold text-accent disabled:opacity-40"
              disabled={count === 0}
              onClick={() => mark(true, [])}
            >
              Mark all read
            </button>
          </div>
          {inbox.items.length === 0 ? (
            <p className="py-6 text-center text-xs font-medium text-ink-3">Nothing yet.</p>
          ) : (
            <ul className="max-h-80 space-y-2 overflow-auto">
              {inbox.items.map((n) => (
                <li key={n.id}>
                  <button
                    type="button"
                    className={`w-full rounded-xl px-2.5 py-2 text-left ${n.read ? "opacity-70" : "bg-accent/8"}`}
                    onClick={() => {
                      if (!n.read) mark(false, [n.id]);
                    }}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className={`text-[10px] font-bold uppercase tracking-wider ${tone(n.severity)}`}>
                        {n.severity}
                      </span>
                      <span className="text-[10px] font-semibold text-ink-3">{when(n.at)}</span>
                    </div>
                    <div className="mt-1 text-xs font-bold text-ink">{n.title}</div>
                    {n.body && <p className="mt-0.5 text-[11px] font-medium leading-relaxed text-ink-2">{n.body}</p>}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

function tone(severity: string): string {
  if (severity === "critical") return "text-bad";
  if (severity === "warning") return "text-warn";
  return "text-ink-3";
}

function when(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
