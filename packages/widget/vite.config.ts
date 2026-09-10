import { defineConfig } from "vite";

/**
 * The npm package: ESM for a bundler, CJS for a `require` in an older Node
 * toolchain (T-22). The CDN file is `vite.cdn.config.ts`, from a different
 * entry, and the split is not cosmetic — see that file.
 *
 * `target: es2019` is inherited from the loader's own budget: the file is
 * dropped into whatever browser a tenant's customers arrive with, and the
 * transforms newer targets skip are cheaper than the support ticket.
 */
export default defineConfig({
  build: {
    outDir: "dist",
    // The CDN build writes into the same directory and runs second.
    emptyOutDir: true,
    target: "es2019",
    sourcemap: true,
    lib: {
      entry: "src/index.ts",
      name: "Argentum",
      formats: ["es", "cjs"],
      fileName: (format) => (format === "cjs" ? "index.cjs" : "index.js"),
    },
    rollupOptions: {
      // Nothing is external. The loader has no dependencies and must not
      // acquire one: a script tag cannot resolve a bare import, so a dependency
      // here would be a file that works from npm and 404s from the CDN.
      external: [],
    },
  },
});
