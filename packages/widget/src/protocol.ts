// The postMessage vocabulary between a host page's loader and the app running
// inside the iframe (T-21).
//
// Both halves import this file, which is the point: a bridge whose two sides
// keep their own copy of the message names is a bridge that breaks on the
// release where one side adds a field.
//
// **Every message carries the marker.** A page that embeds the widget may also
// embed a payment form, an analytics pixel and a support tool, all posting
// their own messages onto the same window. Without a marker, a `resize` from
// somebody else's iframe resizes ours.

export const MARKER = "argentum-widget";

/** Sent by the iframe app when it has booted and is ready for identity. */
export interface ReadyMessage {
  marker: typeof MARKER;
  type: "ready";
}

/** Host → app. A **minted session token**, not the credential that bought it.
 *
 *  The loader mints, the app spends. That split is not tidiness — it is what
 *  makes T-19's origin allowlist work at all. A session is minted only from an
 *  allowlisted origin, and the only document whose origin *is* the tenant's
 *  site is the host page: the iframe is served from a CDN, and under its
 *  sandbox it has an opaque origin that serialises to `"null"`. A mint called
 *  from inside the frame therefore presents an origin no tenant can ever
 *  allowlist.
 *
 *  It also means the client key and the HMAC never enter the frame. The frame
 *  holds a bearer token that expires in minutes and nothing that could mint
 *  another. */
export interface AuthMessage {
  marker: typeof MARKER;
  type: "auth";
  apiBase: string;
  token: string;
  theme?: WidgetTheme;
  locale?: string;
}

/** App → host. The panel wants to be a different height. */
export interface ResizeMessage {
  marker: typeof MARKER;
  type: "resize";
  height: number;
}

/** Either direction: open/close the panel. The host sends it when its own
 *  trigger is clicked; the app sends it when the user presses Esc or the close
 *  button, so the launcher's state stays in step. */
export interface ToggleMessage {
  marker: typeof MARKER;
  type: "open" | "close";
}

/** App → host. Something the host page may want to react to. `token_expired`
 *  is the one that matters: the host re-signs and sends a fresh `auth`. */
export interface EventMessage {
  marker: typeof MARKER;
  type: "event";
  name: "message" | "error" | "token_expired";
  detail?: unknown;
}

export type WidgetMessage =
  | ReadyMessage
  | AuthMessage
  | ResizeMessage
  | ToggleMessage
  | EventMessage;

export interface WidgetTheme {
  primary?: string;
  radius?: number;
  mode?: "light" | "dark" | "auto";
}

/** True when a MessageEvent is one of ours from the expected origin.
 *
 *  Both halves call this on every message they receive. An unchecked
 *  postMessage handler is a cross-origin hole: any page that can get a
 *  reference to the window can post to it, and a handler that trusts the
 *  payload is a handler that can be driven by a hostile frame. */
export function isWidgetMessage(
  event: MessageEvent,
  expectedOrigin: string,
): event is MessageEvent<WidgetMessage> {
  if (expectedOrigin !== "*" && event.origin !== expectedOrigin) {
    return false;
  }
  const data = event.data as { marker?: string } | null;
  return !!data && typeof data === "object" && data.marker === MARKER;
}
