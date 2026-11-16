import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, IconButton, Modal } from "../form";
import type { Container } from "./types";

/** Shows the tail of a container's logs, refreshed every few seconds. */
export default function LogsDialog({
  container,
  onClose,
}: {
  container: Container;
  onClose: () => void;
}) {
  const [logs, setLogs] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [follow, setFollow] = useState(true);
  const pre = useRef<HTMLPreElement>(null);

  const load = useCallback(async () => {
    try {
      const res = await api<{ logs: string }>(
        `/docker/containers/${encodeURIComponent(container.id)}/logs?tail=500`,
      );
      setLogs(res.logs);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }, [container.id]);

  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
  }, [load]);

  // Keep the newest lines in view unless the reader has scrolled up.
  useEffect(() => {
    if (follow && pre.current) pre.current.scrollTop = pre.current.scrollHeight;
  }, [logs, follow]);

  const onScroll = () => {
    const el = pre.current;
    if (!el) return;
    setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40);
  };

  return (
    <Modal
      title={`Logs · ${container.name}`}
      onClose={onClose}
      footer={
        <span className="text-xs font-medium text-ink-3">
          {follow ? "Following new output" : "Scroll to the bottom to follow"}
        </span>
      }
    >
      <div className="relative">
        <div className="absolute right-0 top-0 z-10">
          <IconButton label="Refresh now" onClick={() => void load()}>
            <RefreshCw size={15} className={loading ? "animate-spin" : ""} />
          </IconButton>
        </div>
        {error && (
          <div className="mb-3">
            <Alert>{error}</Alert>
          </div>
        )}
        <pre
          ref={pre}
          onScroll={onScroll}
          className="h-[52vh] overflow-auto rounded-xl border border-line bg-[#05070d] p-4 font-mono text-xs leading-relaxed text-slate-200"
        >
          {logs || (loading ? "Loading…" : "This container has no output yet.")}
        </pre>
      </div>
    </Modal>
  );
}
