import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { ArgentumWidget } from "@argentum/widget-react";

// Where the widget's `dist/app` is served from — your CDN copy of it in
// production. Required, not optional, once the loader is bundled: there is no
// script tag left for it to derive the iframe's URL from.
const APP_BASE = "http://localhost:5174/app";

// Ask your own backend who this visitor is. It answers with the public client
// key and a signature over `<ref>:<exp>` — never with the secret.
const identify = () => fetch("/identity", { credentials: "same-origin" }).then((r) => r.json());

function App() {
  const [identity, setIdentity] = useState(null);

  useEffect(() => {
    identify().then(setIdentity);
  }, []);

  return (
    <>
      <h1>A React page with a widget on it</h1>
      <p>Everything the widget needs comes from <code>/identity</code>, which your own server signs.</p>
      {identity && (
        <ArgentumWidget
          clientKey={identity.clientKey}
          apiBase={identity.apiBase}
          appBase={APP_BASE}
          user={identity.user}
          launcher="bubble"
          position="bottom-right"
          theme={{ primary: "#f25c5c", radius: 12, mode: "auto" }}
          // The one prop you must handle. A session lasts minutes; when it ends
          // the widget asks the page to re-sign rather than retrying a refusal.
          // The new signature arrives as a prop, so the component re-identifies
          // instead of tearing the conversation down.
          onTokenExpired={async () => setIdentity(await identify())}
        />
      )}
    </>
  );
}

createRoot(document.getElementById("root")).render(<App />);
