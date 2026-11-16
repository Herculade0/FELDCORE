import { useCallback, useEffect, useState } from "react";
import { api, errorMessage } from "../../lib/api";
import type { Job, VirtioView } from "./types";

export function useVirtio() {
  const [view, setView] = useState<VirtioView | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setView(await api<VirtioView>("/vms/virtio"));
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!job || job.finished) return;
    const t = setInterval(() => {
      void api<Job>(`/vms/jobs/${encodeURIComponent(job.id)}`)
        .then((next) => {
          setJob(next);
          if (next.finished) void load();
        })
        .catch((e) => setError(errorMessage(e)));
    }, 700);
    return () => clearInterval(t);
  }, [job, load]);

  const download = async (channel: string) => {
    setError(null);
    try {
      setJob(await api<Job>("/vms/virtio", { method: "POST", body: { channel } }));
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  const remove = async (name: string) => {
    setError(null);
    try {
      await api(`/vms/virtio/${encodeURIComponent(name)}`, { method: "DELETE" });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  return { view, job, error, load, download, remove };
}
