import { useState } from "react";
import { Moon, Sun } from "lucide-react";
import { NAV, type NavKey } from "./nav";
import { VERSION } from "./version";
import { ThemeProvider, useTheme } from "./lib/theme";
import { LogoMark, Wordmark } from "./components/Logo";
import Dashboard from "./components/Dashboard";
import SettingsPage from "./components/SettingsPage";
import ComingSoon from "./components/ComingSoon";

export default function App() {
  return (
    <ThemeProvider>
      <Shell />
    </ThemeProvider>
  );
}

function Shell() {
  const [active, setActive] = useState<NavKey>("dashboard");

  return (
    <div className="min-h-screen md:pl-[17.5rem]">
      {/* Desktop: floating glass rail */}
      <aside className="glass fixed inset-y-4 left-4 z-20 hidden w-60 flex-col rounded-3xl p-4 md:flex">
        <div className="flex items-center gap-3 px-2 py-2">
          <LogoMark />
          <Wordmark />
        </div>
        <nav className="mt-6 flex-1 space-y-1.5" aria-label="Main">
          {NAV.map(({ key, label, icon: Icon, ready }) => {
            const on = active === key;
            return (
              <button
                key={key}
                onClick={() => setActive(key)}
                aria-current={on ? "page" : undefined}
                className={`flex w-full items-center gap-3 rounded-2xl px-3.5 py-2.5 text-sm font-semibold transition-colors ${
                  on
                    ? "accent-gradient text-white shadow-lg shadow-accent/30"
                    : "text-ink-2 hover:bg-accent/8 hover:text-ink"
                }`}
              >
                <Icon size={18} strokeWidth={2.1} />
                {label}
                {!ready && (
                  <span
                    className={`ml-auto rounded-full px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider ${
                      on ? "bg-white/20 text-white" : "bg-track text-ink-3"
                    }`}
                  >
                    Soon
                  </span>
                )}
              </button>
            );
          })}
        </nav>
        <div className="flex items-center justify-between px-2">
          <span className="text-[10px] font-semibold tracking-wider text-ink-3">
            v{VERSION} · pre-alpha
          </span>
          <ThemeToggle />
        </div>
      </aside>

      {/* Mobile: top bar */}
      <header className="flex items-center justify-between px-4 pt-4 md:hidden">
        <div className="flex items-center gap-3">
          <LogoMark size={32} />
          <Wordmark />
        </div>
        <ThemeToggle />
      </header>

      <main className="mx-auto max-w-6xl px-4 pb-28 pt-5 md:px-6 md:pb-10 md:pt-8">
        {active === "dashboard" ? (
          <Dashboard onNavigate={setActive} />
        ) : active === "settings" ? (
          <SettingsPage />
        ) : (
          <ComingSoon nav={active} />
        )}
      </main>

      {/* Mobile: bottom dock */}
      <nav
        className="glass fixed inset-x-3 bottom-3 z-20 flex justify-around rounded-3xl p-1.5 md:hidden"
        aria-label="Main"
      >
        {NAV.map(({ key, label, icon: Icon }) => {
          const on = active === key;
          return (
            <button
              key={key}
              onClick={() => setActive(key)}
              aria-current={on ? "page" : undefined}
              className={`flex min-w-0 flex-1 flex-col items-center gap-1 rounded-2xl px-1 py-2 text-[10px] font-bold transition-colors ${
                on ? "accent-gradient text-white" : "text-ink-3"
              }`}
            >
              <Icon size={19} strokeWidth={2.1} />
              <span className="truncate">{label}</span>
            </button>
          );
        })}
      </nav>
    </div>
  );
}

function ThemeToggle() {
  const { resolved, setMode } = useTheme();
  const next = resolved === "dark" ? "light" : "dark";
  return (
    <button
      onClick={() => setMode(next)}
      aria-label={`Switch to ${next} theme`}
      title={`Switch to ${next} theme`}
      className="flex h-9 w-9 items-center justify-center rounded-xl text-ink-2 transition-colors hover:bg-accent/10 hover:text-accent"
    >
      {resolved === "dark" ? <Sun size={17} /> : <Moon size={17} />}
    </button>
  );
}
