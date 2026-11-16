import { useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import type { Container } from "./types";

const KEY = "feldcore.docker.allocations";

function loadOpen(): boolean {
  try {
    return localStorage.getItem(KEY) !== "closed";
  } catch {
    return true;
  }
}

/**
 * Which network, address and host ports each container holds, so a clash is
 * easy to spot before picking a port for the next one.
 */
export default function Allocations({ containers }: { containers: Container[] }) {
  const [open, setOpen] = useState(loadOpen);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    try {
      localStorage.setItem(KEY, next ? "open" : "closed");
    } catch {
      /* preference just won't persist */
    }
  };

  const rows = [...containers].sort((a, b) => a.name.localeCompare(b.name));
  const Chevron = open ? ChevronUp : ChevronDown;

  return (
    <section>
      <button
        onClick={toggle}
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded-lg px-1 py-1 text-sm font-bold text-accent hover:underline"
      >
        <Chevron size={16} strokeWidth={2.5} />
        {open ? "Hide" : "Show"} docker allocations
      </button>

      {open && (
        <div className="glass mt-3 overflow-x-auto rounded-3xl">
          <table className="w-full min-w-[34rem] text-left text-sm">
            <thead>
              <tr className="border-b border-line text-[10px] font-bold uppercase tracking-[0.16em] text-ink-3">
                <th className="px-5 py-3 font-bold">Container</th>
                <th className="px-3 py-3 font-bold">Network</th>
                <th className="px-3 py-3 font-bold">Address</th>
                <th className="px-5 py-3 font-bold">Host ports</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((c) => {
                const ports = c.ports ?? [];
                const ips = c.ips ?? [];
                return (
                  <tr key={c.id} className="border-b border-line last:border-0">
                    <td className="px-5 py-2.5 font-bold text-ink">{c.name}</td>
                    <td className="px-3 py-2.5 font-medium text-ink-2">{c.network || "—"}</td>
                    <td className="px-3 py-2.5 font-mono text-xs text-ink-2">
                      {ips.length ? ips.join("  ") : "—"}
                    </td>
                    <td className="px-5 py-2.5 font-mono text-xs text-ink-2">
                      {ports.length
                        ? ports.map((p) => (p.proto === "tcp" ? p.host : `${p.host}/${p.proto}`)).join(", ")
                        : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
