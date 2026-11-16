import { useState, type CSSProperties, type ReactNode } from "react";
import { Button } from "../form";

export interface BrandLink {
  label: string;
  url: string;
}

/** One sign-in page. Empty fields fall back to the Citizen ID defaults. */
export interface SignInBrand {
  title: string;
  welcome: string;
  logo: string;
  accent: string;
  background: string;
  footer: string;
  links: BrandLink[];
}

export const emptyBrand: SignInBrand = {
  title: "",
  welcome: "",
  logo: "",
  accent: "",
  background: "",
  footer: "",
  links: [],
};

const FALLBACK = {
  title: "Citizen ID",
  welcome: "One sign-in for this server and the apps published on it.",
  accent: "#0b5cff",
  background: "#f4f7fd",
};

const HEX = /^#[0-9a-fA-F]{6}$/;

export function asBrand(raw?: Partial<SignInBrand> | null): SignInBrand {
  return {
    title: raw?.title ?? "",
    welcome: raw?.welcome ?? "",
    logo: raw?.logo ?? "",
    accent: raw?.accent ?? "",
    background: raw?.background ?? "",
    footer: raw?.footer ?? "",
    links: (raw?.links ?? []).map((l) => ({ label: l.label ?? "", url: l.url ?? "" })),
  };
}

function hexOr(value: string, fallback: string) {
  return HEX.test(value.trim()) ? value.trim() : fallback;
}

function imageURL(value: string) {
  const v = value.trim();
  return v.startsWith("data:image/png;base64,") ||
    v.startsWith("data:image/jpeg;base64,") ||
    v.startsWith("data:image/webp;base64,")
    ? v
    : "";
}

/** The sign-in card, filled the same way the HTML page is. */
export function SignInPreview({ brand, host }: { brand: SignInBrand; host?: string }) {
  const accent = hexOr(brand.accent, FALLBACK.accent);
  const background = brand.background.trim();
  const bgImage = imageURL(background);
  const bg = bgImage ? FALLBACK.background : hexOr(background, FALLBACK.background);
  const logo = imageURL(brand.logo);
  const title = brand.title.trim() || FALLBACK.title;
  const welcome = brand.welcome.trim() || FALLBACK.welcome;
  const page: CSSProperties = {
    backgroundColor: bg,
    backgroundImage: bgImage
      ? `url("${bgImage}")`
      : `radial-gradient(18rem 12rem at 8% -12%, ${accent}33, transparent 62%), radial-gradient(16rem 12rem at 104% 4%, #38bdf828, transparent 60%)`,
    backgroundSize: bgImage ? "cover" : undefined,
    backgroundPosition: "center",
  };
  return (
    <div className="overflow-hidden rounded-2xl border border-line p-4" style={page}>
      <p className="mb-3 text-[10px] font-bold uppercase tracking-[0.16em] text-[#8592ad]">
        Preview{host ? ` · ${host}` : ""}
      </p>
      <div className="mx-auto w-full max-w-sm rounded-[1.6rem] border border-[rgba(11,40,110,.12)] bg-white/80 p-5 shadow-md">
        <div className="flex items-center gap-3">
          {logo ? (
            <img src={logo} alt="" className="h-10 w-10 rounded-xl object-cover" />
          ) : (
            <span
              className="inline-block h-10 w-10 rounded-xl"
              style={{ background: `linear-gradient(135deg, ${accent}, #38bdf8)` }}
            />
          )}
          <div>
            <p className="text-lg font-extrabold tracking-tight text-[#0a1633]">{title}</p>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-[#8592ad]">FELDCORE</p>
          </div>
        </div>
        <p className="mt-3 text-sm font-medium leading-relaxed text-[#43506b]">{welcome}</p>
        <div className="mt-4 space-y-2">
          <div className="h-9 rounded-xl border border-[rgba(11,40,110,.12)] bg-white/70" />
          <div className="h-9 rounded-xl border border-[rgba(11,40,110,.12)] bg-white/70" />
        </div>
        <div
          className="mt-4 rounded-xl px-3 py-2 text-center text-sm font-extrabold text-white"
          style={{ background: `linear-gradient(135deg, ${accent}, #38bdf8)` }}
        >
          Sign in
        </div>
        {brand.footer.trim() && (
          <p className="mt-3 text-xs font-medium leading-relaxed text-[#43506b]">{brand.footer.trim()}</p>
        )}
        {brand.links.filter((l) => l.label.trim() && safeLink(l.url)).map((l) => (
          <p key={`${l.label}-${l.url}`} className="mt-1 text-xs font-bold" style={{ color: accent }}>
            {l.label.trim()}
          </p>
        ))}
      </div>
    </div>
  );
}

function safeLink(url: string) {
  const raw = url.trim();
  return raw.startsWith("https://") || (raw.startsWith("/") && !raw.startsWith("//"));
}

function readImage(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!["image/png", "image/jpeg", "image/webp"].includes(file.type)) {
      reject(new Error("Use a PNG, JPEG or WebP image."));
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      const url = String(reader.result || "");
      if (url.length > 80 * 1024) reject(new Error("That image is too large. Keep it under 80 KB."));
      else resolve(url);
    };
    reader.onerror = () => reject(reader.error ?? new Error("That image could not be read."));
    reader.readAsDataURL(file);
  });
}

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block min-w-0">
      <span className="mb-1.5 block text-xs font-bold text-ink-2">{label}</span>
      {children}
      {hint && <span className="mt-1.5 block text-xs font-medium text-ink-3">{hint}</span>}
    </label>
  );
}

const box =
  "w-full min-w-0 rounded-xl border border-line bg-surface-2 px-3.5 py-2.5 text-sm font-medium text-ink placeholder:text-ink-3";

/**
 * Title, welcome, logo, colours, background and footer for a sign-in page,
 * with a live preview beside the fields.
 */
export function BrandFields({
  value,
  onChange,
  previewHost,
  hint,
}: {
  value: SignInBrand;
  onChange: (next: SignInBrand) => void;
  previewHost?: string;
  hint?: string;
}) {
  const [imageError, setImageError] = useState<string | null>(null);
  const set = (patch: Partial<SignInBrand>) => onChange({ ...value, ...patch });
  const links = value.links.length > 0 ? value.links : [{ label: "", url: "" }];

  const pick = async (file: File | undefined, key: "logo" | "background") => {
    if (!file) return;
    setImageError(null);
    try {
      set({ [key]: await readImage(file) });
    } catch (e) {
      setImageError(e instanceof Error ? e.message : "That image could not be read.");
    }
  };

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="space-y-4">
        {hint && <p className="text-xs font-medium leading-relaxed text-ink-3">{hint}</p>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Title" hint="Up to 60 characters. Blank uses Citizen ID.">
            <input className={box} value={value.title} maxLength={60} onChange={(e) => set({ title: e.target.value })} />
          </Field>
          <Field label="Accent colour">
            <span className="flex gap-2">
              <input
                type="color"
                aria-label="Accent colour"
                className="h-11 w-12 shrink-0 cursor-pointer rounded-xl border border-line bg-surface-2 p-1"
                value={hexOr(value.accent, FALLBACK.accent)}
                onChange={(e) => set({ accent: e.target.value })}
              />
              <input
                className={box}
                value={value.accent}
                placeholder={FALLBACK.accent}
                spellCheck={false}
                onChange={(e) => set({ accent: e.target.value })}
              />
            </span>
          </Field>
        </div>
        <Field label="Welcome text" hint="Up to 240 characters.">
          <textarea
            className={`${box} min-h-20`}
            value={value.welcome}
            maxLength={240}
            onChange={(e) => set({ welcome: e.target.value })}
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Logo" hint="PNG, JPEG or WebP, under 80 KB.">
            <input
              className="block w-full text-xs font-medium text-ink-2"
              type="file"
              accept="image/png,image/jpeg,image/webp"
              onChange={(e) => void pick(e.target.files?.[0], "logo")}
            />
            {imageURL(value.logo) && (
              <Button type="button" size="sm" className="mt-2" onClick={() => set({ logo: "" })}>
                Remove logo
              </Button>
            )}
          </Field>
          <Field label="Background" hint="A hex colour, or an image.">
            <span className="flex gap-2">
              <input
                type="color"
                aria-label="Background colour"
                className="h-11 w-12 shrink-0 cursor-pointer rounded-xl border border-line bg-surface-2 p-1"
                value={hexOr(value.background, FALLBACK.background)}
                onChange={(e) => set({ background: e.target.value })}
              />
              <input
                className={box}
                value={imageURL(value.background) ? "" : value.background}
                placeholder={FALLBACK.background}
                spellCheck={false}
                onChange={(e) => set({ background: e.target.value })}
              />
            </span>
            <input
              className="mt-2 block w-full text-xs font-medium text-ink-2"
              type="file"
              accept="image/png,image/jpeg,image/webp"
              onChange={(e) => void pick(e.target.files?.[0], "background")}
            />
            {imageURL(value.background) && (
              <Button type="button" size="sm" className="mt-2" onClick={() => set({ background: "" })}>
                Use a colour instead
              </Button>
            )}
          </Field>
        </div>
        <Field label="Footer" hint="Up to 200 characters.">
          <input className={box} value={value.footer} maxLength={200} onChange={(e) => set({ footer: e.target.value })} />
        </Field>
        <div className="space-y-2">
          <p className="text-xs font-bold text-ink-2">Footer links</p>
          <p className="text-xs font-medium text-ink-3">Up to four. Each address starts with https:// or /.</p>
          {links.map((link, i) => (
            <div key={i} className="grid gap-2 sm:grid-cols-2">
              <input
                className={box}
                value={link.label}
                placeholder="Label"
                maxLength={40}
                onChange={(e) => {
                  const next = links.map((l, n) => (n === i ? { ...l, label: e.target.value } : l));
                  set({ links: next });
                }}
              />
              <input
                className={box}
                value={link.url}
                placeholder="https:// or /path"
                spellCheck={false}
                onChange={(e) => {
                  const next = links.map((l, n) => (n === i ? { ...l, url: e.target.value } : l));
                  set({ links: next });
                }}
              />
            </div>
          ))}
          {links.length < 4 && (
            <Button type="button" size="sm" onClick={() => set({ links: [...links, { label: "", url: "" }] })}>
              Add a link
            </Button>
          )}
        </div>
        {imageError && <p className="text-xs font-semibold text-bad">{imageError}</p>}
      </div>
      <SignInPreview brand={value} host={previewHost} />
    </div>
  );
}
