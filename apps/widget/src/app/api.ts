// The iframe app's client for `/api/embed` (T-20's surface).
//
// It holds a session token in a closure — never in localStorage, never in a
// cookie — and it holds *only* that. The client key and the HMAC stay in the
// host page, which is where the mint has to happen: T-19's allowlist checks the
// `Origin` of the minting request, and the only document whose origin is the
// tenant's own site is theirs. This frame is served from a CDN and, under its
// sandbox, reports an opaque `null`.
//
// So the frame cannot mint, cannot re-sign, and cannot outlive its token by
// itself. When one expires it says so and waits.

export interface Message {
  id?: string;
  role: "user" | "assistant" | "system";
  content: string;
  created_at?: string;
}

export interface WidgetConfig {
  greeting: string;
  suggested_prompts: string[];
  locale: string;
  agents?: { id: string; name: string; is_default: boolean }[];
}

/** Thrown when the session cannot be minted or has expired. The app turns this
 *  into a `token_expired` event for the host rather than retrying: only the
 *  tenant's backend can sign a new assertion, and a blind retry loop against a
 *  refused signature is how a widget bills a rate limiter forever. */
export class SessionExpired extends Error {}

export class EmbedClient {
  /** The session, handed down by the loader. This class cannot mint one and
   *  deliberately holds nothing that could: no client key, no signature. The
   *  host page mints, because only its origin is the one a tenant allowlisted
   *  — see protocol.ts AuthMessage. */
  constructor(
    private base: string,
    private token: string,
  ) {}

  /** Replace the session after the host has minted a fresh one. */
  setToken(token: string) {
    this.token = token;
  }

  private async call<T>(path: string, init: RequestInit = {}): Promise<T> {
    const token = this.token;
    const res = await fetch(`${this.base}/api/embed${path}`, {
      ...init,
      headers: {
        ...(init.headers ?? {}),
        Authorization: `Bearer ${token}`,
      },
    });
    if (res.status === 401) {
      // The session died mid-flight. The app tells the host, which mints
      // again from a signature that is probably still valid — and only asks
      // the tenant's backend to re-sign if that fails too.
      throw new SessionExpired(await message(res));
    }
    if (!res.ok) {
      throw new Error(await message(res));
    }
    return (await res.json()) as T;
  }

  config(): Promise<WidgetConfig> {
    return this.call<WidgetConfig>("/config");
  }

  currentThread(): Promise<{ thread: { id: string; title?: string; messages?: Message[] } | null }> {
    return this.call("/threads/current");
  }

  send(message: string, threadID?: string, agentID?: string) {
    return this.call<{ thread_id: string; task_id: string; is_new_thread: boolean }>("/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        message,
        ...(threadID ? { thread_id: threadID } : {}),
        ...(agentID ? { agent_id: agentID } : {}),
      }),
    });
  }

  /** Open the event socket for one thread.
   *
   *  The token rides in the query string because a browser cannot set a header
   *  on a WebSocket upgrade — the same exemption the dashboard's own stream
   *  has, under a different parameter name so the two cannot be confused. */
  async socket(threadID: string): Promise<WebSocket> {
    const token = this.token;
    const url = new URL(`${this.base}/api/embed/threads/${threadID}/stream`);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.searchParams.set("et", token);
    return new WebSocket(url.toString());
  }
}

/** Pull a human-readable reason out of a refusal, falling back to the status. */
async function message(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string };
    if (body?.error) return body.error;
  } catch {
    // not JSON
  }
  return `Request failed (${res.status})`;
}
