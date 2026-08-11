// The loader (T-21): the file a tenant drops into a script tag.
//
// It has a hard budget of 15 KB gzipped, so there is no framework in here and
// there will not be one. Everything it does is DOM: an iframe, a launcher
// button, a postMessage bridge, and the option object those three read.
//
// **Why an iframe rather than mounting into the host DOM.** CSS isolation (the
// host's Tailwind or Bootstrap cannot break us and we cannot break their page),
// JS isolation, and a real origin boundary around the session token. The cost
// is bridging sizing and open/close over postMessage, which is mechanical and
// is most of this file.

import { MARKER, isWidgetMessage, type WidgetMessage, type WidgetTheme } from "./protocol";

interface InitOptions {
  clientKey: string;
  user: { ref: string; name?: string; exp: number; sig: string };
  /** The Argentum API. Required — a self-hosted deployment is the common case
   *  and a default pointing at ours would silently send a tenant's questions to
   *  the wrong company's API. */
  apiBase: string;
  /** Where the iframe app is served from. Defaults to the directory this
   *  script was loaded from, so a tenant who copies `dist/` to their own CDN
   *  gets a working widget without configuring a second URL. */
  appBase?: string;
  launcher?: "bubble" | "none";
  position?: "bottom-right" | "bottom-left";
  theme?: WidgetTheme;
  locale?: string;
}

type EventName = "ready" | "open" | "close" | "message" | "error" | "token_expired";
type Handler = (detail?: unknown) => void;

const PANEL_WIDTH = 400;
const PANEL_HEIGHT = 620;
/** Under this width the panel is a full-screen sheet: a 400px panel on a 380px
 *  phone is a panel with the page peeking out behind it. */
const MOBILE_MAX = 640;

let opts: InitOptions | null = null;
let frame: HTMLIFrameElement | null = null;
let launcher: HTMLButtonElement | null = null;
let appOrigin = "";
let ready = false;
let open = false;
const handlers: Record<string, Handler[]> = {};

/** The origin the iframe will run on, derived from where this script came
 *  from. `document.currentScript` is read at module scope on purpose — it is
 *  null by the time any callback runs. */
const scriptSrc = (document.currentScript as HTMLScriptElement | null)?.src ?? "";

function emit(name: EventName, detail?: unknown) {
  for (const h of handlers[name] ?? []) {
    try {
      h(detail);
    } catch {
      // A throwing host handler must not take the widget down with it.
    }
  }
}

// The origin a sandboxed frame reports. The loader sandboxes the iframe
// without `allow-same-origin`, which gives it an *opaque* origin — so every
// message it sends arrives with `event.origin === "null"`, not with the host it
// was served from, and `postMessage` to it must target `"*"` because an opaque
// origin matches no specific target.
//
// **What authenticates the bridge is therefore not the origin string.** It is
// `event.source === frame.contentWindow`: a reference only this loader holds,
// to a frame only this loader created. That check is strictly stronger than an
// origin comparison, and it runs first on every message.
const SANDBOXED_ORIGIN = "null";

function post(message: WidgetMessage) {
  // `"*"` and not appOrigin: the recipient has an opaque origin, so a specific
  // targetOrigin never matches and the message is silently dropped. The only
  // document that can receive this is the frame we hold a handle to.
  frame?.contentWindow?.postMessage(message, "*");
}

/** Mint a session and hand the frame the token.
 *
 *  **This runs in the host page on purpose.** The mint is the one request whose
 *  `Origin` header has to be the tenant's own site, because that is what T-19's
 *  allowlist checks — and the iframe cannot produce it: it is served from a CDN
 *  and, under its sandbox, reports an opaque `null` origin that no allowlist can
 *  contain. Doing it here also keeps the client key and the signature out of the
 *  frame entirely; what crosses the bridge is a token that expires in minutes.
 */
async function sendAuth() {
  if (!opts || !frame) return;
  const apiBase = opts.apiBase.replace(/\/+$/, "");
  try {
    const res = await fetch(`${apiBase}/api/embed/session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        client_key: opts.clientKey,
        user_ref: opts.user.ref,
        exp: opts.user.exp,
        signature: opts.user.sig,
      }),
    });
    if (!res.ok) {
      // 401 is a stale or wrong signature, 403 an origin nobody allowlisted.
      // Both are the host page's to fix, and neither is retryable from here —
      // only the tenant's backend can sign a new assertion.
      const detail = await res.text().catch(() => "");
      emit(res.status === 403 ? "error" : "token_expired", detail || res.status);
      return;
    }
    const body = (await res.json()) as { token: string };
    post({
      marker: MARKER,
      type: "auth",
      apiBase,
      token: body.token,
      theme: opts.theme,
      locale: opts.locale,
    });
  } catch (e) {
    emit("error", String(e));
  }
}

function appURL(o: InitOptions): string {
  if (o.appBase) return o.appBase.replace(/\/+$/, "") + "/index.html";
  if (scriptSrc) return scriptSrc.replace(/\/[^/]*$/, "") + "/app/index.html";
  // Nothing to derive from: a script tag with no src (inlined by a bundler)
  // and no appBase. Fail loudly rather than pointing an iframe at the host
  // page's own root, which would render the tenant's site inside itself.
  throw new Error("Argentum: appBase is required when the loader is inlined");
}

function styleFrame(el: HTMLIFrameElement, o: InitOptions) {
  const mobile = window.innerWidth <= MOBILE_MAX;
  const side = o.position === "bottom-left" ? "left" : "right";
  const s = el.style;
  s.position = "fixed";
  s.border = "0";
  s.zIndex = "2147483000";
  s.colorScheme = "normal";
  s.display = open ? "block" : "none";
  s.boxShadow = "0 12px 40px rgba(0,0,0,.18)";
  s.borderRadius = mobile ? "0" : `${o.theme?.radius ?? 12}px`;
  if (mobile) {
    s.inset = "0";
    s.width = "100%";
    s.height = "100%";
    // Cleared, not merely unset: the desktop branch below sets a max-height,
    // and a viewport that crosses the breakpoint the other way (a rotation, a
    // window drag) would otherwise keep it — leaving the "full-screen" sheet
    // 120px short of the screen with no obvious cause. Found by the browser
    // gate 2026-08-10, exercising the branch directly.
    s.maxHeight = "";
  } else {
    s.inset = "auto";
    s.bottom = "88px";
    s[side] = "20px";
    s.width = `${PANEL_WIDTH}px`;
    s.height = `${PANEL_HEIGHT}px`;
    s.maxHeight = "calc(100vh - 120px)";
  }
}

function makeLauncher(o: InitOptions): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.setAttribute("aria-label", "Open chat");
  b.setAttribute("aria-expanded", "false");
  const side = o.position === "bottom-left" ? "left" : "right";
  Object.assign(b.style, {
    position: "fixed",
    bottom: "20px",
    [side]: "20px",
    width: "56px",
    height: "56px",
    borderRadius: "50%",
    border: "0",
    cursor: "pointer",
    zIndex: "2147483000",
    // color.primary from packages/design-tokens/tokens.json. A tenant accent
    // still wins; this is the fallback for one that has set none, and until
    // T-U9 it was #e11d48 — a red matching no token in the system, on the one
    // element a visitor sees before they have opened anything.
    background: o.theme?.primary ?? "#f25c5c",
    color: "#fff",
    fontSize: "24px",
    lineHeight: "1",
    boxShadow: "0 6px 20px rgba(0,0,0,.2)",
  } as Partial<CSSStyleDeclaration>);
  b.textContent = "💬";
  b.addEventListener("click", () => api.toggle());
  return b;
}

function onMessage(event: MessageEvent) {
  // The source check first, because it is the one that authenticates: only the
  // frame this loader created can be `frame.contentWindow`.
  if (!frame || event.source !== frame.contentWindow) return;
  // Then the origin, accepting the opaque `"null"` a sandboxed frame reports as
  // well as the host it was served from — the latter only matters if somebody
  // later relaxes the sandbox. Anything else is refused even from our own
  // frame's window handle.
  const expected = event.origin === SANDBOXED_ORIGIN ? SANDBOXED_ORIGIN : appOrigin;
  if (!isWidgetMessage(event, expected)) return;

  const msg = event.data;
  switch (msg.type) {
    case "ready":
      ready = true;
      void sendAuth();
      emit("ready");
      break;
    case "close":
      api.close();
      break;
    case "open":
      api.open();
      break;
    case "resize":
      if (frame && window.innerWidth > MOBILE_MAX) {
        const h = Math.min(Math.max(msg.height, 320), window.innerHeight - 120);
        frame.style.height = `${h}px`;
      }
      break;
    case "event":
      if (msg.name === "token_expired") {
        // The frame's token ran out. The signature the host gave us may still
        // be good — it lives up to 24h and the session only 15 minutes — so
        // mint again first. sendAuth emits `token_expired` to the host itself
        // if the signature is the thing that expired, which is the only case
        // where the page has to re-sign.
        void sendAuth();
      } else {
        emit(msg.name, msg.detail);
      }
      break;
  }
}

function onResize() {
  if (frame && opts) styleFrame(frame, opts);
}

const api = {
  init(options: InitOptions) {
    if (frame) api.destroy();
    if (!options?.clientKey || !options?.user?.sig || !options?.apiBase) {
      throw new Error("Argentum: clientKey, apiBase and a signed user are required");
    }
    opts = options;

    const url = appURL(options);
    appOrigin = new URL(url, location.href).origin;

    frame = document.createElement("iframe");
    frame.src = url;
    frame.title = "Argentum chat";
    // No allow-same-origin: the app needs no access to its own origin's
    // storage, and withholding it means a compromised widget cannot read
    // anything persisted by whatever else is hosted there.
    frame.setAttribute("sandbox", "allow-scripts allow-forms");
    styleFrame(frame, options);
    document.body.appendChild(frame);

    if (options.launcher !== "none") {
      launcher = makeLauncher(options);
      document.body.appendChild(launcher);
    }

    window.addEventListener("message", onMessage);
    window.addEventListener("resize", onResize);
    return api;
  },

  open() {
    if (!frame) return;
    open = true;
    frame.style.display = "block";
    launcher?.setAttribute("aria-expanded", "true");
    post({ marker: MARKER, type: "open" });
    emit("open");
  },

  close() {
    if (!frame) return;
    open = false;
    frame.style.display = "none";
    launcher?.setAttribute("aria-expanded", "false");
    post({ marker: MARKER, type: "close" });
    emit("close");
  },

  toggle() {
    if (open) api.close();
    else api.open();
  },

  /** Re-sign. The host calls this when its own token is about to expire, or in
   *  response to `token_expired`. */
  identify(user: InitOptions["user"]) {
    if (!opts) return;
    opts.user = user;
    if (ready) void sendAuth();
  },

  destroy() {
    window.removeEventListener("message", onMessage);
    window.removeEventListener("resize", onResize);
    frame?.remove();
    launcher?.remove();
    frame = null;
    launcher = null;
    opts = null;
    ready = false;
    open = false;
  },

  on(name: EventName, handler: Handler) {
    (handlers[name] ??= []).push(handler);
    return api;
  },
};

declare global {
  interface Window {
    Argentum: typeof api;
  }
}

window.Argentum = api;

export default api;
