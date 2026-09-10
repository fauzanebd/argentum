import { defineConfig } from "vite";

/**
 * The CDN file, built from `loader.ts` rather than from `index.ts` (T-22).
 *
 * **This split is load-bearing and was found by building it the other way.**
 * `index.ts` has named exports beside the default, and rollup's IIFE wrapper
 * turns a module with named exports into `var Argentum = { default, MARKER,
 * isWidgetMessage, … }` — so `Argentum.init(...)`, the call in every script tag
 * this product has ever documented, becomes `undefined is not a function`. The
 * loader's own `window.Argentum = api` does not save it either: the wrapper's
 * `var Argentum` *is* `window.Argentum`, and it is assigned last.
 *
 * `loader.ts` has exactly one runtime export — the default — so the wrapper
 * assigns the api itself and the script tag keeps working. The npm entry keeps
 * its named exports, which is what a bundler consumer wants.
 */
export default defineConfig({
  build: {
    outDir: "dist",
    // Second of the two builds; emptying here would delete the ESM/CJS output.
    emptyOutDir: false,
    target: "es2019",
    sourcemap: true,
    lib: {
      entry: "src/loader.ts",
      name: "Argentum",
      formats: ["iife"],
      // The name the CDN path has always served and the vanilla example
      // already references; renaming it would break every page that copied the
      // script tag out of the embed guide.
      fileName: () => "argentum-widget.js",
    },
  },
});
