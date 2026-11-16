import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, api, errorMessage } from "../../lib/api";
import { Alert, Button } from "../form";

export interface InstallJob {
  id: string;
  name: string;
  label: string;
  state: "queued" | "running" | "succeeded" | "failed";
  step: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  error?: string;
  log: string[];
}

const STATE: Record<InstallJob["state"], { label: string; cls: string }> = {
  queued: { label: "Queued", cls: "bg-track text-ink-2" },
  running: { label: "Running", cls: "bg-accent/15 text-accent" },
  succeeded: { label: "Installed", cls: "bg-ok/15 text-ok" },
  failed: { label: "Failed", cls: "bg-bad/12 text-bad" },
};

function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) ms = 0;
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

/** True when the panel should stay on screen. A finished success is kept briefly so a refresh still shows it. */
export function showInstall(job: InstallJob | null, keepSuccess = true): boolean {
  if (!job) return false;
  if (job.state === "queued" || job.state === "running" || job.state === "failed") return true;
  if (!keepSuccess || job.state !== "succeeded" || !job.finishedAt) return false;
  const at = Date.parse(job.finishedAt);
  return Number.isFinite(at) && Date.now() - at < 2 * 60 * 1000;
}

/**
 * useInstall resumes whatever job the server still has for name, and polls it
 * while it is queued or running. onDone runs once when a job this page was
 * watching reaches succeeded.
 */
export function useInstall(name: string, onDone?: () => void) {
  const [job, setJob] = useState<InstallJob | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const onDoneRef = useRef(onDone);
  onDoneRef.current = onDone;
  const watched = useRef(false);
  const reported = useRef<string | null>(null);

  useEffect(() => {
    let gone = false;
    api<{ job: InstallJob | null }>(`/installs?name=${encodeURIComponent(name)}`)
      .then((res) => {
        if (!gone) setJob(res.job);
      })
      .catch(() => {});
    return () => {
      gone = true;
    };
  }, [name]);

  useEffect(() => {
    if (!job || (job.state !== "queued" && job.state !== "running")) return;
    let gone = false;
    const tick = () => {
      api<InstallJob>(`/installs/${job.id}`)
        .then((next) => {
          if (!gone) setJob(next);
        })
        .catch((e) => {
          if (!gone && e instanceof ApiError && e.status === 404) setJob(null);
        });
    };
    tick();
    const timer = setInterval(tick, 700);
    return () => {
      gone = true;
      clearInterval(timer);
    };
  }, [job?.id, job?.state]);

  useEffect(() => {
    if (!job) return;
    if (job.state === "queued" || job.state === "running") watched.current = true;
    if (job.state === "succeeded" && watched.current && reported.current !== job.id) {
      reported.current = job.id;
      onDoneRef.current?.();
    }
  }, [job]);

  const start = useCallback(async () => {
    setStarting(true);
    setError(null);
    try {
      const next = await api<InstallJob>("/installs", { method: "POST", body: { name } });
      watched.current = true;
      setJob(next);
      return next;
    } catch (e) {
      const msg = errorMessage(e);
      setError(msg);
      throw e;
    } finally {
      setStarting(false);
    }
  }, [name]);

  const active = !!job && (job.state === "queued" || job.state === "running");
  return { job, active, starting, error, start };
}

export function InstallPanel({
  job,
  onRetry,
  busy = false,
}: {
  job: InstallJob;
  onRetry?: () => void;
  busy?: boolean;
}) {
  const ticking = job.state === "queued" || job.state === "running";
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [ticking]);

  const startAt = Date.parse(job.startedAt || job.createdAt);
  const endAt = job.finishedAt ? Date.parse(job.finishedAt) : now;
  const elapsed = formatElapsed(endAt - startAt);
  const tone = STATE[job.state] ?? STATE.queued;
  const logRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [job.log]);

  const lines = job.log ?? [];

  return (
    <div className="w-full space-y-3" aria-busy={ticking}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[11px] font-bold uppercase tracking-wider text-ink-3">Install progress</p>
          <p className="truncate text-sm font-extrabold text-ink">{job.step || job.label}</p>
        </div>
        <div className="flex items-center gap-2">
          <span className={`rounded-full px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${tone.cls}`}>
            {tone.label}
          </span>
          <span className="font-mono text-xs font-bold tabular-nums text-ink-2" aria-label="Elapsed time">
            {elapsed}
          </span>
        </div>
      </div>
      <pre
        ref={logRef}
        className="max-h-52 overflow-auto rounded-2xl bg-[#07101f] px-4 py-3 text-left font-mono text-[11px] leading-5 whitespace-pre-wrap break-words text-[#d5deef]"
      >
        {lines.length ? lines.join("\n") : ticking ? "Waiting for output…" : ""}
      </pre>
      {job.state === "succeeded" && <Alert tone="info">{job.label} is installed.</Alert>}
      {job.state === "failed" && (
        <>
          <Alert>{job.error || "The install failed."}</Alert>
          {onRetry && (
            <Button variant="primary" onClick={onRetry} loading={busy}>
              Retry
            </Button>
          )}
        </>
      )}
    </div>
  );
}
