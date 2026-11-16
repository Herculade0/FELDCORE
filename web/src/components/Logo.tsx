import { useId } from "react";

/** The FELDCORE mark: a hexagonal shell around a bright core. */
export function LogoMark({ size = 36 }: { size?: number }) {
  const id = useId().replace(/:/g, "");
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="var(--accent)" />
          <stop offset="100%" stopColor="var(--accent-2)" />
        </linearGradient>
      </defs>
      <polygon
        points="16,1.5 28.6,8.75 28.6,23.25 16,30.5 3.4,23.25 3.4,8.75"
        fill={`url(#${id})`}
      />
      <polygon
        points="16,6 24.66,11 24.66,21 16,26 7.34,21 7.34,11"
        fill="none"
        stroke="#fff"
        strokeOpacity="0.45"
        strokeWidth="1"
      />
      <polygon points="16,10.5 20.76,13.25 20.76,18.75 16,21.5 11.24,18.75 11.24,13.25" fill="#fff" />
    </svg>
  );
}

export function Wordmark() {
  return (
    <div className="leading-none">
      <div className="text-[15px] font-extrabold tracking-[0.22em] text-ink">FELDCORE</div>
      <div className="mt-1.5 text-[8.5px] font-semibold tracking-[0.32em] text-ink-3">
        A FELDCORP SYSTEM
      </div>
    </div>
  );
}
