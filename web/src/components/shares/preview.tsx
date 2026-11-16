import { useEffect, useState } from "react";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import plaintext from "highlight.js/lib/languages/plaintext";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";
import { Alert, Button, Modal } from "../form";
import { bytes } from "../../lib/format";
import "./highlight.css";

hljs.registerLanguage("bash", bash);
hljs.registerLanguage("c", c);
hljs.registerLanguage("cpp", cpp);
hljs.registerLanguage("css", css);
hljs.registerLanguage("diff", diff);
hljs.registerLanguage("dockerfile", dockerfile);
hljs.registerLanguage("go", go);
hljs.registerLanguage("ini", ini);
hljs.registerLanguage("java", java);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("json", json);
hljs.registerLanguage("markdown", markdown);
hljs.registerLanguage("plaintext", plaintext);
hljs.registerLanguage("python", python);
hljs.registerLanguage("rust", rust);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("yaml", yaml);

export type PreviewKind = "image" | "svg" | "video" | "audio" | "pdf" | "text" | "none";

export function previewKind(name: string): PreviewKind {
  const base = name.toLowerCase();
  const ext = base.includes(".") ? base.slice(base.lastIndexOf(".")) : "";
  if (ext === ".svg") return "svg";
  if ([".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".ico"].includes(ext)) return "image";
  if ([".mp4", ".webm", ".mov", ".m4v", ".ogv"].includes(ext)) return "video";
  if ([".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".oga", ".opus"].includes(ext)) return "audio";
  if (ext === ".pdf") return "pdf";
  if (
    [
      ".txt", ".md", ".markdown", ".log", ".json", ".yaml", ".yml", ".xml", ".csv",
      ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".go", ".py", ".rs", ".css",
      ".html", ".htm", ".sh", ".bash", ".conf", ".ini", ".toml", ".sql", ".c", ".h",
      ".cpp", ".java", ".rb", ".php", ".env", ".gitignore", ".dockerfile", ".diff",
    ].includes(ext) ||
    base.endsWith(".log") ||
    base === "dockerfile"
  ) {
    return "text";
  }
  return "none";
}

function language(name: string): string {
  const base = name.toLowerCase();
  const ext = base.includes(".") ? base.slice(base.lastIndexOf(".") + 1) : base;
  const map: Record<string, string> = {
    js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
    ts: "typescript", tsx: "typescript",
    py: "python", go: "go", json: "json", md: "markdown", markdown: "markdown",
    sh: "bash", bash: "bash", css: "css", xml: "xml", html: "xml", htm: "xml",
    yml: "yaml", yaml: "yaml", sql: "sql", rs: "rust", c: "c", h: "c", cpp: "cpp",
    java: "java", ini: "ini", conf: "ini", toml: "ini", diff: "diff",
    dockerfile: "dockerfile", txt: "plaintext", log: "plaintext",
  };
  return map[ext] || (base === "dockerfile" ? "dockerfile" : "plaintext");
}

export function contentURL(share: string, path: string, download = false): string {
  const q = new URLSearchParams({ path, preview: "1" });
  if (download) q.set("download", "1");
  return `/api/v1/shares/${encodeURIComponent(share)}/files/content?${q}`;
}

export function PreviewModal({
  share,
  path,
  name,
  size,
  onClose,
}: {
  share: string;
  path: string;
  name: string;
  size: number;
  onClose: () => void;
}) {
  const kind = previewKind(name);
  const src = contentURL(share, path);
  return (
    <Modal
      title={name}
      onClose={onClose}
      size={kind === "text" || kind === "pdf" || kind === "image" || kind === "svg" ? "xl" : "lg"}
      footer={
        <Button variant="primary" onClick={() => downloadFile(share, path)}>
          Download
        </Button>
      }
    >
      <p className="mb-3 text-xs font-semibold text-ink-3">
        {bytes(size)}
        {kind === "text" ? " · read only" : ""}
        {kind === "none" ? " · no preview for this type" : ""}
      </p>
      {kind === "image" && <img src={src} alt={name} className="mx-auto max-h-[70vh] max-w-full rounded-2xl" />}
      {kind === "svg" && <SvgPreview src={src} name={name} />}
      {kind === "video" && <video src={src} controls className="max-h-[70vh] w-full rounded-2xl bg-black" />}
      {kind === "audio" && <audio src={src} controls className="w-full" />}
      {kind === "pdf" && <PdfPreview src={src} />}
      {kind === "text" && <TextPreview src={src} name={name} />}
      {kind === "none" && (
        <p className="text-sm font-medium text-ink-2">Download this file to open it in another program.</p>
      )}
    </Modal>
  );
}

function SvgPreview({ src, name }: { src: string; name: string }) {
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let gone = false;
    let object: string | null = null;
    fetch(src, { credentials: "same-origin" })
      .then((res) => {
        if (!res.ok) throw new Error("The picture could not be opened.");
        return res.blob();
      })
      .then((blob) => {
        object = URL.createObjectURL(new Blob([blob], { type: "image/svg+xml" }));
        if (!gone) setUrl(object);
      })
      .catch((e: unknown) => {
        if (!gone) setError(e instanceof Error ? e.message : "The picture could not be opened.");
      });
    return () => {
      gone = true;
      if (object) URL.revokeObjectURL(object);
    };
  }, [src]);
  if (error) return <Alert>{error}</Alert>;
  if (!url) return <p className="text-sm font-medium text-ink-3">Loading…</p>;
  return <img src={url} alt={name} className="mx-auto max-h-[70vh] max-w-full rounded-2xl bg-white p-4" />;
}

function PdfPreview({ src }: { src: string }) {
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let gone = false;
    let object: string | null = null;
    fetch(src, { credentials: "same-origin" })
      .then((res) => {
        if (!res.ok) throw new Error("The PDF could not be opened.");
        return res.blob();
      })
      .then((blob) => {
        object = URL.createObjectURL(new Blob([blob], { type: "application/pdf" }));
        if (!gone) setUrl(object);
      })
      .catch((e: unknown) => {
        if (!gone) setError(e instanceof Error ? e.message : "The PDF could not be opened.");
      });
    return () => {
      gone = true;
      if (object) URL.revokeObjectURL(object);
    };
  }, [src]);
  if (error) return <Alert>{error}</Alert>;
  if (!url) return <p className="text-sm font-medium text-ink-3">Loading…</p>;
  return <iframe title="PDF preview" src={url} className="h-[70vh] w-full rounded-2xl border border-line bg-white" />;
}

function TextPreview({ src, name }: { src: string; name: string }) {
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let gone = false;
    fetch(src, { credentials: "same-origin" })
      .then(async (res) => {
        if (!res.ok) throw new Error("The file could not be opened.");
        return res.text();
      })
      .then((text) => {
        const lang = language(name);
        const highlighted = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value;
        if (!gone) setHtml(highlighted);
      })
      .catch((e: unknown) => {
        if (!gone) setError(e instanceof Error ? e.message : "The file could not be opened.");
      });
    return () => {
      gone = true;
    };
  }, [src, name]);
  if (error) return <Alert>{error}</Alert>;
  if (html == null) return <p className="text-sm font-medium text-ink-3">Loading…</p>;
  return (
    <pre className="max-h-[70vh] overflow-auto rounded-2xl border border-line bg-surface-2 p-4 text-xs leading-relaxed">
      <code className="hljs" dangerouslySetInnerHTML={{ __html: html }} />
    </pre>
  );
}

export function downloadFile(share: string, path: string) {
  const a = document.createElement("a");
  a.href = contentURL(share, path, true);
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

export function downloadZip(share: string, paths: string[]) {
  const form = document.createElement("form");
  form.method = "POST";
  form.action = `/api/v1/shares/${encodeURIComponent(share)}/files/zip`;
  form.style.display = "none";
  for (const path of paths) {
    const input = document.createElement("input");
    input.type = "hidden";
    input.name = "path";
    input.value = path;
    form.appendChild(input);
  }
  document.body.appendChild(form);
  form.submit();
  form.remove();
}
