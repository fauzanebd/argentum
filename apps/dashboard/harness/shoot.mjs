/**
 * Takes the screenshots the coverage docs owe.
 *
 * `pnpm --filter dashboard shots` boots the harness on a real vite server and
 * drives a real Chromium over it. It writes into `docs/coverage/assets/`, which
 * is where every other screenshot this repo cites already lives.
 *
 * **The browser is not downloaded.** playwright-core resolves the chromium in
 * the shared `~/.cache/ms-playwright`, which is already on this machine; the
 * dependency added for this is the driver, not the browser. If a run ever fails
 * with "Executable doesn't exist", `pnpm exec playwright install chromium` is
 * the fix and it is a download, not a licence problem.
 *
 * A scene that needs an interaction to reach the state being photographed gets
 * one here, through the product's own controls. Typing 64 characters into a
 * field capped at 60 is how the red counter is produced, because the alternative
 * — a fixture that claims the counter is red — photographs this file rather than
 * the product.
 */
import { fileURLToPath } from "node:url";
import path from "node:path";
import { readFile } from "node:fs/promises";
import { createServer } from "vite";
import { chromium, firefox, webkit } from "playwright-core";
import { FORM_NAME, FORM_TRIGGER, FORM_BODY } from "./constants.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, "../../../docs/coverage/assets");

/** A committed, fully renderable plan — no `sha256:` image placeholders, so it
 *  needs none of `TestWritePlans`'s undigested output. It is served over the
 *  wire rather than imported, because that is how the page really gets it. */
const PLAN = JSON.parse(
  await readFile(
    path.resolve(here, "../../backend/internal/report/videoplan/testdata/kpi_summary.plan.json"),
    "utf8",
  ),
);

function shareView(plan) {
  return {
    title: plan.title,
    filename: "kpi_summary.mp4",
    format: "video",
    plan,
    expires_at: "2027-01-01T00:00:00Z",
  };
}

const ENGINES = { chromium, firefox, webkit };

/** The share API call the page makes, and nothing else. */
const SHARE_ROUTE = /\/share\/harness$/;

const SCENES = [
  { id: "chat-chips", file: "skills-chat-chips.png", height: 260 },
  { id: "settings-member", file: "skills-tab-member.png", height: 420 },
  { id: "settings-admin", file: "skills-tab-admin.png", height: 420 },
  { id: "skills-overflow", file: "skills-index-overflow.png", height: 700 },
  {
    // T-V4's owed arm: the shared player, in all three engines. One frame of a
    // real plan drawn by the same compositions the render service draws
    // headlessly — which is the claim the page is making, so a browser that
    // draws it differently is the defect this is looking for.
    id: "share-player",
    file: "share-player.png",
    height: 900,
    engines: ["chromium", "firefox", "webkit"],
    // Anchored on the token rather than `**/share/**`: the loose glob also
    // matched the dev server's module URL for `features/share/share-page.tsx`,
    // so the page was served its own JSON and never booted.
    route: { pattern: SHARE_ROUTE, body: () => shareView(PLAN) },
    async drive(page) {
      await page.getByRole("heading", { name: PLAN.title }).waitFor();
      // **Frame 0 is black on purpose** — the cover's entrance begins at zero
      // opacity, which is the same reason the stills CLI samples mid-scene
      // rather than at a scene's first frame. A screenshot of a freshly mounted
      // player photographs that and looks like a player that does not work.
      await page.getByRole("button", { name: "Play" }).first().click();
      await page.waitForTimeout(1800);
    },
  },
  {
    // The other half of the same row: a plan from a version this page does not
    // know renders the scenes it understands and says so.
    id: "share-player-future",
    file: "share-player-future-version.png",
    height: 900,
    route: { pattern: SHARE_ROUTE, body: () => shareView({ ...PLAN, version: 99 }) },
    async drive(page) {
      await page.getByText("newer version of Argentum").waitFor();
      await page.getByRole("button", { name: "Play" }).first().click();
      await page.waitForTimeout(1800);
    },
  },
  {
    id: "skills-form",
    file: "skills-form-preview.png",
    height: 1400,
    async drive(page) {
      await page.getByRole("button", { name: "Write a procedure" }).click();
      await page.getByLabel("Name").fill(FORM_NAME);
      await page.getByLabel("When to use it").fill(FORM_TRIGGER);
      await page.getByLabel("The procedure").fill(FORM_BODY);
      // Move focus off the last field before the shot: this app's focus ring is
      // its primary colour, which is red, and a focused textarea photographs as
      // a field in error.
      await page.getByText("New procedure").click();
      // The preview is debounced 400ms and then a round trip; wait for the pane
      // itself rather than for a duration.
      await page.getByText("What your agents will see").waitFor();
    },
  },
];

const server = await createServer({ configFile: path.join(here, "vite.config.ts") });
await server.listen();
const base = `http://localhost:${server.config.server.port}`;

const results = [];
const launched = new Map();
const skipped = new Map();
try {
  for (const scene of SCENES) {
    // Most scenes are about layout and one browser answers for them. The player
    // is the exception: T-V4's arm is *three* engines, because "the same
    // compositions run in the browser" is a claim about browsers.
    for (const name of scene.engines ?? ["chromium"]) {
      if (!launched.has(name)) {
        // An engine that will not start is reported and skipped, not fatal.
        // WebKit needs system libraries (`libsecret`, `libwoff2dec`) that only
        // root can install, and a harness that aborts the whole run over the
        // third browser is one that stops producing the first two.
        try {
          launched.set(name, await ENGINES[name].launch());
        } catch (e) {
          launched.set(name, null);
          skipped.set(name, e.message.split("\n")[0]);
        }
      }
      const browser = launched.get(name);
      if (!browser) continue;
      const page = await browser.newPage({
        viewport: { width: 1280, height: scene.height },
        // 1 on the extra engines: three copies of the same page at 2x is three
        // times the bytes in git for a difference nobody is looking for.
        deviceScaleFactor: name === "chromium" ? 2 : 1,
      });
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      if (scene.route) {
        await page.route(scene.route.pattern, (route) =>
          route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify(scene.route.body()),
          }),
        );
      }
      await page.goto(`${base}/?scene=${scene.id}`, { waitUntil: "networkidle" });
      if (scene.drive) await scene.drive(page);
      const file = path.join(
        out,
        name === "chromium" ? scene.file : scene.file.replace(/\.png$/, `-${name}.png`),
      );
      await page.screenshot({ path: file, fullPage: true });
      // Read back what the browser actually rendered, so the run reports a fact
      // rather than "a file was written" — a blank page screenshots fine.
      const text = await page.locator("body").innerText();
      results.push({ scene: `${scene.id} (${name})`, file: path.basename(file), chars: text.length, errors });
      await page.close();
    }
  }
} finally {
  for (const b of launched.values()) if (b) await b.close();
  await server.close();
}

for (const r of results) {
  const bad = r.errors.length ? ` ERRORS: ${r.errors.join(" | ")}` : "";
  console.log(`${r.chars.toString().padStart(5)} chars  ${r.file}${bad}`);
}
for (const [name, why] of skipped) console.log(`skip  ${name}: ${why}`);
if (results.some((r) => r.errors.length)) process.exit(1);
