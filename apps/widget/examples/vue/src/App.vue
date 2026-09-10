<script setup>
import { onMounted, onUnmounted } from "vue";
import Argentum from "@argentum/widget";

// There is no Vue wrapper package: the loader is plain DOM with a lifecycle of
// exactly two calls, and a component around it would be a second thing to keep
// in step with the app inside the iframe.

// Where the widget's `dist/app` is served from — your CDN copy of it in
// production. Required, not optional, once the loader is bundled: there is no
// script tag left for it to derive the iframe's URL from.
const APP_BASE = "http://localhost:5174/app";

// Ask your own backend who this visitor is. It answers with the public client
// key and a signature over `<ref>:<exp>` — never with the secret.
const identify = () => fetch("/identity", { credentials: "same-origin" }).then((r) => r.json());

onMounted(async () => {
  const identity = await identify();
  Argentum.init({
    clientKey: identity.clientKey,
    apiBase: identity.apiBase,
    appBase: APP_BASE,
    user: identity.user,
    launcher: "bubble",
    position: "bottom-right",
    theme: { primary: "#f25c5c", radius: 12, mode: "auto" },
  });

  // The one event you must handle. A session lasts minutes; when it ends the
  // widget asks the page to re-sign rather than retrying a refusal. `identify`
  // replaces the signature in place, leaving the open conversation alone.
  Argentum.on("token_expired", async () => Argentum.identify((await identify()).user));
});

// The loader appends an iframe and a launcher to <body> and listens on window,
// none of which Vue can unmount for it.
onUnmounted(() => Argentum.destroy());
</script>

<template>
  <h1>A Vue page with a widget on it</h1>
  <p>Everything the widget needs comes from <code>/identity</code>, which your own server signs.</p>
</template>
