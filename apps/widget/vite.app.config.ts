import { writeFileSync } from "node:fs";
import { defineConfig } from "vite";

// The app that runs inside the iframe.
//
// **It builds as a classic script, not an ES module, and that is load-bearing.**
// The loader sandboxes the frame `allow-scripts allow-forms` — deliberately
// without `allow-same-origin`, so a compromised widget cannot read anything
// else stored on the origin it is served from. A sandboxed frame without that
// flag has an *opaque* origin, and a `<script type="module">` is fetched under
// CORS with `Origin: null`. No static host or CDN answers that with
// `Access-Control-Allow-Origin`, so the module is blocked and the panel opens
// blank — with no console error the host page can see, because the failure is
// inside a frame it cannot read.
//
// A classic script is fetched no-CORS and simply works. Found by the browser
// gate on 2026-08-10: every unit test passed, both builds were clean, and the
// panel had never actually been opened.
//
// `base: "./"` for the same class of reason: Vite's default emits root-absolute
// asset URLs, which 404 anywhere but a domain root — and the CDN path T-22
// specifies is `/widget/v1/`.
export default defineConfig({
  base: "./",
  esbuild: { jsx: "automatic", jsxImportSource: "preact" },
  resolve: {
    // Nothing here imports React; the alias exists so a dependency that does
    // resolves to Preact's shim instead of pulling a second framework into a
    // bundle with an 80 KB budget.
    alias: { react: "preact/compat", "react-dom": "preact/compat" },
  },
  build: {
    outDir: "dist/app",
    emptyOutDir: true,
    target: "es2019",
    cssCodeSplit: false,
    lib: {
      entry: "src/app/main.tsx",
      name: "ArgentumApp",
      formats: ["iife"],
      fileName: () => "app.js",
    },
  },
  plugins: [
    {
      // The iframe's own document. Written here rather than processed by Vite's
      // HTML pipeline, because that pipeline is what injects `type="module"` —
      // the exact attribute this file exists to avoid.
      name: "argentum-iframe-html",
      closeBundle() {
        writeFileSync(
          "dist/app/index.html",
          `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
    <!-- No indexing: this page is a component, not a destination. -->
    <meta name="robots" content="noindex, nofollow" />
    <title>Argentum</title>
    <link rel="stylesheet" href="./style.css" />
  </head>
  <body>
    <div id="root"></div>
    <!-- Classic script, never type="module" — see vite.app.config.ts. -->
    <script src="./app.js"></script>
  </body>
</html>
`,
        );
      },
    },
  ],
});
