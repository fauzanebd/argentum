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
//
// **Every shape below is generated from the Go that serves it**
// (`internal/transport/http/embedwire`, via `make types`). It was hand-written
// until 2026-09-10, and both of the interfaces that used to sit here were
// wrong: `WidgetConfig` described the *inner* object of a `{config, agents}`
// envelope, so the tenant's greeting and starter prompts read `undefined` on
// every load since T-23 shipped; and `Message` described four fields of a row
// the route was serving whole, including the agent's tool digests. A generated
// type would have refused to compile against the first and made the second
// visible. That is the whole argument for T-02b, arriving late in the one app
// that was never made a consumer of it.

export type {
  Agent,
  ConfigResponse,
  CurrentThreadResponse,
  ErrorResponse,
  Message,
  MessagesResponse,
  SendResponse,
  Thread,
} from "@argentum/api-types/embed";

// The tenant's own appearance and content (T-23). It lives in `domain` rather
// than in the embed contract because Settings → Widget writes the same struct
// the widget reads, and two generated copies of one shape is the defect this
// file just stopped committing.
export type { WidgetConfig } from "@argentum/api-types";

import type {
  ConfigResponse,
  CurrentThreadResponse,
  ErrorResponse,
  SendResponse,
} from "@argentum/api-types/embed";

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

  /** The tenant's appearance and content, and the roster the picker offers.
   *
   *  Returns the envelope, not the config: `agents` is the live roster and sits
   *  beside `config` rather than inside it. The caller reads `.config`, which is
   *  the line this method existed for three weeks without. */
  config(): Promise<ConfigResponse> {
    return this.call<ConfigResponse>("/config");
  }

  currentThread(): Promise<CurrentThreadResponse> {
    return this.call<CurrentThreadResponse>("/threads/current");
  }

  send(message: string, threadID?: string, agentID?: string) {
    return this.call<SendResponse>("/chat", {
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
    const body = (await res.json()) as Partial<ErrorResponse>;
    if (body?.error) return body.error;
  } catch {
    // not JSON
  }
  return `Request failed (${res.status})`;
}
