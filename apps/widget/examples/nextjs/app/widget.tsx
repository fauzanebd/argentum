"use client";

// A client component because the loader is DOM from its first line: an iframe,
// a launcher, a window listener. It imports safely during the server render —
// it guards `document` — but it only does anything after hydration.

import { useEffect, useState } from "react";
import { ArgentumWidget, type InitOptions } from "@argentum/widget-react";

type Identity = Pick<InitOptions, "clientKey" | "apiBase" | "user">;

// Where the widget's `dist/app` is served from — your CDN copy of it in
// production. Required, not optional, once the loader is bundled: there is no
// script tag left for it to derive the iframe's URL from.
const APP_BASE = "http://localhost:5174/app";

// Ask your own backend who this visitor is. It answers with the public client
// key and a signature over `<ref>:<exp>` — never with the secret.
const identify = (): Promise<Identity> =>
  fetch("/api/identity", { credentials: "same-origin" }).then((r) => r.json());

export default function Widget() {
  const [identity, setIdentity] = useState<Identity | null>(null);

  useEffect(() => {
    identify().then(setIdentity);
  }, []);

  if (!identity) return null;

  return (
    <ArgentumWidget
      clientKey={identity.clientKey}
      apiBase={identity.apiBase}
      appBase={APP_BASE}
      user={identity.user}
      launcher="bubble"
      position="bottom-right"
      theme={{ primary: "#f25c5c", radius: 12, mode: "auto" }}
      // The one prop you must handle. A session lasts minutes; when it ends the
      // widget asks the page to re-sign rather than retrying a refusal. The new
      // signature arrives as a prop, so the component re-identifies instead of
      // tearing the conversation down.
      onTokenExpired={async () => setIdentity(await identify())}
    />
  );
}
