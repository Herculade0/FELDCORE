import { Component, type ErrorInfo, type ReactNode } from "react";
import { RotateCcw, TriangleAlert } from "lucide-react";

interface Props {
  children: ReactNode;
  /** Changing this value resets the boundary (e.g. when the page changes). */
  resetKey?: unknown;
  /** Short label for where the error happened, e.g. "Docker". */
  area?: string;
}

interface State {
  error: Error | null;
}

/**
 * Catches render errors in its subtree so a single bug shows a recoverable
 * message instead of blanking the whole app. Wrap each page in one, keyed by
 * the active page, so navigating away clears a crashed page.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidUpdate(prev: Props) {
    // Reset when the caller signals a context change (e.g. page switch).
    if (prev.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("FELDCORE UI error", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex min-h-[60vh] items-center justify-center">
        <div className="glass flex max-w-md flex-col items-center rounded-3xl px-8 py-10 text-center">
          <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-bad/15 text-bad">
            <TriangleAlert size={26} />
          </span>
          <h2 className="mt-5 text-lg font-extrabold text-ink">
            {this.props.area ? `The ${this.props.area} page hit a problem` : "Something went wrong"}
          </h2>
          <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
            This is a bug in FELDCORE, not something you did. The rest of the app still works.
          </p>
          <button
            onClick={() => this.setState({ error: null })}
            className="accent-gradient mt-6 inline-flex items-center gap-2 rounded-xl px-4 py-2.5 text-sm font-bold text-white"
          >
            <RotateCcw size={15} />
            Try again
          </button>
          <p className="mt-4 break-words font-mono text-[11px] text-ink-3">
            {this.state.error.message}
          </p>
        </div>
      </div>
    );
  }
}
