import { useEffect, useRef, useState } from "react";
import type { Stats } from "../types";

export type StreamStatus = "connecting" | "live" | "offline";

/** How many samples of history the dashboard keeps (2s apart ≈ 2 minutes). */
export const HISTORY_SIZE = 60;

/**
 * useStatsStream subscribes to the daemon's /api/v1/system/stream WebSocket and
 * returns the latest sample, a rolling history for graphs, and connection
 * status. It auto-reconnects with a short backoff so the dashboard recovers if
 * the daemon restarts.
 */
export function useStatsStream(): {
  stats: Stats | null;
  history: Stats[];
  status: StreamStatus;
} {
  const [history, setHistory] = useState<Stats[]>([]);
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const retry = useRef<number>(0);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let closed = false;

    const connect = () => {
      const proto = location.protocol === "https:" ? "wss" : "ws";
      ws = new WebSocket(`${proto}://${location.host}/api/v1/system/stream`);

      ws.onopen = () => {
        retry.current = 0;
        setStatus("live");
      };
      ws.onmessage = (ev) => {
        try {
          const sample = JSON.parse(ev.data) as Stats;
          // The daemon replays its history window on every (re)connect, so
          // skip anything we already hold.
          setHistory((h) =>
            h.length && sample.timestamp <= h[h.length - 1].timestamp
              ? h
              : [...h.slice(-(HISTORY_SIZE - 1)), sample],
          );
        } catch {
          /* ignore malformed frame */
        }
      };
      ws.onclose = () => {
        if (closed) return;
        setStatus("offline");
        const delay = Math.min(1000 * 2 ** retry.current, 10000);
        retry.current += 1;
        timer = setTimeout(connect, delay);
      };
      ws.onerror = () => ws?.close();
    };

    connect();
    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      ws?.close();
    };
  }, []);

  const stats = history.length ? history[history.length - 1] : null;
  return { stats, history, status };
}
