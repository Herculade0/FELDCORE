import { useState, type ReactNode } from "react";
import { Check, Copy, Link2 } from "lucide-react";
import { useAppNav } from "../../lib/appnav";

export interface OutlineItem {
  id: string;
  text: string;
  level: number;
}

export function slugify(text: string): string {
  const base = text
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-");
  return base || "section";
}

/** Headings outside code fences, with the same ids the renderer uses. */
export function outline(source: string): OutlineItem[] {
  const seen = new Map<string, number>();
  const out: OutlineItem[] = [];
  let fence = false;
  for (const line of source.split("\n")) {
    if (line.startsWith("```")) {
      fence = !fence;
      continue;
    }
    if (fence) continue;
    const match = /^(#{2,3})\s+(.+)$/.exec(line);
    if (!match) continue;
    const text = match[2].replace(/[`*_]/g, "").trim();
    out.push({ id: uniqueSlug(text, seen), text, level: match[1].length });
  }
  return out;
}

function uniqueSlug(text: string, seen: Map<string, number>): string {
  const base = slugify(text);
  const n = seen.get(base) ?? 0;
  seen.set(base, n + 1);
  return n === 0 ? base : `${base}-${n + 1}`;
}

export function Markdown({ source }: { source: string }) {
  const blocks = parseBlocks(source);
  return (
    <div className="help-doc">
      {blocks.map((block, index) => (
        <div key={index}>{renderBlock(block)}</div>
      ))}
    </div>
  );
}

type Block =
  | { type: "h"; level: number; id: string; text: string }
  | { type: "p"; text: string }
  | { type: "ul"; items: string[] }
  | { type: "ol"; items: string[] }
  | { type: "code"; lang: string; code: string }
  | { type: "quote"; kind: "tip" | "warning" | "note" | "plain"; text: string }
  | { type: "table"; header: string[]; rows: string[][] }
  | { type: "rule" };

function parseBlocks(source: string): Block[] {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  const seen = new Map<string, number>();
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === "") {
      i++;
      continue;
    }
    if (line.startsWith("```")) {
      const lang = line.slice(3).trim();
      const buf: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith("```")) {
        buf.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++;
      blocks.push({ type: "code", lang, code: buf.join("\n") });
      continue;
    }
    const heading = /^(#{1,3})\s+(.+)$/.exec(line);
    if (heading) {
      const text = heading[2].trim();
      blocks.push({
        type: "h",
        level: heading[1].length,
        id: uniqueSlug(text.replace(/[`*_]/g, ""), seen),
        text,
      });
      i++;
      continue;
    }
    if (line.startsWith(">")) {
      const buf: string[] = [];
      while (i < lines.length && (lines[i].startsWith(">") || (lines[i].trim() === "" && i + 1 < lines.length && lines[i + 1].startsWith(">")))) {
        buf.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      const text = buf.join("\n").trim();
      const kind = calloutKind(text);
      blocks.push({
        type: "quote",
        kind,
        text: kind === "plain" ? text : text.replace(/^\[!(?:TIP|WARNING|NOTE)\]\s*/, "").trim(),
      });
      continue;
    }
    if (line.startsWith("|") && i + 1 < lines.length && /^\|\s*-+/.test(lines[i + 1])) {
      const header = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].startsWith("|")) {
        rows.push(splitRow(lines[i]));
        i++;
      }
      blocks.push({ type: "table", header, rows });
      continue;
    }
    if (/^[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^[-*]\s+/, ""));
        i++;
      }
      blocks.push({ type: "ul", items });
      continue;
    }
    if (/^\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\d+\.\s+/, ""));
        i++;
      }
      blocks.push({ type: "ol", items });
      continue;
    }
    if (line.trim() === "---") {
      blocks.push({ type: "rule" });
      i++;
      continue;
    }
    const buf = [line];
    i++;
    while (i < lines.length && lines[i].trim() !== "" && !blockStart(lines[i])) {
      buf.push(lines[i]);
      i++;
    }
    blocks.push({ type: "p", text: buf.join(" ") });
  }
  return blocks;
}

function blockStart(line: string): boolean {
  return (
    line.startsWith("```") ||
    line.startsWith("#") ||
    line.startsWith(">") ||
    line.startsWith("|") ||
    /^[-*]\s+/.test(line) ||
    /^\d+\.\s+/.test(line) ||
    line.trim() === "---"
  );
}

function calloutKind(text: string): "tip" | "warning" | "note" | "plain" {
  if (text.startsWith("[!TIP]")) return "tip";
  if (text.startsWith("[!WARNING]")) return "warning";
  if (text.startsWith("[!NOTE]")) return "note";
  return "plain";
}

function splitRow(line: string): string[] {
  return line
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((cell) => cell.trim());
}

function renderBlock(block: Block): ReactNode {
  switch (block.type) {
    case "h":
      return <Heading key={block.id} block={block} />;
    case "p":
      return (
        <p key={block.text} className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          {inline(block.text)}
        </p>
      );
    case "ul":
      return (
        <ul key={block.items.join("|")} className="mt-3 list-disc space-y-1.5 pl-5 text-sm font-medium leading-relaxed text-ink-2">
          {block.items.map((item) => (
            <li key={item}>{inline(item)}</li>
          ))}
        </ul>
      );
    case "ol":
      return (
        <ol key={block.items.join("|")} className="mt-3 list-decimal space-y-1.5 pl-5 text-sm font-medium leading-relaxed text-ink-2">
          {block.items.map((item) => (
            <li key={item} className="pl-1">
              {inline(item)}
            </li>
          ))}
        </ol>
      );
    case "code":
      return <CodeBlock key={block.code} code={block.code} lang={block.lang} />;
    case "quote":
      return <Callout key={block.text} kind={block.kind} text={block.text} />;
    case "table":
      return <Table key={block.header.join("|")} header={block.header} rows={block.rows} />;
    case "rule":
      return <hr key="rule" className="my-6 border-line" />;
    default:
      return null;
  }
}

function Heading({ block }: { block: Extract<Block, { type: "h" }> }) {
  const Tag = block.level >= 3 ? "h3" : "h2";
  const cls =
    block.level === 1
      ? "mt-8 text-2xl font-extrabold tracking-tight text-ink"
      : block.level === 2
        ? "mt-8 text-xl font-extrabold tracking-tight text-ink"
        : "mt-6 text-base font-extrabold text-ink";
  return (
    <Tag id={block.id} className={`group scroll-mt-24 ${cls}`}>
      <a
        href={`#${block.id}`}
        onClick={(e) => {
          e.preventDefault();
          document.getElementById(block.id)?.scrollIntoView({ behavior: "smooth", block: "start" });
        }}
        className="inline-flex items-center gap-2"
      >
        {inline(block.text)}
        <Link2 size={14} className="shrink-0 text-ink-3 opacity-0 transition group-hover:opacity-100" />
      </a>
    </Tag>
  );
}

function Callout({ kind, text }: { kind: "tip" | "warning" | "note" | "plain"; text: string }) {
  if (kind === "plain") {
    return (
      <blockquote className="mt-4 border-l-4 border-line pl-4 text-sm font-medium leading-relaxed text-ink-2">
        {text.split("\n").map((line) => (
          <p key={line} className="mt-1">
            {inline(line)}
          </p>
        ))}
      </blockquote>
    );
  }
  const tone =
    kind === "warning"
      ? "border-warn bg-warn/10"
      : kind === "note"
        ? "border-ink-3 bg-track/70"
        : "border-accent bg-accent/8";
  const label = kind === "warning" ? "Warning" : kind === "note" ? "Note" : "Tip";
  return (
    <aside className={`mt-4 rounded-2xl border-l-4 px-4 py-3 ${tone}`}>
      <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-ink">{label}</p>
      <div className="mt-1 space-y-1 text-sm font-medium leading-relaxed text-ink-2">
        {text.split("\n").filter(Boolean).map((line) => (
          <p key={line}>{inline(line)}</p>
        ))}
      </div>
    </aside>
  );
}

function Table({ header, rows }: { header: string[]; rows: string[][] }) {
  return (
    <div className="mt-4 overflow-x-auto rounded-2xl border border-line">
      <table className="w-full min-w-[28rem] text-left text-sm">
        <thead>
          <tr className="border-b border-line bg-surface-2">
            {header.map((cell) => (
              <th key={cell} className="px-3 py-2 text-[10px] font-bold uppercase tracking-[0.14em] text-ink-3">
                {inline(cell)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.join("|")} className="border-b border-line last:border-0">
              {row.map((cell, i) => (
                <td key={`${i}-${cell}`} className="px-3 py-2 align-top font-medium text-ink-2">
                  {inline(cell)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CodeBlock({ code, lang }: { code: string; lang: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard
      .writeText(code)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => setCopied(false));
  };
  return (
    <div className="relative mt-4 overflow-hidden rounded-2xl bg-[#0b1428]">
      <div className="flex items-center justify-between px-4 py-2 text-[10px] font-bold uppercase tracking-[0.16em] text-[#8ea0c5]">
        <span>{lang || "text"}</span>
        <button type="button" onClick={copy} className="inline-flex items-center gap-1.5 text-[#d5def0]">
          {copied ? <Check size={13} /> : <Copy size={13} />}
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre className="overflow-x-auto px-4 pb-4 font-mono text-xs leading-relaxed text-[#e8eefc]">
        <code>{code}</code>
      </pre>
    </div>
  );
}

function inline(text: string): ReactNode[] {
  const re =
    /!\[([^\]]*)\]\(([^)\s]+)\)|\[\[(route|help):([a-z0-9.-]+)\|([^\]]+)\]\]|\[([^\]]+)\]\(([^)\s]+)\)|`([^`]+)`|\*\*([^*]+)\*\*/g;
  const out: ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const match of text.matchAll(re)) {
    const index = match.index ?? 0;
    if (index > last) out.push(text.slice(last, index));
    const key = `${n++}-${index}`;
    if (match[2]) {
      out.push(<HelpImage key={key} alt={match[1]} src={match[2]} />);
    } else if (match[3] === "route") {
      out.push(<RouteLink key={key} id={match[4]} label={match[5]} />);
    } else if (match[3] === "help") {
      out.push(<HelpLink key={key} id={match[4]} label={match[5]} />);
    } else if (match[6]) {
      const href = match[7];
      if (href.startsWith("help:")) {
        out.push(<HelpLink key={key} id={href.slice(5)} label={match[6]} />);
      } else {
        out.push(
          <a
            key={key}
            href={href}
            target="_blank"
            rel="noreferrer"
            className="font-bold text-accent underline decoration-accent/30 underline-offset-2"
          >
            {match[6]}
          </a>,
        );
      }
    } else if (match[8]) {
      out.push(
        <code key={key} className="rounded-md bg-track px-1.5 py-0.5 font-mono text-[0.85em] text-ink">
          {match[8]}
        </code>,
      );
    } else if (match[9]) {
      out.push(
        <strong key={key} className="font-extrabold text-ink">
          {match[9]}
        </strong>,
      );
    }
    last = index + match[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

function HelpImage({ alt, src }: { alt: string; src: string }) {
  const url = src.startsWith("http") || src.startsWith("/") ? src : `/api/v1/help/media/${src.replace(/^\.\//, "")}`;
  return <img src={url} alt={alt} className="mt-4 max-w-full rounded-2xl border border-line" />;
}

function RouteLink({ id, label }: { id: string; label: string }) {
  const { routes, openRoute } = useAppNav();
  const route = routes[id];
  if (!route) {
    return (
      <span
        className="mx-0.5 inline-flex items-center rounded-xl border border-dashed border-line px-3 py-1.5 text-xs font-bold text-ink-3"
        title="This link is not in the route map."
      >
        {label}
      </span>
    );
  }
  return (
    <button
      type="button"
      onClick={() => openRoute(id)}
      title={route.note || undefined}
      className="mx-0.5 my-1 inline-flex items-center gap-2 rounded-xl border border-accent/30 bg-accent/10 px-3 py-1.5 text-left text-xs font-bold text-accent transition hover:bg-accent/16"
    >
      {label}
      {!route.available && (
        <span className="rounded-full bg-track px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-ink-3">
          Soon
        </span>
      )}
    </button>
  );
}

function HelpLink({ id, label }: { id: string; label: string }) {
  const { openHelp } = useAppNav();
  return (
    <button
      type="button"
      onClick={() => openHelp(id)}
      className="font-bold text-accent underline decoration-accent/30 underline-offset-2"
    >
      {label}
    </button>
  );
}
