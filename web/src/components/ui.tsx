import type { ReactNode } from "react";

// Lightweight presentational primitives. These keep the dashboard consistent
// without pulling in a component library yet; shadcn/ui can replace them later.

export function Card({
  title,
  icon,
  children,
  right,
}: {
  title?: string;
  icon?: ReactNode;
  children: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div className="rounded-xl border border-white/5 bg-[#111823] shadow-lg shadow-black/20">
      {title && (
        <div className="flex items-center justify-between border-b border-white/5 px-4 py-3">
          <div className="flex items-center gap-2 text-sm font-medium text-slate-200">
            {icon}
            {title}
          </div>
          {right}
        </div>
      )}
      <div className="p-4">{children}</div>
    </div>
  );
}

function barColor(p: number): string {
  if (p >= 90) return "bg-rose-500";
  if (p >= 70) return "bg-amber-400";
  return "bg-emerald-400";
}

export function Meter({ percent }: { percent: number }) {
  const p = Math.max(0, Math.min(100, percent || 0));
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-white/5">
      <div
        className={`h-full rounded-full transition-all duration-500 ${barColor(p)}`}
        style={{ width: `${p}%` }}
      />
    </div>
  );
}

export function StatTile({
  label,
  value,
  sub,
  percent,
  icon,
}: {
  label: string;
  value: string;
  sub?: string;
  percent?: number;
  icon?: ReactNode;
}) {
  return (
    <div className="rounded-xl border border-white/5 bg-[#111823] p-4">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium uppercase tracking-wide text-slate-400">
          {label}
        </span>
        <span className="text-slate-500">{icon}</span>
      </div>
      <div className="mt-2 text-2xl font-semibold text-slate-100">{value}</div>
      {sub && <div className="mt-0.5 text-xs text-slate-400">{sub}</div>}
      {percent !== undefined && (
        <div className="mt-3">
          <Meter percent={percent} />
        </div>
      )}
    </div>
  );
}
