/**
 * The harness's own vite config.
 *
 * Separate from the app's because of the two aliases below: `@/lib/api` and
 * `@/store/auth` are replaced by stubs, which is the only way to mount a screen
 * that normally needs a session and a warehouse. Everything else — the `@`
 * alias, the React plugin, `index.css` and therefore every design token — is
 * the app's own, so what is photographed is the product's CSS rather than a
 * lookalike.
 *
 * It does not shell out to `git describe` the way `vite.config.ts` does, for
 * the reason `vitest.config.ts` gives: a screenshot run should not depend on a
 * checkout having tags.
 */
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

const src = path.resolve(__dirname, "../src");

export default defineConfig({
  root: __dirname,
  plugins: [react()],
  resolve: {
    alias: [
      { find: /^@\/lib\/api$/, replacement: path.resolve(__dirname, "./stub-api.ts") },
      { find: /^@\/store\/auth$/, replacement: path.resolve(__dirname, "./stub-auth.ts") },
      { find: /^@\//, replacement: src + "/" },
    ],
  },
  server: { port: 5199, strictPort: true },
});
