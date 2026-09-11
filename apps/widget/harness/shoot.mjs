/**
 * Photographs the widget a tenant's visitor actually gets.
 *
 * `pnpm --filter @argentum/widget-app shots`. It serves the **built** bundle
 * from `dist/app` rather than a dev server, inside a sandboxed iframe on a host
 * page, and fakes `/api/embed` at the network. Three properties of the real
 * thing survive that the dashboard harness's module stubs could not:
 *
 * 1. The frame is sandboxed without `allow-same-origin`, so it runs on an
 *    opaque origin — the condition that made a `type="module"` bundle open
 *    blank on 2026-08-10, with no console error the host page could read.
 * 2. The bundle is the IIFE `vite.app.config.ts` emits, not source modules.
 * 3. `EmbedClient` really parses the response. The month-long defect this arm
 *    is owed for was a client reading `{config, agents}` as though it were the
 *    config; a stub of that client would have stubbed out the bug.
 *
 * What it still does not prove is the server: these bodies are written here, so
 * nothing here says `/api/embed/config` really answers this shape or that the
 * transcript route really drops the agent's tool rows. That is the second half
 * of `T-23`'s gate row and it stays owed — it needs the stack and a tenant.
 */
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "playwright-core";
import {
  CONFIG_CONFIGURED,
  CONFIG_DEFAULT,
  NO_THREAD,
  RETURNING_THREAD,
} from "./fixtures.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const appDir = path.join(here, "..", "dist", "app");
const out = path.resolve(here, "../../../docs/coverage/assets");

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
};

/** Serves the host page at `/` and the built bundle under `/app/`. Deliberately
 *  not vite: what a visitor loads is the artifact, and a dev server would
 *  transform it into something no tenant is ever served. */
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  try {
    const file =
      url.pathname === "/" || url.pathname === "/index.html"
        ? path.join(here, "host.html")
        : path.join(appDir, url.pathname.replace(/^\/app\//, ""));
    const body = await readFile(file);
    res.writeHead(200, { "content-type": TYPES[path.extname(file)] ?? "application/octet-stream" });
    res.end(body);
  } catch {
    res.writeHead(404).end("not found");
  }
});
await new Promise((r) => server.listen(0, r));
const base = `http://localhost:${server.address().port}`;

const SCENES = [
  {
    file: "widget-empty-configured.png",
    config: CONFIG_CONFIGURED,
    thread: NO_THREAD,
    // The arm, stated as an assertion rather than left to the eye: the
    // tenant's own words have to be on screen, and Argentum's default must not
    // be. Both directions matter — the failure mode was a *silent* fallback,
    // and a shot of a screen reading "Ask me about your data." would have
    // looked perfectly fine for a month.
    expect: {
      // The last two are the chrome, which `locale` is documented to decide
      // (`domain/widget_config.go:30` — "the label on the composer"). A tenant
      // on `id` whose composer says "Ask about your data…" is the same defect
      // as §6a wearing different clothes: a field an admin filled in that never
      // reaches the visitor.
      present: [
        "Selamat datang di Gelael",
        "Berapa penjualan minggu ini?",
        "Tanya soal data Anda…",
        "Tutup obrolan",
      ],
      absent: ["Ask me about your data.", "Ask about your data…"],
    },
  },
  {
    file: "widget-empty-default.png",
    config: CONFIG_DEFAULT,
    thread: NO_THREAD,
    // An unconfigured tenant defaults to `en` server-side, so the English
    // chrome is correct here — and asserting it is what stops a locale fix from
    // turning into "every widget is Indonesian now".
    expect: {
      present: ["Ask me about your data.", "Ask about your data…"],
      absent: ["Selamat datang", "Tanya soal data Anda…"],
    },
  },
  {
    file: "widget-returning-visitor.png",
    config: CONFIG_CONFIGURED,
    thread: RETURNING_THREAD,
    expect: { present: ["Penjualan minggu ini"], absent: ["Selamat datang di Gelael"] },
  },
];

const browser = await chromium.launch();
const results = [];
try {
  for (const scene of SCENES) {
    const page = await browser.newPage({
      viewport: { width: 900, height: 680 },
      deviceScaleFactor: 2,
    });
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));

    await page.route("**/api/embed/**", (route) => {
      const url = route.request().url();
      const body = url.includes("/config")
        ? scene.config
        : url.includes("/threads/current")
          ? scene.thread
          : {};
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        // The frame's origin is opaque, so every one of its fetches is
        // cross-origin and needs the header a CDN-served widget really gets.
        headers: { "access-control-allow-origin": "*" },
        body: JSON.stringify(body),
      });
    });

    await page.goto(base, { waitUntil: "networkidle" });
    const frame = page.frameLocator("#frame");
    // Wait for the app to have rendered *something* from the fixture rather
    // than for a duration: the empty state draws before the config arrives.
    await frame.locator(".empty, .row").first().waitFor();
    await page.waitForTimeout(150);

    await page.screenshot({ path: path.join(out, scene.file), fullPage: false });

    // The words on screen include the ones that are attributes: a placeholder
    // and an aria-label are chrome a tenant's locale is supposed to reach, and
    // innerText cannot see either.
    const text = await frame.locator("body").evaluate((body) => {
      const attrs = [...body.querySelectorAll("[placeholder], [aria-label]")]
        .flatMap((el) => [el.getAttribute("placeholder"), el.getAttribute("aria-label")])
        .filter(Boolean);
      return [body.innerText, ...attrs].join("\n");
    });
    const missing = scene.expect.present.filter((s) => !text.includes(s));
    const leaked = scene.expect.absent.filter((s) => text.includes(s));
    results.push({ file: scene.file, missing, leaked, errors });
    await page.close();
  }
} finally {
  await browser.close();
  server.close();
}

let failed = false;
for (const r of results) {
  const problems = [
    ...r.missing.map((s) => `MISSING ${JSON.stringify(s)}`),
    ...r.leaked.map((s) => `LEAKED ${JSON.stringify(s)}`),
    ...r.errors.map((e) => `PAGE ERROR ${e}`),
  ];
  failed ||= problems.length > 0;
  console.log(`${problems.length ? "FAIL" : "ok  "}  ${r.file}${problems.length ? "  " + problems.join(" | ") : ""}`);
}
process.exit(failed ? 1 : 0);
