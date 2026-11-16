import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Folder, FolderTree } from "lucide-react";
import { api } from "../../lib/api";
import { inputClass } from "../form";

interface Suggestion {
  name: string;
  path: string;
}

interface Suggestions {
  shares: Suggestion[];
  dirs: Suggestion[];
}

/**
 * A host-path field that suggests where to point it. Focused and empty it
 * offers your shares; once you type "/" it lists the real folders on the
 * server as you go, so you never type a path blind.
 */
export default function PathInput({
  label,
  value,
  onChange,
  placeholder,
  hint,
  autoFocus,
}: {
  label: string;
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  hint?: string;
  autoFocus?: boolean;
}) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<Suggestions>({ shares: [], dirs: [] });
  const [active, setActive] = useState(-1);
  const input = useRef<HTMLInputElement>(null);
  const latest = useRef(0);

  // Look up suggestions shortly after the value settles.
  useEffect(() => {
    if (!open) return;
    const seq = ++latest.current;
    const t = setTimeout(() => {
      api<Suggestions>(`/fs/suggest?q=${encodeURIComponent(value)}`)
        .then((res) => {
          // Ignore answers that arrive after a newer question was asked.
          if (seq === latest.current) {
            setData({ shares: res.shares ?? [], dirs: res.dirs ?? [] });
            setActive(-1);
          }
        })
        .catch(() => {});
    }, 120);
    return () => clearTimeout(t);
  }, [value, open]);

  const browsing = value.startsWith("/");
  // While browsing, shares that match are already among the folders.
  const shares = browsing ? [] : data.shares;
  const dirs = browsing ? data.dirs : [];
  const items = [...shares, ...dirs];

  const choose = (s: Suggestion, drill: boolean) => {
    // Picking a folder leaves a trailing slash so its subfolders show next;
    // the slash is dropped when the setting is saved.
    onChange(drill ? s.path.replace(/\/+$/, "") + "/" : s.path);
    input.current?.focus();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (!open) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      if (!items.length) return;
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive((a) => (a + step + items.length) % items.length);
    } else if ((e.key === "Enter" || e.key === "Tab") && active >= 0 && items[active]) {
      e.preventDefault();
      choose(items[active], true);
    } else if (e.key === "Escape") {
      // Close the list, not the dialog behind it.
      e.stopPropagation();
      e.nativeEvent.stopImmediatePropagation();
      setOpen(false);
    }
  };

  const showList = open && (items.length > 0 || browsing);

  return (
    <div className="relative">
      <label htmlFor={id} className="mb-1.5 block text-xs font-bold text-ink-2">
        {label}
      </label>
      <input
        id={id}
        ref={input}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        // Delay so a click on a suggestion lands before the list closes.
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        onKeyDown={onKeyDown}
        placeholder={placeholder}
        autoFocus={autoFocus}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        role="combobox"
        aria-expanded={showList}
        aria-controls={`${id}-list`}
        aria-autocomplete="list"
        className={`${inputClass} font-mono !text-xs`}
      />
      {hint && !showList && <p className="mt-1.5 text-xs font-medium text-ink-3">{hint}</p>}

      {showList && (
        <div
          id={`${id}-list`}
          role="listbox"
          className="panel absolute inset-x-0 top-full z-30 mt-1.5 max-h-60 overflow-y-auto rounded-xl py-1.5"
        >
          {shares.length > 0 && <Heading>Your shares</Heading>}
          {shares.map((s, i) => (
            <Row
              key={`s-${s.path}`}
              icon={<FolderTree size={14} />}
              primary={s.name}
              secondary={s.path}
              active={active === i}
              onPick={() => choose(s, false)}
            />
          ))}
          {!browsing && (
            <p className="px-3.5 pb-1 pt-2 text-[11px] font-medium text-ink-3">
              Type <kbd className="font-mono font-bold text-ink-2">/</kbd> to browse every folder
              on this server.
            </p>
          )}

          {browsing && <Heading>Folders</Heading>}
          {dirs.map((d, i) => (
            <Row
              key={`d-${d.path}`}
              icon={<Folder size={14} />}
              primary={d.path}
              active={active === shares.length + i}
              onPick={() => choose(d, true)}
            />
          ))}
          {browsing && dirs.length === 0 && (
            <p className="px-3.5 py-2 text-xs font-medium text-ink-3">
              No matching folders. A folder that doesn't exist yet is created when the container
              starts.
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function Heading({ children }: { children: string }) {
  return (
    <div className="px-3.5 pb-1 pt-1.5 text-[10px] font-bold uppercase tracking-[0.16em] text-ink-3">
      {children}
    </div>
  );
}

function Row({
  icon,
  primary,
  secondary,
  active,
  onPick,
}: {
  icon: ReactNode;
  primary: string;
  secondary?: string;
  active: boolean;
  onPick: () => void;
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={active}
      // mousedown, not click: it fires before the input's blur closes the list.
      onMouseDown={(e) => {
        e.preventDefault();
        onPick();
      }}
      className={`flex w-full items-center gap-2.5 px-3.5 py-1.5 text-left transition-colors ${
        active ? "bg-accent/12" : "hover:bg-accent/8"
      }`}
    >
      <span className="shrink-0 text-accent">{icon}</span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink">{primary}</span>
      {secondary && (
        <span className="shrink-0 truncate font-mono text-[11px] text-ink-3">{secondary}</span>
      )}
    </button>
  );
}
