import {
  useEffect,
  useId,
  useRef,
  type ButtonHTMLAttributes,
  type FormEvent,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react";
import { CircleAlert, Info, Loader2, TriangleAlert, X } from "lucide-react";

// Form controls and overlays shared by every page.

type Variant = "primary" | "secondary" | "danger" | "ghost";

const VARIANT: Record<Variant, string> = {
  primary: "accent-gradient text-white shadow-md shadow-accent/25 hover:brightness-110",
  secondary: "border border-line bg-surface-2 text-ink hover:border-accent/60",
  danger: "bg-bad text-white hover:brightness-110",
  ghost: "text-ink-2 hover:bg-accent/10 hover:text-accent",
};

export function Button({
  variant = "secondary",
  size = "md",
  loading = false,
  className = "",
  children,
  disabled,
  type = "button",
  ...rest
}: {
  variant?: Variant;
  size?: "sm" | "md";
  loading?: boolean;
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      className={`inline-flex items-center justify-center gap-2 rounded-xl font-bold transition disabled:cursor-not-allowed disabled:opacity-55 ${
        size === "sm" ? "px-3 py-1.5 text-xs" : "px-4 py-2.5 text-sm"
      } ${VARIANT[variant]} ${className}`}
      {...rest}
    >
      {loading && <Loader2 size={size === "sm" ? 13 : 15} className="animate-spin" />}
      {children}
    </button>
  );
}

/** Square icon-only button. `label` is required for screen readers. */
export function IconButton({
  label,
  children,
  className = "",
  ...rest
}: { label: string } & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-ink-2 transition-colors hover:bg-accent/10 hover:text-accent disabled:opacity-50 ${className}`}
      {...rest}
    >
      {children}
    </button>
  );
}

export function TextField({
  label,
  hint,
  error,
  className = "",
  ...rest
}: {
  label: string;
  hint?: ReactNode;
  error?: string | null;
} & InputHTMLAttributes<HTMLInputElement>) {
  const id = useId();
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1.5 block text-xs font-bold text-ink-2">
        {label}
      </label>
      <input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={hint || error ? `${id}-help` : undefined}
        className={`w-full rounded-xl border bg-surface-2 px-3.5 py-2.5 text-sm font-medium text-ink placeholder:text-ink-3 disabled:opacity-60 ${
          error ? "border-bad" : "border-line"
        }`}
        {...rest}
      />
      {(error || hint) && (
        <p
          id={`${id}-help`}
          className={`mt-1.5 text-xs font-medium ${error ? "text-bad" : "text-ink-3"}`}
        >
          {error || hint}
        </p>
      )}
    </div>
  );
}

/** Shared look for text-like inputs that aren't a TextField. */
export const inputClass =
  "w-full rounded-xl border border-line bg-surface-2 px-3.5 py-2.5 text-sm font-medium text-ink placeholder:text-ink-3 disabled:opacity-60";

export function SelectField({
  label,
  hint,
  children,
  className = "",
  ...rest
}: {
  label: string;
  hint?: ReactNode;
} & SelectHTMLAttributes<HTMLSelectElement>) {
  const id = useId();
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1.5 block text-xs font-bold text-ink-2">
        {label}
      </label>
      <select id={id} className={inputClass} {...rest}>
        {children}
      </select>
      {hint && <p className="mt-1.5 text-xs font-medium text-ink-3">{hint}</p>}
    </div>
  );
}

export function Toggle({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50 ${
        checked ? "accent-gradient" : "bg-track"
      }`}
    >
      <span
        className={`absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform ${
          checked ? "translate-x-5" : ""
        }`}
      />
    </button>
  );
}

const ALERT = {
  error: { cls: "border-bad/35 bg-bad/10", icon: CircleAlert, tone: "text-bad" },
  warn: { cls: "border-warn/40 bg-warn/10", icon: TriangleAlert, tone: "text-warn" },
  info: { cls: "border-accent/30 bg-accent/8", icon: Info, tone: "text-accent" },
} as const;

export function Alert({
  tone = "error",
  children,
  action,
}: {
  tone?: keyof typeof ALERT;
  children: ReactNode;
  action?: ReactNode;
}) {
  const a = ALERT[tone];
  const Icon = a.icon;
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={`flex items-start gap-3 rounded-2xl border px-4 py-3 text-sm font-medium text-ink ${a.cls}`}
    >
      <Icon size={17} className={`mt-0.5 shrink-0 ${a.tone}`} />
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  );
}

// Open dialogs, oldest first. Dialogs can stack (one opened from another), and
// Escape must close only the top one.
const openModals: symbol[] = [];

/**
 * A dialog. When `onSubmit` is given the dialog is a form, so Enter submits
 * and the footer's submit button works.
 */
export function Modal({
  title,
  onClose,
  onSubmit,
  children,
  footer,
  size = "md",
}: {
  title: string;
  onClose: () => void;
  onSubmit?: () => void;
  children: ReactNode;
  footer?: ReactNode;
  size?: "md" | "lg";
}) {
  const id = useId();
  // Keep the latest onClose without re-registering, which would reorder the stack.
  const close = useRef(onClose);
  close.current = onClose;

  useEffect(() => {
    const token = Symbol("modal");
    openModals.push(token);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && openModals[openModals.length - 1] === token) {
        close.current();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      const i = openModals.indexOf(token);
      if (i >= 0) openModals.splice(i, 1);
    };
  }, []);

  const body = (
    <>
      <header className="flex items-center justify-between gap-4 px-6 pt-5">
        <h2 id={id} className="text-lg font-extrabold tracking-tight text-ink">
          {title}
        </h2>
        <IconButton label="Close" onClick={onClose}>
          <X size={18} />
        </IconButton>
      </header>
      <div className="overflow-y-auto px-6 py-5">{children}</div>
      {footer && (
        <footer className="flex flex-wrap justify-end gap-3 border-t border-line px-6 py-4">
          {footer}
        </footer>
      )}
    </>
  );

  const panel = `panel relative flex max-h-[92vh] w-full flex-col rounded-t-3xl sm:rounded-3xl ${
    size === "lg" ? "max-w-2xl" : "max-w-lg"
  }`;

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center sm:items-center sm:p-6"
      role="dialog"
      aria-modal="true"
      aria-labelledby={id}
    >
      <div className="absolute inset-0 bg-black/55 backdrop-blur-sm" onClick={onClose} />
      {onSubmit ? (
        <form
          className={panel}
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            onSubmit();
          }}
        >
          {body}
        </form>
      ) : (
        <div className={panel}>{body}</div>
      )}
    </div>
  );
}
