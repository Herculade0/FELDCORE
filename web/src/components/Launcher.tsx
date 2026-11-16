import { useEffect, useState } from "react";
import { ExternalLink, LayoutGrid } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Card, CardTitle } from "./ui";
import { Alert } from "./form";
import { OwnSignIn } from "./settings/CitizenID";

interface App {
  container: string;
  name: string;
  host: string;
  url: string;
  level: string;
}

export default function Launcher() {
  const { admin } = useAuth();
  const [apps, setApps] = useState<App[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api<{ apps: App[] }>("/launcher")
      .then((res) => {
        setApps(res.apps);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">App Launcher</h1>
        <p className="mt-2 max-w-xl text-sm font-semibold text-ink-2">
          {admin
            ? "Every published app. Who can open one is set in Settings → Citizen ID."
            : "The apps your groups can open. One Citizen ID sign-in covers all of them."}
        </p>
      </header>
      {error && <Alert>{error}</Alert>}
      <Card>
        <CardTitle icon={LayoutGrid} title="Published apps" hint={apps ? `${apps.length}` : undefined} />
        {apps === null ? (
          <p className="mt-5 text-sm font-medium text-ink-3">Loading…</p>
        ) : apps.length === 0 ? (
          <p className="mt-5 text-sm font-medium leading-relaxed text-ink-3">
            {admin
              ? "Nothing is published yet. Add a hostname on a container, then choose who can open it under Settings → Citizen ID."
              : "Nothing you can open is published yet."}
          </p>
        ) : (
          <ul className="mt-5 grid gap-3 sm:grid-cols-2">
            {apps.map((app) => (
              <li key={app.host}>
                <a
                  href={app.url}
                  className="flex items-center justify-between gap-3 rounded-2xl border border-line px-4 py-3.5 transition-colors hover:border-accent/60"
                >
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-bold text-ink">{app.name}</span>
                    <span className="block truncate text-xs font-medium text-ink-3">{app.host}</span>
                  </span>
                  <ExternalLink size={16} className="shrink-0 text-ink-3" />
                </a>
              </li>
            ))}
          </ul>
        )}
      </Card>
      {!admin && <OwnSignIn />}
    </div>
  );
}
