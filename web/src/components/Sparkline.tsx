import { useId } from "react";

const W = 100;
const H = 100;

/**
 * A small area chart of recent history. `capacity` fixes the time window so
 * the line grows in from the right instead of rescaling as samples arrive.
 */
export function Sparkline({
  data,
  capacity,
  max,
  color = "var(--accent)",
  height = 56,
}: {
  data: number[];
  capacity: number;
  /** Fixed y-axis top (e.g. 100 for percentages); auto-scales when omitted. */
  max?: number;
  color?: string;
  height?: number;
}) {
  const id = useId().replace(/:/g, "");
  const n = data.length;
  if (n < 2) return <div style={{ height }} />;

  const top = max ?? Math.max(...data, 1) * 1.2;
  const step = W / (capacity - 1);
  const pts = data.map((v, i) => {
    const x = W - (n - 1 - i) * step;
    const y = H - (Math.max(0, Math.min(v, top)) / top) * (H - 8) - 3;
    return [x, y] as const;
  });
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(2)},${y.toFixed(2)}`).join(" ");
  const area = `${line} L${W},${H} L${pts[0][0].toFixed(2)},${H} Z`;

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      style={{ height, width: "100%", display: "block" }}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.35" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${id})`} />
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth="2"
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
