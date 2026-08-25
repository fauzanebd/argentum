import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { execSync } from "node:child_process";
import path from "path";

// The build identity shown in the sidebar footer.
//
// `release.yaml` only tags on `apps/backend/**`, so there is no dashboard
// version to read — a frontend-only deploy mints no tag and never bumps
// package.json (still 0.1.0). What identifies a dashboard deploy is the commit
// it was built from, which `git describe` names relative to the newest backend
// tag: `v0.24.0-5-gf55ac04`.
//
// Cloudflare Pages is the deploy path and its checkout may arrive without tags,
// in which case `--always` degrades to the bare SHA on its own. If there is no
// git at all, Pages still names the commit in the build environment.
function buildId(): string {
  try {
    const described = execSync("git describe --tags --always --dirty", {
      cwd: __dirname,
      stdio: ["ignore", "pipe", "ignore"],
    })
      .toString()
      .trim();
    if (described) return described;
  } catch {
    // fall through to the Pages environment
  }
  const sha = process.env.CF_PAGES_COMMIT_SHA;
  return sha ? sha.slice(0, 7) : "dev";
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.VITE_API_BASE_URL || "http://localhost:8080";

  return {
    plugins: [react()],
    define: {
      __APP_VERSION__: JSON.stringify(buildId()),
      __BUILD_DATE__: JSON.stringify(new Date().toISOString()),
    },
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: {
      port: 5173,
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
          ws: true,
        },
        "/webhook": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
        },
        // The report player's data route (T-V4). It is not under `/api`
        // because it authenticates nobody, which means it needs its own proxy
        // entry here for the same reason — the page is `/s/:token` and the
        // data it fetches is `/share/:token`, two different prefixes on
        // purpose so the SPA route and the API route cannot collide.
        "/share": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
        },
      },
    },
  };
});
