import { useEffect, useRef, useState } from "react";
import type { Stats } from "../types";

type Status = "connecting" | "live" | "offline";

/**
 * useStatsStream subscribes to the daemon's /api/v1/system/stream WebSocket and
 * returns the latest sample plus connection status. It auto-reconnects with a
 * short backoff so the dashboard recovers if the daemon restarts.
 */
export function useStatsStream(): { stats: Stats | null; status: Status } {
  const [stats, setStats] = useState<Stats | null>(null);
  const [status, setStatus] = useState<Status>("connecting");
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
          setStats(JSON.parse(ev.data) as Stats);
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

  return { stats, status };
}
