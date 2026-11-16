import { NAV, type NavKey } from "../nav";
import { IconBadge } from "./ui";

type Pending = Exclude<NavKey, "dashboard" | "shares" | "docker" | "apps" | "settings" | "audit" | "updates">;

const COPY: Record<Pending, { title: string; body: string }> = {
  vms: {
    title: "Virtual Machines",
    body: "Create and control KVM virtual machines, with a console in the browser. Scheduled for a later phase.",
  },
};

export default function ComingSoon({ nav }: { nav: Pending }) {
  const item = NAV.find((n) => n.key === nav)!;
  const copy = COPY[nav];
  return (
    <div className="flex min-h-[70vh] items-center justify-center">
      <div className="glass flex max-w-md flex-col items-center rounded-[2rem] px-8 py-12 text-center">
        <IconBadge icon={item.icon} size={72} />
        <h1 className="mt-6 text-2xl font-extrabold tracking-tight text-ink">{copy.title}</h1>
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">{copy.body}</p>
        <span className="mt-6 rounded-full bg-accent/12 px-4 py-1.5 text-[10px] font-bold uppercase tracking-[0.2em] text-accent">
          In development
        </span>
      </div>
    </div>
  );
}
