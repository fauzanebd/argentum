/**
 * The workspace's first test runner (backlog: "Frontend test framework").
 *
 * **Why this file rather than a `test` block inside `vite.config.ts`.** That
 * config computes a build identity by shelling out to `git describe` at module
 * load. Useful for a build, wrong for a test run: it makes every `vitest`
 * invocation depend on a git checkout with tags, which is exactly the kind of
 * ambient dependency that turns a green suite red on somebody else's machine.
 * This config carries only what a test needs — the React plugin, the `@` alias,
 * and a DOM where one is asked for.
 *
 * `environment` defaults to node rather than jsdom so a pure unit test of a
 * module like `lib/contrast.ts` does not pay for a DOM. A file that needs one
 * opts in with `// @vitest-environment jsdom` on its first line.
 */
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
  test: {
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "node",
  },
});
