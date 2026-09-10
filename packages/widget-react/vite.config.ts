import { defineConfig } from "vite";

/**
 * ESM and CJS, and no IIFE (T-22).
 *
 * The base package ships a CDN file because a script tag is a real way to use
 * it. There is no script-tag way to use a React component, so a third output
 * here would be a file with no consumer.
 *
 * React and the loader are both external: bundling React would give a host two
 * copies and the hooks error that follows, and bundling the loader would give
 * them two widgets that do not know about each other.
 */
export default defineConfig({
  build: {
    outDir: "dist",
    target: "es2019",
    sourcemap: true,
    lib: {
      entry: "src/index.tsx",
      formats: ["es", "cjs"],
      fileName: (format) => (format === "cjs" ? "index.cjs" : "index.js"),
    },
    rollupOptions: {
      external: ["react", "react/jsx-runtime", "@argentum/widget"],
    },
  },
});
