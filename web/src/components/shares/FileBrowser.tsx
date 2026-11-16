import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  ChevronRight,
  Copy,
  Download,
  Eye,
  File,
  Folder,
  FolderInput,
  FolderPlus,
  FolderUp,
  Grid2x2,
  Link2,
  List,
  Pencil,
  RotateCcw,
  Search,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes, bytesPerSec } from "../../lib/format";
import { Card } from "../ui";
import { Alert, Button, IconButton, Modal, TextField, Toggle, inputClass } from "../form";
import { PreviewModal, downloadFile, downloadZip } from "./preview";
import {
  incomingFromDrop,
  incomingFromFiles,
  startUpload,
  type ConflictAction,
  type Incoming,
  type UploadSnapshot,
} from "./upload";

interface Entry {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  modTime: string;
  link?: boolean;
  broken?: boolean;
}

interface Listing {
  path: string;
  parent?: string;
  entries: Entry[];
  trash: boolean;
  truncated?: boolean;
}

interface FetchJob {
  id: string;
  name: string;
  path: string;
  url: string;
  status: string;
  bytes: number;
  total: number;
  speed: number;
  error?: string;
  skipped?: boolean;
}

type SortKey = "name" | "size" | "date";
type View = "list" | "grid";

const VIEW_KEY = "feldcore.files.view";

function clean(path: string): string {
  if (!path || path === ".") return "";
  return path;
}

function when(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function loadView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === "grid" ? "grid" : "list";
  } catch {
    return "list";
  }
}

export default function FileBrowser({ name, onBack }: { name: string; onBack: () => void }) {
  const [path, setPath] = useState("");
  const [listing, setListing] = useState<Listing | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("name");
  const [sortAsc, setSortAsc] = useState(true);
  const [view, setView] = useState<View>(loadView);
  const [selected, setSelected] = useState<string[]>([]);
  const [anchor, setAnchor] = useState<string | null>(null);
  const [stamp, setStamp] = useState(0);
  const [preview, setPreview] = useState<Entry | null>(null);
  const [dragging, setDragging] = useState(false);
  const [uploads, setUploads] = useState<UploadSnapshot | null>(null);
  const [conflict, setConflict] = useState<{
    path: string;
    name: string;
    resolve: (answer: { action: ConflictAction; all: boolean } | "cancel") => void;
  } | null>(null);
  const [applyAll, setApplyAll] = useState(false);
  const [mkdirOpen, setMkdirOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [transfer, setTransfer] = useState<"move" | "copy" | null>(null);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [fetchOpen, setFetchOpen] = useState(false);
  const [jobs, setJobs] = useState<FetchJob[]>([]);
  const cancelRef = useRef<(() => void) | null>(null);
  const beginRef = useRef<(incoming: Incoming) => void>(() => {});

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const next = await api<Listing>(
        `/shares/${encodeURIComponent(name)}/files?path=${encodeURIComponent(path)}`,
      );
      setListing(next);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }, [name, path]);

  useEffect(() => {
    setSelected([]);
    setQuery("");
    void load();
  }, [load]);

  const refreshJobs = useCallback(async () => {
    try {
      const res = await api<{ jobs: FetchJob[] }>(`/shares/${encodeURIComponent(name)}/fetch`);
      setJobs(res.jobs ?? []);
    } catch {
      /* the file list error is enough */
    }
  }, [name]);

  useEffect(() => {
    void refreshJobs();
  }, [refreshJobs]);

  const jobsLive = jobs.some((job) => job.status === "queued" || job.status === "running");
  useEffect(() => {
    if (!jobsLive) return;
    const t = setInterval(() => void refreshJobs(), 1000);
    return () => clearInterval(t);
  }, [jobsLive, refreshJobs]);

  useEffect(() => () => cancelRef.current?.(), []);

  useEffect(() => {
    let depth = 0;
    const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");
    const hide = () => {
      depth = 0;
      setDragging(false);
    };
    const enter = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      depth += 1;
      setDragging(true);
    };
    const over = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
    };
    const leave = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      // Leaving the window ends the gesture. Moving between children does not.
      if (!e.relatedTarget) hide();
      else {
        depth = Math.max(0, depth - 1);
        if (depth === 0) setDragging(false);
      }
    };
    const drop = (e: DragEvent) => {
      e.preventDefault();
      const transfer = e.dataTransfer;
      hide();
      if (!transfer || !hasFiles(e)) return;
      void incomingFromDrop(transfer).then((incoming) => beginRef.current(incoming));
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragover", over);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", drop);
    window.addEventListener("dragend", hide);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragover", over);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", drop);
      window.removeEventListener("dragend", hide);
    };
  }, []);

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    const items = (listing?.entries ?? []).filter((entry) => !q || entry.name.toLowerCase().includes(q));
    const dir = sortAsc ? 1 : -1;
    items.sort((a, b) => {
      if (a.dir !== b.dir) return a.dir ? -1 : 1;
      if (sortKey === "size") return (a.size - b.size) * dir;
      if (sortKey === "date") return (new Date(a.modTime).getTime() - new Date(b.modTime).getTime()) * dir;
      return a.name.localeCompare(b.name, undefined, { numeric: true }) * dir;
    });
    return items;
  }, [listing, query, sortKey, sortAsc]);

  const chosen = rows.filter((entry) => selected.includes(entry.path));
  const one = chosen.length === 1 ? chosen[0] : null;
  const inTrash = !!listing?.trash;

  const openPath = (next: string) => setPath(clean(next));

  const pick = (entry: Entry, event: { metaKey: boolean; ctrlKey: boolean; shiftKey: boolean }) => {
    if (event.shiftKey && anchor) {
      const from = rows.findIndex((row) => row.path === anchor);
      const to = rows.findIndex((row) => row.path === entry.path);
      if (from >= 0 && to >= 0) {
        const [start, end] = from < to ? [from, to] : [to, from];
        setSelected(rows.slice(start, end + 1).map((row) => row.path));
        return;
      }
    }
    if (event.metaKey || event.ctrlKey) {
      setSelected((cur) => (cur.includes(entry.path) ? cur.filter((p) => p !== entry.path) : [...cur, entry.path]));
    } else {
      setSelected([entry.path]);
    }
    setAnchor(entry.path);
  };

  const openEntry = (entry: Entry) => {
    if (entry.dir) openPath(entry.path);
    else setPreview(entry);
  };

  const changed = async () => {
    setStamp((n) => n + 1);
    await load();
  };

  const begin = (incoming: Incoming) => {
    if (incoming.files.length === 0 && incoming.dirs.length === 0) {
      setError(incoming.rejected ? "Those paths stay inside this share, so nothing was uploaded." : "Drop a file or a folder.");
      return;
    }
    if (incoming.rejected) setError("Some paths were left out because they would leave this share.");
    cancelRef.current?.();
    const handle = startUpload({
      share: name,
      dest: path,
      incoming,
      onConflict: (file) =>
        new Promise((resolve) => {
          setApplyAll(false);
          setConflict({ ...file, resolve });
        }),
      onUpdate: (snap) => {
        setUploads(snap);
        if (!snap.active) void changed();
      },
    });
    cancelRef.current = handle.cancel;
  };
  beginRef.current = begin;

  const chooseFiles = (directory: boolean) => {
    const input = document.createElement("input");
    input.type = "file";
    input.multiple = true;
    if (directory) {
      input.setAttribute("webkitdirectory", "");
      input.setAttribute("directory", "");
    }
    input.onchange = () => {
      if (input.files && input.files.length > 0) begin(incomingFromFiles(input.files));
    };
    input.click();
  };

  const setMode = (next: View) => {
    setView(next);
    try {
      localStorage.setItem(VIEW_KEY, next);
    } catch {
      /* private mode */
    }
  };

  const crumbs = [{ label: name, path: "" }];
  if (path) {
    const parts = path.split("/");
    parts.forEach((part, i) => crumbs.push({ label: part, path: parts.slice(0, i + 1).join("/") }));
  }

  const answerConflict = (answer: { action: ConflictAction; all: boolean } | "cancel") => {
    conflict?.resolve(answer);
    setConflict(null);
  };

  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-center gap-3">
        <IconButton label="Back to shares" onClick={onBack}>
          <ArrowLeft size={18} />
        </IconButton>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-2xl font-extrabold tracking-tight text-ink sm:text-3xl">{name}</h1>
          <nav className="mt-1 flex flex-wrap items-center gap-1 text-sm font-semibold text-ink-2" aria-label="Folder">
            {crumbs.map((crumb, i) => (
              <span key={crumb.path || "root"} className="flex items-center gap-1">
                {i > 0 && <ChevronRight size={14} className="text-ink-3" />}
                <button type="button" className="rounded-lg px-1.5 py-0.5 hover:bg-accent/10 hover:text-accent" onClick={() => openPath(crumb.path)}>
                  {crumb.label}
                </button>
              </span>
            ))}
          </nav>
        </div>
      </header>

      {error && <Alert>{error}</Alert>}

      <Card className="!p-3">
        <div className="flex flex-wrap items-center gap-2">
          <label className="relative min-w-40 flex-1">
            <Search size={15} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ink-3" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter this folder"
              aria-label="Filter this folder"
              className={`${inputClass} pl-9`}
            />
          </label>
          <select
            aria-label="Sort by"
            value={`${sortKey}-${sortAsc ? "asc" : "desc"}`}
            onChange={(e) => {
              const [key, dir] = e.target.value.split("-");
              setSortKey(key as SortKey);
              setSortAsc(dir === "asc");
            }}
            className={`${inputClass} w-auto`}
          >
            <option value="name-asc">Name</option>
            <option value="name-desc">Name, Z to A</option>
            <option value="size-desc">Size, largest</option>
            <option value="size-asc">Size, smallest</option>
            <option value="date-desc">Newest</option>
            <option value="date-asc">Oldest</option>
          </select>
          <div className="flex rounded-xl border border-line p-0.5">
            <IconButton label="List" onClick={() => setMode("list")} className={view === "list" ? "bg-accent/10 text-accent" : ""}>
              <List size={16} />
            </IconButton>
            <IconButton label="Grid" onClick={() => setMode("grid")} className={view === "grid" ? "bg-accent/10 text-accent" : ""}>
              <Grid2x2 size={16} />
            </IconButton>
          </div>
          <Button size="sm" onClick={() => setMkdirOpen(true)}>
            <FolderPlus size={14} /> New folder
          </Button>
          <Button size="sm" onClick={() => chooseFiles(false)}>
            <Upload size={14} /> Upload
          </Button>
          <Button
            size="sm"
            title="Drag a folder onto the page to keep empty folders too. This picker includes every file and the folders that contain one."
            onClick={() => chooseFiles(true)}
          >
            <FolderUp size={14} /> Upload folder
          </Button>
          <Button size="sm" onClick={() => setFetchOpen(true)}>
            <Link2 size={14} /> From URL
          </Button>
          <Button size="sm" variant="ghost" onClick={() => openPath(".trash")}>
            <Trash2 size={14} /> Trash
          </Button>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button size="sm" variant="ghost" disabled={!one || one.dir} onClick={() => one && setPreview(one)}>
            <Eye size={14} /> Preview
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={chosen.length === 0}
            onClick={() => {
              if (chosen.length === 1 && !chosen[0].dir) downloadFile(name, chosen[0].path);
              else downloadZip(name, chosen.map((entry) => entry.path));
            }}
          >
            <Download size={14} /> Download
          </Button>
          <Button size="sm" variant="ghost" disabled={!one} onClick={() => setRenameOpen(true)}>
            <Pencil size={14} /> Rename
          </Button>
          <Button size="sm" variant="ghost" disabled={chosen.length === 0} onClick={() => setTransfer("move")}>
            <FolderInput size={14} /> Move
          </Button>
          <Button size="sm" variant="ghost" disabled={chosen.length === 0} onClick={() => setTransfer("copy")}>
            <Copy size={14} /> Copy
          </Button>
          {inTrash && (
            <Button
              size="sm"
              variant="ghost"
              disabled={chosen.length === 0}
              onClick={() =>
                void api(`/shares/${encodeURIComponent(name)}/files/move`, {
                  method: "POST",
                  body: { paths: chosen.map((entry) => entry.path), dest: "", conflict: "rename" },
                })
                  .then(() => changed())
                  .catch((e) => setError(errorMessage(e)))
              }
            >
              <RotateCcw size={14} /> Restore
            </Button>
          )}
          <Button size="sm" variant="danger" disabled={chosen.length === 0} onClick={() => setRemoveOpen(true)}>
            <Trash2 size={14} /> {inTrash ? "Delete" : "Trash"}
          </Button>
          {path === ".trash" && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setSelected([]);
                setRemoveOpen(true);
              }}
            >
              Empty trash
            </Button>
          )}
        </div>
      </Card>

      {uploads && <UploadPanel snap={uploads} onStop={() => cancelRef.current?.()} onDismiss={() => setUploads(null)} />}
      {jobs.length > 0 && (
        <FetchPanel
          jobs={jobs}
          onCancel={async (id) => {
            try {
              await api(`/shares/${encodeURIComponent(name)}/fetch/${encodeURIComponent(id)}`, { method: "DELETE" });
              await refreshJobs();
              await changed();
            } catch (e) {
              setError(errorMessage(e));
            }
          }}
        />
      )}

      <div className="grid items-start gap-4 md:grid-cols-[15rem_1fr]">
        <Card className="hidden max-h-[70vh] overflow-auto md:block">
          <FolderTree share={name} current={path} stamp={stamp} onOpen={openPath} />
        </Card>
        <Card className="min-h-80">
          {loading && !listing ? (
            <p className="py-10 text-center text-sm font-medium text-ink-3">Loading…</p>
          ) : rows.length === 0 ? (
            <div className="flex flex-col items-center px-6 py-14 text-center">
              <Folder size={28} className="text-accent" />
              <p className="mt-3 text-sm font-bold text-ink">{query ? "Nothing matches" : "This folder is empty"}</p>
              <p className="mt-1 max-w-sm text-xs font-medium text-ink-3">
                Drop files or whole folders here. Empty folders are kept.
              </p>
            </div>
          ) : view === "list" ? (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="text-[11px] font-bold uppercase tracking-wider text-ink-3">
                    <th className="w-8 pb-2" />
                    <th className="pb-2 font-bold">Name</th>
                    <th className="pb-2 font-bold">Size</th>
                    <th className="pb-2 font-bold">Modified</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((entry) => {
                    const on = selected.includes(entry.path);
                    return (
                      <tr
                        key={entry.path}
                        onClick={(e) => pick(entry, e)}
                        onDoubleClick={() => openEntry(entry)}
                        className={`cursor-pointer border-t border-line ${on ? "bg-accent/10" : "hover:bg-accent/5"}`}
                      >
                        <td className="py-2">
                          <input
                            type="checkbox"
                            checked={on}
                            aria-label={`Select ${entry.name}`}
                            onChange={() => pick(entry, { metaKey: true, ctrlKey: true, shiftKey: false })}
                            onClick={(e) => e.stopPropagation()}
                          />
                        </td>
                        <td className="max-w-xs truncate py-2 font-semibold text-ink">
                          <span className="inline-flex items-center gap-2">
                            {entry.dir ? <Folder size={15} className="text-accent" /> : <File size={15} className="text-ink-3" />}
                            {entry.name}
                            {entry.broken && <span className="text-[10px] font-bold text-bad">broken link</span>}
                          </span>
                        </td>
                        <td className="whitespace-nowrap py-2 text-ink-2">{entry.dir ? "—" : bytes(entry.size)}</td>
                        <td className="whitespace-nowrap py-2 text-ink-2">{when(entry.modTime)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-4">
              {rows.map((entry) => {
                const on = selected.includes(entry.path);
                return (
                  <button
                    key={entry.path}
                    type="button"
                    onClick={(e) => pick(entry, e)}
                    onDoubleClick={() => openEntry(entry)}
                    className={`rounded-2xl border px-3 py-3 text-left ${on ? "border-accent bg-accent/10" : "border-line hover:border-accent/50"}`}
                  >
                    {entry.dir ? <Folder size={22} className="text-accent" /> : <File size={22} className="text-ink-3" />}
                    <div className="mt-2 truncate text-sm font-bold text-ink">{entry.name}</div>
                    <div className="truncate text-[11px] font-medium text-ink-3">
                      {entry.dir ? "Folder" : bytes(entry.size)} · {when(entry.modTime)}
                    </div>
                  </button>
                );
              })}
            </div>
          )}
          {listing?.truncated && (
            <p className="mt-3 text-xs font-semibold text-ink-3">This folder has more than the list can show. Filter to narrow it.</p>
          )}
        </Card>
      </div>

      {dragging && (
        <div className="pointer-events-none fixed inset-0 z-40 flex items-center justify-center bg-bg/80 p-6 backdrop-blur-sm">
          <div className="w-full max-w-lg rounded-3xl border-2 border-dashed border-accent bg-surface px-8 py-14 text-center shadow-2xl">
            <FolderUp size={36} className="mx-auto text-accent" />
            <p className="mt-4 text-xl font-extrabold text-ink">Drop files and folders</p>
            <p className="mt-2 text-sm font-medium text-ink-2">
              The whole directory tree is recreated in {path ? path : name}, including empty folders.
            </p>
          </div>
        </div>
      )}

      {preview && !preview.dir && (
        <PreviewModal share={name} path={preview.path} name={preview.name} size={preview.size} onClose={() => setPreview(null)} />
      )}

      {conflict && (
        <Modal title="Already in this folder" onClose={() => answerConflict("cancel")}>
          <div className="space-y-4">
            <p className="text-sm font-medium leading-relaxed text-ink-2">
              <span className="font-bold text-ink">{conflict.name}</span> is already here. Choose what to do with{" "}
              <span className="break-all font-mono text-xs text-ink">{conflict.path}</span>.
            </p>
            <label className="flex items-center gap-2 text-sm font-semibold text-ink">
              <input type="checkbox" checked={applyAll} onChange={(e) => setApplyAll(e.target.checked)} />
              Apply to all
            </label>
            <div className="flex flex-wrap justify-end gap-2">
              <Button onClick={() => answerConflict("cancel")}>Cancel</Button>
              <Button onClick={() => answerConflict({ action: "skip", all: applyAll })}>Skip</Button>
              <Button onClick={() => answerConflict({ action: "rename", all: applyAll })}>Rename</Button>
              <Button variant="primary" onClick={() => answerConflict({ action: "overwrite", all: applyAll })}>
                Overwrite
              </Button>
            </div>
          </div>
        </Modal>
      )}

      {mkdirOpen && (
        <NameDialog
          title="New folder"
          label="Folder name"
          confirm="Create"
          onClose={() => setMkdirOpen(false)}
          onSave={async (value) => {
            const next = path ? `${path}/${value}` : value;
            await api(`/shares/${encodeURIComponent(name)}/files/mkdir`, { method: "POST", body: { path: next } });
            setMkdirOpen(false);
            await changed();
          }}
        />
      )}
      {renameOpen && one && (
        <NameDialog
          title={`Rename ${one.name}`}
          label="New name"
          initial={one.name}
          confirm="Rename"
          onClose={() => setRenameOpen(false)}
          onSave={async (value) => {
            await api(`/shares/${encodeURIComponent(name)}/files/rename`, {
              method: "POST",
              body: { path: one.path, name: value },
            });
            setRenameOpen(false);
            setSelected([]);
            await changed();
          }}
        />
      )}
      {transfer && (
        <TransferDialog
          mode={transfer}
          count={chosen.length}
          onClose={() => setTransfer(null)}
          onSave={async (dest, action) => {
            await api(`/shares/${encodeURIComponent(name)}/files/${transfer}`, {
              method: "POST",
              body: { paths: chosen.map((entry) => entry.path), dest, conflict: action },
            });
            setTransfer(null);
            setSelected([]);
            await changed();
          }}
        />
      )}
      {removeOpen && (
        <DeleteDialog
          permanent={inTrash || path === ".trash"}
          empty={path === ".trash" && chosen.length === 0}
          count={chosen.length}
          onClose={() => setRemoveOpen(false)}
          onSave={async () => {
            const paths = path === ".trash" && chosen.length === 0 ? [".trash"] : chosen.map((entry) => entry.path);
            await api(`/shares/${encodeURIComponent(name)}/files/delete`, { method: "POST", body: { paths } });
            setRemoveOpen(false);
            setSelected([]);
            await changed();
          }}
        />
      )}
      {fetchOpen && (
        <FetchDialog
          share={name}
          dir={path}
          onClose={() => setFetchOpen(false)}
          onStarted={async () => {
            await refreshJobs();
            await changed();
          }}
        />
      )}
    </div>
  );
}

function FolderTree({
  share,
  current,
  stamp,
  onOpen,
}: {
  share: string;
  current: string;
  stamp: number;
  onOpen: (path: string) => void;
}) {
  return <TreeDir share={share} path="" label={share} current={current} stamp={stamp} onOpen={onOpen} depth={0} />;
}

function TreeDir({
  share,
  path,
  label,
  current,
  stamp,
  onOpen,
  depth,
}: {
  share: string;
  path: string;
  label: string;
  current: string;
  stamp: number;
  onOpen: (path: string) => void;
  depth: number;
}) {
  const here = clean(current) === path;
  const child = path !== "" && (current === path || current.startsWith(`${path}/`));
  const [open, setOpen] = useState(depth === 0 || child);
  const [dirs, setDirs] = useState<Entry[] | null>(null);

  useEffect(() => {
    if (!open) return;
    let gone = false;
    api<Listing>(`/shares/${encodeURIComponent(share)}/files?path=${encodeURIComponent(path)}`)
      .then((list) => {
        if (!gone) setDirs(list.entries.filter((entry) => entry.dir && entry.name !== ".trash"));
      })
      .catch(() => {
        if (!gone) setDirs([]);
      });
    return () => {
      gone = true;
    };
  }, [open, share, path, stamp]);

  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setOpen(true);
          onOpen(path);
        }}
        className={`flex w-full items-center gap-1.5 rounded-xl px-2 py-1.5 text-left text-sm font-semibold ${
          here ? "bg-accent/12 text-accent" : "text-ink-2 hover:bg-accent/8 hover:text-ink"
        }`}
        style={{ paddingLeft: `${depth * 12 + 8}px` }}
      >
        <ChevronRight
          size={14}
          className={`shrink-0 transition-transform ${open ? "rotate-90" : ""}`}
          onClick={(e) => {
            e.stopPropagation();
            setOpen((v) => !v);
          }}
        />
        <Folder size={14} />
        <span className="truncate">{label}</span>
      </button>
      {open &&
        dirs?.map((dir) => (
          <TreeDir
            key={dir.path}
            share={share}
            path={dir.path}
            label={dir.name}
            current={current}
            stamp={stamp}
            onOpen={onOpen}
            depth={depth + 1}
          />
        ))}
    </div>
  );
}

function Meter({ value }: { value: number }) {
  const p = Math.max(0, Math.min(100, value));
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-track" role="progressbar" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <div className="accent-gradient h-full rounded-full transition-[width] duration-300" style={{ width: `${p}%` }} />
    </div>
  );
}

function UploadPanel({ snap, onStop, onDismiss }: { snap: UploadSnapshot; onStop: () => void; onDismiss: () => void }) {
  const files = snap.items.filter((item) => item.kind === "file");
  const ratio = snap.totalBytes > 0 ? snap.doneBytes / snap.totalBytes : files.length ? files.filter((f) => f.status === "done" || f.status === "skipped").length / files.length : snap.folders > 0 ? 1 : 0;
  return (
    <Card>
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-extrabold text-ink">{snap.active ? "Uploading" : "Upload finished"}</h2>
          <p className="text-xs font-medium text-ink-3">
            {snap.folders} folder{snap.folders === 1 ? "" : "s"} · {files.length} file{files.length === 1 ? "" : "s"} · {bytes(snap.doneBytes)} of {bytes(snap.totalBytes)}
          </p>
        </div>
        <div className="flex gap-2">
          {snap.active ? (
            <Button size="sm" variant="ghost" onClick={onStop}>
              Stop
            </Button>
          ) : (
            <IconButton label="Dismiss upload" onClick={onDismiss}>
              <X size={16} />
            </IconButton>
          )}
        </div>
      </div>
      <div className="mt-3">
        <Meter value={ratio * 100} />
      </div>
      {snap.items.some((item) => item.kind === "dir") && (
        <p className="mt-2 text-[11px] font-medium leading-relaxed text-ink-3">
          Folders: {snap.items.filter((item) => item.kind === "dir").map((item) => item.path).join(", ")}
        </p>
      )}
      <ul className="mt-3 max-h-64 space-y-2 overflow-auto">
        {files.slice(0, 12).map((item) => (
          <li key={item.key}>
            <div className="flex items-center justify-between gap-3 text-xs font-semibold">
              <span className="truncate text-ink" title={item.path}>
                {item.path}
              </span>
              <span className="shrink-0 text-ink-3">
                {item.status === "error" ? item.error : item.status === "skipped" ? "Skipped" : item.status === "done" ? "Done" : `${Math.round(item.size ? (item.sent / item.size) * 100 : 0)}%`}
              </span>
            </div>
            <div className="mt-1">
              <Meter value={item.size ? (item.sent / item.size) * 100 : item.status === "done" ? 100 : 0} />
            </div>
          </li>
        ))}
      </ul>
      {files.length > 12 && <p className="mt-2 text-xs font-medium text-ink-3">And {files.length - 12} more files.</p>}
    </Card>
  );
}

function FetchPanel({ jobs, onCancel }: { jobs: FetchJob[]; onCancel: (id: string) => void }) {
  return (
    <Card>
      <h2 className="text-sm font-extrabold text-ink">Downloads from URL</h2>
      <ul className="mt-3 space-y-3">
        {jobs.slice(0, 6).map((job) => {
          const pct = job.total > 0 ? (job.bytes / job.total) * 100 : job.status === "done" ? 100 : 0;
          return (
            <li key={job.id}>
              <div className="flex items-center justify-between gap-3 text-xs font-semibold">
                <span className="truncate text-ink" title={job.url}>
                  {job.name || job.path} · {job.status}
                  {job.speed > 0 ? ` · ${bytesPerSec(job.speed)}` : ""}
                </span>
                {(job.status === "running" || job.status === "queued") && (
                  <Button size="sm" variant="ghost" onClick={() => onCancel(job.id)}>
                    Cancel
                  </Button>
                )}
              </div>
              <p className="text-[11px] font-medium text-ink-3">
                {bytes(job.bytes)}
                {job.total > 0 ? ` of ${bytes(job.total)}` : ""}
                {job.error ? ` · ${job.error}` : ""}
              </p>
              <div className="mt-1">
                <Meter value={pct} />
              </div>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}

function NameDialog({
  title,
  label,
  initial = "",
  confirm,
  onClose,
  onSave,
}: {
  title: string;
  label: string;
  initial?: string;
  confirm: string;
  onClose: () => void;
  onSave: (value: string) => Promise<void>;
}) {
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <Modal
      title={title}
      onClose={onClose}
      onSubmit={() => {
        if (!value.trim() || busy) return;
        setBusy(true);
        setError(null);
        void onSave(value.trim()).catch((e) => {
          setError(errorMessage(e));
          setBusy(false);
        });
      }}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" loading={busy}>
            {confirm}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label={label} value={value} onChange={(e) => setValue(e.target.value)} autoFocus />
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}

function TransferDialog({
  mode,
  count,
  onClose,
  onSave,
}: {
  mode: "move" | "copy";
  count: number;
  onClose: () => void;
  onSave: (dest: string, conflict: ConflictAction) => Promise<void>;
}) {
  const [dest, setDest] = useState("");
  const [conflict, setConflict] = useState<ConflictAction>("rename");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <Modal
      title={mode === "move" ? "Move" : "Copy"}
      onClose={onClose}
      onSubmit={() => {
        setBusy(true);
        setError(null);
        void onSave(dest.trim(), conflict).catch((e) => {
          setError(errorMessage(e));
          setBusy(false);
        });
      }}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" loading={busy}>
            {mode === "move" ? "Move" : "Copy"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-sm font-medium text-ink-2">
          {count} item{count === 1 ? "" : "s"}. Leave the folder blank to use the top of the share. The folder has to exist already.
        </p>
        <TextField label="Destination folder" value={dest} onChange={(e) => setDest(e.target.value)} placeholder="photos/2024" />
        <label className="block text-xs font-bold text-ink-2">
          If the name is taken
          <select className={`${inputClass} mt-1.5`} value={conflict} onChange={(e) => setConflict(e.target.value as ConflictAction)}>
            <option value="rename">Rename the new one</option>
            <option value="overwrite">Overwrite</option>
            <option value="skip">Skip</option>
          </select>
        </label>
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}

function DeleteDialog({
  permanent,
  empty,
  count,
  onClose,
  onSave,
}: {
  permanent: boolean;
  empty: boolean;
  count: number;
  onClose: () => void;
  onSave: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const title = empty ? "Empty trash?" : permanent ? "Delete permanently?" : "Move to trash?";
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="danger"
            loading={busy}
            onClick={() => {
              setBusy(true);
              setError(null);
              void onSave().catch((e) => {
                setError(errorMessage(e));
                setBusy(false);
              });
            }}
          >
            {permanent ? "Delete" : "Move to trash"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-sm font-medium leading-relaxed text-ink-2">
          {empty
            ? "Everything in the trash is removed for good."
            : permanent
              ? `${count} item${count === 1 ? "" : "s"} will be removed for good.`
              : `${count} item${count === 1 ? "" : "s"} will move to this share's trash. You can restore them to the top of the share.`}
        </p>
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}

function FetchDialog({
  share,
  dir,
  onClose,
  onStarted,
}: {
  share: string;
  dir: string;
  onClose: () => void;
  onStarted: () => Promise<void>;
}) {
  const [url, setUrl] = useState("");
  const [filename, setFilename] = useState("");
  const [conflict, setConflict] = useState<ConflictAction>("rename");
  const [lan, setLan] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [job, setJob] = useState<FetchJob | null>(null);

  useEffect(() => {
    api<{ allowLANURLs: boolean }>("/settings/files")
      .then((res) => setLan(res.allowLANURLs))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!job || (job.status !== "running" && job.status !== "queued")) return;
    const t = setInterval(() => {
      api<FetchJob>(`/shares/${encodeURIComponent(share)}/fetch/${encodeURIComponent(job.id)}`)
        .then((next) => {
          setJob(next);
          if (next.status === "done" || next.status === "skipped") void onStarted();
        })
        .catch((e) => setError(errorMessage(e)));
    }, 1000);
    return () => clearInterval(t);
  }, [job, share]);

  const pct = job && job.total > 0 ? (job.bytes / job.total) * 100 : job?.status === "done" ? 100 : 0;

  return (
    <Modal
      title="Download from URL"
      onClose={onClose}
      onSubmit={() => {
        if (busy || job) return;
        setBusy(true);
        setError(null);
        void api<FetchJob>(`/shares/${encodeURIComponent(share)}/fetch`, {
          method: "POST",
          body: { url: url.trim(), dir, filename: filename.trim(), conflict },
        })
          .then(async (started) => {
            setJob(started);
            setBusy(false);
            await onStarted();
          })
          .catch((e) => {
            setError(errorMessage(e));
            setBusy(false);
          });
      }}
      footer={
        <>
          <Button onClick={onClose}>Close</Button>
          {!job && (
            <Button variant="primary" type="submit" loading={busy}>
              Download
            </Button>
          )}
          {job && (job.status === "running" || job.status === "queued") && (
            <Button
              variant="danger"
              onClick={() => {
                void api(`/shares/${encodeURIComponent(share)}/fetch/${encodeURIComponent(job.id)}`, { method: "DELETE" }).then(() =>
                  setJob({ ...job, status: "cancelled" }),
                );
              }}
            >
              Cancel
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-4">
        <TextField label="URL" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" disabled={!!job} />
        <TextField
          label="File name"
          hint="Optional. Leave blank to keep the name from the URL."
          value={filename}
          onChange={(e) => setFilename(e.target.value)}
          disabled={!!job}
        />
        <label className="block text-xs font-bold text-ink-2">
          If the name is taken
          <select className={`${inputClass} mt-1.5`} value={conflict} disabled={!!job} onChange={(e) => setConflict(e.target.value as ConflictAction)}>
            <option value="rename">Rename the new one</option>
            <option value="overwrite">Overwrite</option>
            <option value="skip">Skip</option>
          </select>
        </label>
        <div className="flex items-start gap-3">
          <Toggle
            checked={lan}
            label="Allow addresses on this network"
            onChange={(on) => {
              setLan(on);
              void api("/settings/files", { method: "PUT", body: { allowLANURLs: on } }).catch((e) => {
                setLan(!on);
                setError(errorMessage(e));
              });
            }}
          />
          <p className="text-xs font-medium leading-relaxed text-ink-3">
            Lets a URL download reach other computers on your network. Localhost, link-local and cloud metadata addresses stay blocked.
          </p>
        </div>
        {job && (
          <div>
            <p className="text-xs font-semibold text-ink">
              {job.status} · {bytes(job.bytes)}
              {job.total > 0 ? ` of ${bytes(job.total)}` : ""}
              {job.speed > 0 ? ` · ${bytesPerSec(job.speed)}` : ""}
            </p>
            <div className="mt-2">
              <Meter value={pct} />
            </div>
            {job.error && <p className="mt-2 text-xs font-semibold text-bad">{job.error}</p>}
          </div>
        )}
        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}
