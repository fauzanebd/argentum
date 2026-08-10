import { defineConfig } from "vite";

// The loader: one IIFE, no framework, no imports at runtime. It is the file a
// tenant puts in a script tag, so its budget is 15 KB gzipped and its only job
// is to open an iframe and bridge to it.
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: false,
    target: "es2019",
    lib: {
      entry: "src/loader.ts",
      name: "Argentum",
      formats: ["iife"],
      fileName: () => "argentum-widget.js",
    },
  },
});
