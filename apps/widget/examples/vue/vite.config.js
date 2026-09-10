import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  // Proxied rather than fetched cross-origin so `/identity` is same-origin in
  // the browser and carries the session cookie your real app authenticates it
  // with. In your project this route is simply part of your own server.
  server: { proxy: { "/identity": "http://localhost:4321" } },
});
