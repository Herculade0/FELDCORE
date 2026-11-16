// Thin client for the daemon's JSON API.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Fired on window when the server says the session is no longer valid. */
export const UNAUTHORIZED_EVENT = "feldcore:unauthorized";

interface Options {
  method?: "GET" | "POST" | "PUT" | "DELETE";
  body?: unknown;
  /** Don't treat a 401 as "session expired" (used by the sign-in form). */
  quiet401?: boolean;
}

export async function api<T>(path: string, opts: Options = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, {
      method: opts.method ?? "GET",
      credentials: "same-origin",
      headers: opts.body !== undefined ? { "Content-Type": "application/json" } : undefined,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
  } catch {
    throw new ApiError(0, "Can't reach FELDCORE. Check that the server is running.");
  }

  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    /* empty or non-JSON body */
  }

  if (!res.ok) {
    if (res.status === 401 && !opts.quiet401) {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    }
    const msg =
      data && typeof data === "object" && "error" in data && typeof data.error === "string"
        ? data.error
        : `The request failed (${res.status}).`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

/** Message suitable for showing to the user, whatever was thrown. */
export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : "Something went wrong.";
}
