import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import type { StreamStatus } from "../lib/useStream";

// Presentational primitives for the "friendly console" look: frosted-glass
// cards with generous radii on top of the accent-tinted backdrop.

export function Card({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return <section className={`glass rounded-3xl p-5 ${className}`}>{children}</section>;
}

export function CardTitle({
  icon: Icon,
  title,
  hint,
}: {
  icon: LucideIcon;
  title: string;
  hint?: ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-2.5">
        <span className="flex h-8 w-8 items-center justify-center rounded-xl bg-accent/12 text-accent">
          <Icon size={16} strokeWidth={2.25} />
        </span>
        <h2 className="text-sm font-bold text-ink">{title}</h2>
      </div>
      {hint && <div className="truncate text-xs font-medium text-ink-3">{hint}</div>}
    </div>
  );
}

/** Rounded progress bar; shifts to amber/red as it fills. */
export function Bar({ percent }: { percent: number }) {
  const p = Math.max(0, Math.min(100, percent || 0));
  const fill = p >= 90 ? "bg-bad" : p >= 75 ? "bg-warn" : "accent-gradient";
  return (
    <div className="h-2.5 w-full overflow-hidden rounded-full bg-track">
      <div
        className={`h-full rounded-full transition-[width] duration-700 ease-out ${fill}`}
        style={{ width: `${Math.max(p, 1.5)}%` }}
      />
    </div>
  );
}

const STATUS: Record<StreamStatus, { label: string; cls: string }> = {
  live: { label: "Online", cls: "text-ok" },
  connecting: { label: "Connecting", cls: "text-warn" },
  offline: { label: "Offline", cls: "text-bad" },
};

export function StatusPill({ status }: { status: StreamStatus }) {
  const s = STATUS[status];
  return (
    <span className="glass inline-flex items-center gap-2 rounded-full px-3.5 py-1.5 text-xs font-bold text-ink-2">
      <span className={`relative flex h-2 w-2 ${s.cls}`}>
        {status === "live" && (
          <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-current opacity-60" />
        )}
        <span className="relative inline-flex h-2 w-2 rounded-full bg-current" />
      </span>
      {s.label}
    </span>
  );
}

/** Gradient squircle holding a white icon — the app-tile motif. */
export function IconBadge({
  icon: Icon,
  size = 48,
  muted = false,
}: {
  icon: LucideIcon;
  size?: number;
  muted?: boolean;
}) {
  return (
    <span
      className={`flex shrink-0 items-center justify-center rounded-2xl text-white shadow-lg shadow-accent/25 ${
        muted ? "bg-ink-3/60 shadow-none" : "accent-gradient"
      }`}
      style={{ width: size, height: size }}
    >
      <Icon size={size * 0.46} strokeWidth={2} />
    </span>
  );
}
