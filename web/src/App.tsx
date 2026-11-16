import { useState } from "react";
import { LogOut, Moon, Sun } from "lucide-react";
import { NAV, type NavKey } from "./nav";
import { VERSION } from "./version";
import { ThemeProvider, useTheme } from "./lib/theme";
import { AuthProvider, useAuth } from "./lib/auth";
import { LogoMark, Wordmark } from "./components/Logo";
import { IconButton } from "./components/form";
import { LoadingScreen, LoginScreen, SetupScreen } from "./components/AuthScreen";
import { ErrorBoundary } from "./components/ErrorBoundary";
import AuditPage from "./components/AuditPage";
import UpdatesPage from "./components/UpdatesPage";
import Dashboard from "./components/Dashboard";
import SettingsPage from "./components/SettingsPage";
import SharesPage from "./components/shares/SharesPage";
import DockerPage from "./components/docker/DockerPage";
import AppsPage from "./components/apps/AppsPage";
import VMsPage from "./components/vms/VMsPage";
import Wizard from "./components/setup/Wizard";
import { TerminalProvider } from "./components/Terminal";

export default function App() {
  return (
    <ThemeProvider>
      <AuthProvider>
        <Gate />
      </AuthProvider>
    </ThemeProvider>
  );
}

/** Nothing behind this renders until the visitor is signed in. */
function Gate() {
  const { phase, wizardPending, refresh } = useAuth();
  switch (phase) {
    case "ready":
      return wizardPending ? <Wizard onDone={() => void refresh()} /> : <Shell />;
    case "setup":
      return <SetupScreen />;
    case "login":
      return <LoginScreen />;
    case "unreachable":
      return <LoadingScreen unreachable />;
    default:
      return <LoadingScreen />;
  }
}

function Shell() {
  const [active, setActive] = useState<NavKey>("dashboard");
  const { username } = useAuth();

  return (
    <TerminalProvider>
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
        <div className="flex items-center gap-1 border-t border-line px-1 pt-3">
          <div className="min-w-0 flex-1 px-1">
            <div className="truncate text-xs font-bold text-ink">{username}</div>
            <div className="text-[10px] font-semibold tracking-wider text-ink-3">v{VERSION}</div>
          </div>
          <ThemeToggle />
          <SignOutButton />
        </div>
      </aside>

      {/* Mobile: top bar */}
      <header className="flex items-center justify-between px-4 pt-4 md:hidden">
        <div className="flex items-center gap-3">
          <LogoMark size={32} />
          <Wordmark />
        </div>
        <div className="flex">
          <ThemeToggle />
          <SignOutButton />
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 pb-28 pt-5 md:px-6 md:pb-10 md:pt-8">
        <ErrorBoundary resetKey={active} area={NAV.find((n) => n.key === active)?.label}>
          {active === "dashboard" ? (
            <Dashboard onNavigate={setActive} />
          ) : active === "shares" ? (
            <SharesPage />
          ) : active === "docker" ? (
            <DockerPage onOpenApps={() => setActive("apps")} />
          ) : active === "apps" ? (
            <AppsPage />
          ) : active === "vms" ? (
            <VMsPage />
          ) : active === "audit" ? (
            <AuditPage />
          ) : active === "updates" ? (
            <UpdatesPage />
          ) : active === "settings" ? (
            <SettingsPage onOpenAudit={() => setActive("audit")} />
          ) : null}
        </ErrorBoundary>
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
    </TerminalProvider>
  );
}

function ThemeToggle() {
  const { resolved, setMode } = useTheme();
  const next = resolved === "dark" ? "light" : "dark";
  return (
    <IconButton label={`Switch to ${next} theme`} onClick={() => setMode(next)}>
      {resolved === "dark" ? <Sun size={17} /> : <Moon size={17} />}
    </IconButton>
  );
}

function SignOutButton() {
  const { signOut } = useAuth();
  return (
    <IconButton label="Sign out" onClick={() => void signOut()}>
      <LogOut size={17} />
    </IconButton>
  );
}
