import { CircleHelp } from "lucide-react";
import { useAppNav } from "../../lib/appnav";

/** Small ? that opens the Help article for this page. */
export function PageHelp({ topic }: { topic: string }) {
  const { openHelp } = useAppNav();
  return (
    <button
      type="button"
      aria-label="Help for this page"
      title="Help for this page"
      onClick={() => openHelp(topic)}
      className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-line text-ink-3 transition hover:border-accent/40 hover:text-accent"
    >
      <CircleHelp size={16} strokeWidth={2.25} />
    </button>
  );
}
