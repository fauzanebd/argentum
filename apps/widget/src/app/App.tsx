import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { EmbedClient, SessionExpired, type Message, type WidgetConfig } from "./api";
import { renderMarkdown } from "./markdown";
import { MARKER, isWidgetMessage, type WidgetTheme } from "../protocol";

// The app inside the iframe (T-21).
//
// It boots with no credentials at all and asks the host for them: `ready` goes
// up, `auth` comes down. That ordering is what makes the widget work on a page
// whose own session is still loading — the alternative, reading options off the
// iframe URL, would put a signature in a browser history entry and a server
// access log.

interface Bubble {
  role: "user" | "assistant";
  content: string;
  pending?: boolean;
  tools?: string[];
  failed?: boolean;
}

/** The worker's event vocabulary, verbatim from internal/app/event_bus.go.
 *  Unknown types are ignored rather than treated as errors: the contract is
 *  additive and a widget in the wild is older than the server it talks to. */
interface ChatEvent {
  type: string;
  content?: string;
  tool_call?: { name?: string };
  error?: string;
}

export function App() {
  const [client, setClient] = useState<EmbedClient | null>(null);
  const [config, setConfig] = useState<WidgetConfig | null>(null);
  const [bubbles, setBubbles] = useState<Bubble[]>([]);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [degraded, setDegraded] = useState(false);
  const [error, setError] = useState("");

  const threadID = useRef("");
  const socket = useRef<WebSocket | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const composer = useRef<HTMLInputElement>(null);
  const hostOrigin = useRef("*");

  const post = useCallback((message: Record<string, unknown>) => {
    parent.postMessage({ marker: MARKER, ...message }, hostOrigin.current);
  }, []);

  // Boot: announce, then wait for identity.
  useEffect(() => {
    function onMessage(event: MessageEvent) {
      // The host's origin is not known until it speaks: the iframe cannot read
      // `document.referrer` reliably under a sandbox, and the embed key's
      // allowlist — checked server-side when the session is minted — is the
      // real control. What this does buy is that once one origin has spoken,
      // nothing else can.
      if (hostOrigin.current !== "*" && !isWidgetMessage(event, hostOrigin.current)) return;
      if (!isWidgetMessage(event, "*")) return;
      if (event.source !== parent) return;

      const msg = event.data;
      if (msg.type === "auth") {
        hostOrigin.current = event.origin;
        applyTheme(msg.theme);
        // A fresh token for an existing client is a re-mint after expiry, not a
        // new visitor: keep the client (and the conversation on screen) and
        // swap the credential underneath it.
        setClient((prev) => {
          if (prev) {
            prev.setToken(msg.token);
            return prev;
          }
          return new EmbedClient(msg.apiBase, msg.token);
        });
      }
      if (msg.type === "open") {
        composer.current?.focus();
      }
    }

    window.addEventListener("message", onMessage);
    post({ type: "ready" });
    return () => window.removeEventListener("message", onMessage);
  }, [post]);

  // Esc closes, which is the shortcut every panel in every product has.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") post({ type: "close" });
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [post]);

  // Once identified: config and any conversation already in progress.
  useEffect(() => {
    if (!client) return;
    let live = true;

    client
      .config()
      .then((c) => live && setConfig(c))
      .catch(() => live && setConfig({ greeting: "", suggested_prompts: [], locale: "en" }));

    client
      .currentThread()
      .then((res) => {
        if (!live || !res.thread) return;
        threadID.current = res.thread.id;
        setBubbles(
          (res.thread.messages ?? [])
            .filter((m: Message) => m.role === "user" || m.role === "assistant")
            .map((m: Message) => ({ role: m.role as "user" | "assistant", content: m.content })),
        );
      })
      .catch((e: unknown) => live && handleFailure(e));

    return () => {
      live = false;
      socket.current?.close();
    };
  }, [client]);

  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight, behavior: "smooth" });
  }, [bubbles]);

  function handleFailure(e: unknown) {
    if (e instanceof SessionExpired) {
      // The host has to re-sign; we cannot. Telling it is the whole recovery
      // path, and a widget that retried instead would loop against a refusal.
      post({ type: "event", name: "token_expired" });
      setError("Session expired. Reloading identity…");
      return;
    }
    const msg = e instanceof Error ? e.message : "Something went wrong.";
    setError(msg);
    post({ type: "event", name: "error", detail: msg });
  }

  function patchPending(patch: (b: Bubble) => Bubble) {
    setBubbles((prev) => {
      const next = [...prev];
      const last = next.length - 1;
      if (last >= 0 && next[last].role === "assistant") next[last] = patch(next[last]);
      return next;
    });
  }

  async function attach(id: string) {
    if (!client) return;
    socket.current?.close();
    const ws = await client.socket(id);
    socket.current = ws;

    ws.onopen = () => setDegraded(false);
    ws.onclose = () => {
      // Only a surprise close is degraded: one we asked for during teardown is
      // not something to tell the user about.
      if (socket.current === ws) setDegraded(true);
    };
    ws.onerror = () => setDegraded(true);
    ws.onmessage = (frame) => {
      let evt: ChatEvent;
      try {
        evt = JSON.parse(frame.data as string) as ChatEvent;
      } catch {
        return;
      }
      switch (evt.type) {
        case "delta":
          patchPending((b) => ({ ...b, content: b.content + (evt.content ?? "") }));
          break;
        case "tool_call":
          if (evt.tool_call?.name) {
            const tool = evt.tool_call.name;
            patchPending((b) => ({ ...b, tools: [...(b.tools ?? []), tool] }));
          }
          break;
        case "final":
          patchPending((b) => ({
            ...b,
            content: evt.content || b.content,
            pending: false,
          }));
          setSending(false);
          post({ type: "event", name: "message" });
          break;
        case "error":
          patchPending((b) => ({
            ...b,
            content: evt.error || "The agent stopped before answering.",
            pending: false,
            failed: true,
          }));
          setSending(false);
          break;
      }
    };
  }

  async function send(text: string) {
    const message = text.trim();
    if (!message || sending || !client) return;

    setError("");
    setDraft("");
    setSending(true);
    setBubbles((prev) => [
      ...prev,
      { role: "user", content: message },
      { role: "assistant", content: "", pending: true, tools: [] },
    ]);

    try {
      const res = await client.send(message, threadID.current || undefined);
      // The socket is opened *after* the send rather than before: until a turn
      // exists there is no thread to attach to, and a widget that opened one on
      // mount would hold a connection per page view for every visitor who never
      // typed anything.
      if (res.thread_id !== threadID.current) {
        threadID.current = res.thread_id;
        await attach(res.thread_id);
      } else if (!socket.current || socket.current.readyState > WebSocket.OPEN) {
        await attach(res.thread_id);
      }
    } catch (e) {
      handleFailure(e);
      patchPending((b) => ({
        ...b,
        content: e instanceof Error ? e.message : "Could not send that.",
        pending: false,
        failed: true,
      }));
      setSending(false);
    }
  }

  const prompts = config?.suggested_prompts ?? [];

  return (
    <div class="wrap">
      <header class="head">
        <span class="title">{config?.greeting ? "Ask" : "Argentum"}</span>
        <button
          class="icon"
          aria-label="Close chat"
          onClick={() => post({ type: "close" })}
        >
          ✕
        </button>
      </header>

      <div class="log" ref={scroller} role="log" aria-live="polite" aria-atomic="false">
        {bubbles.length === 0 && (
          <div class="empty">
            <p>{config?.greeting || "Ask me about your data."}</p>
            {prompts.map((p) => (
              <button key={p} class="prompt" onClick={() => send(p)}>
                {p}
              </button>
            ))}
          </div>
        )}

        {bubbles.map((b, i) => (
          <div key={i} class={`row ${b.role}`}>
            <div class={`bubble ${b.failed ? "failed" : ""}`}>
              {(b.tools?.length ?? 0) > 0 && (
                <div class="tools">
                  {[...new Set(b.tools)].map((t) => (
                    <span key={t} class="tool">
                      {t}
                    </span>
                  ))}
                </div>
              )}
              {b.role === "assistant" && !b.failed ? (
                <div
                  class="md"
                  // Sanitised in markdown.ts: parse → DOMPurify → insert. The
                  // content is model output over tenant data and is treated as
                  // hostile.
                  dangerouslySetInnerHTML={{ __html: renderMarkdown(b.content) }}
                />
              ) : (
                <div class="plain">{b.content}</div>
              )}
              {b.pending && <span class="caret">▊</span>}
            </div>
          </div>
        ))}
      </div>

      {degraded && (
        <div class="notice">Connection lost — your answer may not appear here.</div>
      )}
      {error && <div class="notice error">{error}</div>}

      <form
        class="composer"
        onSubmit={(e) => {
          e.preventDefault();
          send(draft);
        }}
      >
        <input
          ref={composer}
          value={draft}
          disabled={!client || sending}
          placeholder={sending ? "Thinking…" : "Ask about your data…"}
          aria-label="Message"
          onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
        />
        <button type="submit" disabled={!client || sending || !draft.trim()} aria-label="Send">
          ➤
        </button>
      </form>
    </div>
  );
}

/** Theme arrives from the host as three values and lands as CSS variables. The
 *  widget has no design system of its own on purpose: it renders inside
 *  somebody else's product and the only opinions it should hold are the ones
 *  the tenant set. */
function applyTheme(theme?: WidgetTheme) {
  const root = document.documentElement;
  if (theme?.primary) root.style.setProperty("--argentum-primary", theme.primary);
  if (typeof theme?.radius === "number") {
    root.style.setProperty("--argentum-radius", `${theme.radius}px`);
  }
  const mode =
    theme?.mode === "auto" || !theme?.mode
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light"
      : theme.mode;
  root.dataset.theme = mode;
}
