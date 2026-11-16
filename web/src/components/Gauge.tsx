import { useId } from "react";

const R = 52;
const CIRC = 2 * Math.PI * R;

/** A ring gauge with the value in the middle. */
export function Gauge({
  percent,
  label,
  size = 148,
}: {
  percent: number;
  label: string;
  size?: number;
}) {
  const id = useId().replace(/:/g, "");
  const p = Math.max(0, Math.min(100, percent || 0));
  const stroke = p >= 90 ? "var(--bad)" : p >= 75 ? "var(--warn)" : `url(#${id})`;

  return (
    <div
      className="relative"
      style={{ width: size, height: size }}
      role="meter"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(p)}
      aria-label={label}
    >
      <svg viewBox="0 0 120 120" className="h-full w-full -rotate-90">
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="var(--accent)" />
            <stop offset="100%" stopColor="var(--accent-2)" />
          </linearGradient>
        </defs>
        <circle cx="60" cy="60" r={R} fill="none" stroke="var(--track)" strokeWidth="10" />
        <circle
          cx="60"
          cy="60"
          r={R}
          fill="none"
          stroke={stroke}
          strokeWidth="10"
          strokeLinecap="round"
          strokeDasharray={CIRC}
          strokeDashoffset={CIRC * (1 - p / 100)}
          style={{ transition: "stroke-dashoffset 0.7s ease-out, stroke 0.3s" }}
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="text-[2rem] font-extrabold leading-none tabular-nums text-ink">
          {Math.round(p)}
          <span className="text-base font-bold text-ink-3">%</span>
        </span>
        <span className="mt-1.5 text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">
          {label}
        </span>
      </div>
    </div>
  );
}
