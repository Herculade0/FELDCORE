// Folder uploads. A drop or a directory picker becomes real directories in
// the share: empty folders are created, and every file keeps its relative path.
// Nothing is packed into a zip.

import { ApiError, UNAUTHORIZED_EVENT } from "../../lib/api";

export type ConflictAction = "overwrite" | "skip" | "rename";

export interface IncomingFile {
  file: File;
  /** Path relative to the drop, using forward slashes. */
  rel: string;
}

export interface Incoming {
  files: IncomingFile[];
  /** Directory paths relative to the drop, including empty ones. */
  dirs: string[];
  rejected: number;
}

export interface TransferItem {
  key: string;
  path: string;
  kind: "file" | "dir";
  size: number;
  sent: number;
  status: "waiting" | "uploading" | "done" | "skipped" | "error";
  error?: string;
}

export interface UploadSnapshot {
  items: TransferItem[];
  doneBytes: number;
  totalBytes: number;
  active: boolean;
  folders: number;
}

interface Created {
  id?: string;
  offset: number;
  path: string;
  size: number;
  complete: boolean;
  skipped?: boolean;
}

interface ConflictBody {
  error?: string;
  conflict?: boolean;
  name?: string;
  path?: string;
  offset?: number;
}

interface EntryLike {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  fullPath?: string;
  file?: (ok: (f: File) => void, err: (e: DOMException) => void) => void;
  createReader?: () => {
    readEntries: (ok: (entries: EntryLike[]) => void, err: (e: DOMException) => void) => void;
  };
}

const CHUNK = 8 * 1024 * 1024;

/** A relative path that cannot climb out of the share. The server checks again. */
export function safeRel(input: string): string | null {
  if (input.includes("\0")) return null;
  const raw = input.replace(/\\/g, "/").replace(/^\/+/, "");
  const parts = raw.split("/");
  const out: string[] = [];
  for (const part of parts) {
    if (part === "" || part === ".") continue;
    if (part === ".." || part === ".uploads") return null;
    if (/[\u0000-\u001f]/.test(part)) return null;
    out.push(part);
  }
  return out.join("/");
}

export function joinRel(dir: string, rel: string): string | null {
  const d = safeRel(dir);
  const r = safeRel(rel);
  if (d == null || r == null) return null;
  if (!d) return r;
  if (!r) return d;
  return safeRel(`${d}/${r}`);
}

function parentDirs(path: string): string[] {
  const parts = path.split("/");
  const out: string[] = [];
  for (let i = 1; i < parts.length; i++) out.push(parts.slice(0, i).join("/"));
  return out;
}

/** Read a directory picker or a plain file picker. webkitRelativePath carries the tree. */
export function incomingFromFiles(list: FileList | File[]): Incoming {
  const files: IncomingFile[] = [];
  const dirs = new Set<string>();
  let rejected = 0;
  for (const file of Array.from(list)) {
    const raw = file.webkitRelativePath || file.name;
    const rel = safeRel(raw);
    if (!rel) {
      rejected++;
      continue;
    }
    files.push({ file, rel });
    for (const dir of parentDirs(rel)) dirs.add(dir);
  }
  return { files, dirs: [...dirs], rejected };
}

function entryOf(item: DataTransferItem): EntryLike | null {
  const withEntry = item as DataTransferItem & { webkitGetAsEntry?: () => EntryLike | null };
  if (typeof withEntry.webkitGetAsEntry !== "function") return null;
  return withEntry.webkitGetAsEntry();
}

function readFile(entry: EntryLike): Promise<File> {
  return new Promise((resolve, reject) => {
    if (!entry.file) {
      reject(new Error("missing file"));
      return;
    }
    entry.file(resolve, reject);
  });
}

/** readEntries returns batches and only reports an empty batch when the directory is finished. */
async function readDir(entry: EntryLike): Promise<EntryLike[]> {
  const reader = entry.createReader?.();
  if (!reader) return [];
  const out: EntryLike[] = [];
  for (;;) {
    const batch = await new Promise<EntryLike[]>((resolve, reject) => {
      reader.readEntries(resolve, reject);
    });
    if (batch.length === 0) return out;
    out.push(...batch);
  }
}

async function walk(entry: EntryLike, prefix: string, into: Incoming): Promise<void> {
  const rel = safeRel(prefix ? `${prefix}/${entry.name}` : entry.name);
  if (rel == null) {
    into.rejected++;
    return;
  }
  if (entry.isFile) {
    try {
      into.files.push({ file: await readFile(entry), rel });
    } catch {
      into.rejected++;
    }
    return;
  }
  if (!entry.isDirectory) return;
  into.dirs.push(rel);
  let children: EntryLike[] = [];
  try {
    children = await readDir(entry);
  } catch {
    into.rejected++;
    return;
  }
  for (const child of children) await walk(child, rel, into);
}

/** Recreate a dropped folder tree. Falls back to loose files when the browser has no entries. */
export async function incomingFromDrop(data: DataTransfer): Promise<Incoming> {
  const into: Incoming = { files: [], dirs: [], rejected: 0 };
  const items = Array.from(data.items ?? []);
  const entries = items
    .filter((item) => item.kind === "file")
    .map(entryOf)
    .filter((entry): entry is EntryLike => !!entry);
  if (entries.length > 0) {
    for (const entry of entries) await walk(entry, "", into);
    return into;
  }
  return incomingFromFiles(data.files);
}

async function call(path: string, init: RequestInit): Promise<{ status: number; body: ConflictBody & Created }> {
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, { credentials: "same-origin", ...init });
  } catch {
    throw new ApiError(0, "Can't reach FELDCORE. Check that the server is running.");
  }
  const text = await res.text();
  let body: ConflictBody & Created = { offset: 0, path: "", size: 0, complete: false };
  if (text) {
    try {
      body = JSON.parse(text) as ConflictBody & Created;
    } catch {
      body = { ...body, error: text };
    }
  }
  if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  return { status: res.status, body };
}

function message(body: ConflictBody, status: number): string {
  return body.error || `The request failed (${status}).`;
}

function snapshot(items: TransferItem[], active: boolean): UploadSnapshot {
  let doneBytes = 0;
  let totalBytes = 0;
  let folders = 0;
  for (const item of items) {
    if (item.kind === "dir") {
      if (item.status === "done") folders++;
      continue;
    }
    totalBytes += item.size;
    if (item.status === "done" || item.status === "skipped") doneBytes += item.size;
    else doneBytes += item.sent;
  }
  return { items: items.map((item) => ({ ...item })), doneBytes, totalBytes, active, folders };
}

export function startUpload(opts: {
  share: string;
  dest: string;
  incoming: Incoming;
  onConflict: (file: { path: string; name: string }) => Promise<{ action: ConflictAction; all: boolean } | "cancel">;
  onUpdate: (snap: UploadSnapshot) => void;
}): { cancel: () => void } {
  let stopped = false;
  const items: TransferItem[] = [];
  const dirs = new Set<string>();
  for (const dir of opts.incoming.dirs) {
    const full = joinRel(opts.dest, dir);
    if (full) dirs.add(full);
  }
  for (const file of opts.incoming.files) {
    const full = joinRel(opts.dest, file.rel);
    if (!full) {
      items.push({
        key: `bad-${items.length}`,
        path: file.rel,
        kind: "file",
        size: file.file.size,
        sent: 0,
        status: "error",
        error: "That path is outside this share.",
      });
      continue;
    }
    for (const dir of parentDirs(full)) dirs.add(dir);
    items.push({
      key: full,
      path: full,
      kind: "file",
      size: file.file.size,
      sent: 0,
      status: "waiting",
      error: undefined,
    });
  }
  const dirItems: TransferItem[] = [...dirs]
    .sort((a, b) => a.split("/").length - b.split("/").length || a.localeCompare(b))
    .map((path) => ({ key: `dir:${path}`, path, kind: "dir" as const, size: 0, sent: 0, status: "waiting" as const }));
  const all = [...dirItems, ...items];
  const files = new Map<string, File>();
  for (const file of opts.incoming.files) {
    const full = joinRel(opts.dest, file.rel);
    if (full) files.set(full, file.file);
  }
  const publish = (active: boolean) => opts.onUpdate(snapshot(all, active));
  publish(true);

  let applyAll: ConflictAction | null = null;
  let gate: Promise<void> = Promise.resolve();

  const ask = async (path: string): Promise<ConflictAction | "cancel"> => {
    if (applyAll) return applyAll;
    let release = () => {};
    const wait = gate;
    gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    await wait;
    try {
      if (stopped) return "cancel";
      if (applyAll) return applyAll;
      const answer = await opts.onConflict({ path, name: path.split("/").pop() || path });
      if (answer === "cancel") return "cancel";
      if (answer.all) applyAll = answer.action;
      return answer.action;
    } finally {
      release();
    }
  };

  const base = `/shares/${encodeURIComponent(opts.share)}`;

  const mkdir = async (item: TransferItem) => {
    if (stopped) return;
    item.status = "uploading";
    publish(true);
    const res = await call(`${base}/files/mkdir`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: item.path }),
    });
    if (res.status >= 400) {
      item.status = "error";
      item.error = message(res.body, res.status);
    } else {
      item.status = "done";
    }
    publish(true);
  };

  const uploadFile = async (item: TransferItem) => {
    if (stopped || item.status === "error") return;
    const file = files.get(item.path);
    if (!file) {
      item.status = "error";
      item.error = "That file is no longer available.";
      publish(true);
      return;
    }
    item.status = "uploading";
    publish(true);
    let action: ConflictAction | "fail" = "fail";
    for (;;) {
      if (stopped) {
        item.status = "error";
        item.error = "Stopped.";
        publish(true);
        return;
      }
      const created = await call(`${base}/uploads`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: item.path, size: file.size, conflict: action }),
      });
      if (created.status === 409 && created.body.conflict && action === "fail") {
        const choice = await ask(item.path);
        if (choice === "cancel" || stopped) {
          item.status = "skipped";
          publish(true);
          return;
        }
        action = choice;
        continue;
      }
      if (created.status >= 400) {
        item.status = "error";
        item.error = message(created.body, created.status);
        publish(true);
        return;
      }
      if (created.body.skipped) {
        item.status = "skipped";
        item.sent = item.size;
        publish(true);
        return;
      }
      item.path = created.body.path || item.path;
      if (created.body.complete || file.size === 0) {
        item.status = "done";
        item.sent = item.size;
        publish(true);
        return;
      }
      await sendChunks(item, file, created.body.id || "", created.body.offset || 0);
      return;
    }
  };

  const sendChunks = async (item: TransferItem, file: File, id: string, offset: number) => {
    let at = offset;
    let misses = 0;
    while (at < file.size) {
      if (stopped) {
        item.status = "error";
        item.error = "Stopped.";
        publish(true);
        return;
      }
      const end = Math.min(at + CHUNK, file.size);
      try {
        const res = await call(`${base}/uploads/${encodeURIComponent(id)}`, {
          method: "PATCH",
          headers: { "Upload-Offset": String(at) },
          body: file.slice(at, end),
        });
        if (res.status === 409 && typeof res.body.offset === "number") {
          at = res.body.offset;
          continue;
        }
        if (res.status >= 400) throw new ApiError(res.status, message(res.body, res.status));
        at = res.body.offset;
        item.sent = at;
        misses = 0;
        publish(true);
        if (res.body.complete) {
          item.status = "done";
          item.path = res.body.path || item.path;
          publish(true);
          return;
        }
      } catch (err) {
        misses++;
        if (misses > 3) {
          item.status = "error";
          item.error = err instanceof Error ? err.message : "The upload stopped.";
          publish(true);
          return;
        }
        const status = await call(`${base}/uploads/${encodeURIComponent(id)}`, { method: "GET" }).catch(() => null);
        if (status && status.status < 400) at = status.body.offset;
      }
    }
    item.status = "done";
    item.sent = item.size;
    publish(true);
  };

  void (async () => {
    for (const dir of dirItems) {
      if (stopped) break;
      try {
        await mkdir(dir);
      } catch (err) {
        dir.status = "error";
        dir.error = err instanceof Error ? err.message : "The folder could not be created.";
        publish(true);
      }
    }
    const pending = all.filter((item) => item.kind === "file" && item.status === "waiting");
    let cursor = 0;
    const workers = Math.min(3, pending.length);
    await Promise.all(
      Array.from({ length: workers }, async () => {
        for (;;) {
          if (stopped) return;
          const item = pending[cursor++];
          if (!item) return;
          try {
            await uploadFile(item);
          } catch (err) {
            item.status = "error";
            item.error = err instanceof Error ? err.message : "The upload stopped.";
            publish(true);
          }
        }
      }),
    );
    publish(false);
  })();

  return {
    cancel: () => {
      stopped = true;
    },
  };
}
