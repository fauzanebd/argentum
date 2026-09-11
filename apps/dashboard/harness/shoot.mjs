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
import { createServer } from "vite";
import { chromium } from "playwright-core";
import { FORM_NAME, FORM_TRIGGER, FORM_BODY } from "./constants.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, "../../../docs/coverage/assets");

const SCENES = [
  { id: "chat-chips", file: "skills-chat-chips.png", height: 260 },
  { id: "settings-member", file: "skills-tab-member.png", height: 420 },
  { id: "settings-admin", file: "skills-tab-admin.png", height: 420 },
  { id: "skills-overflow", file: "skills-index-overflow.png", height: 700 },
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

const browser = await chromium.launch();
const results = [];
try {
  for (const scene of SCENES) {
    const page = await browser.newPage({
      viewport: { width: 1280, height: scene.height },
      deviceScaleFactor: 2,
    });
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto(`${base}/?scene=${scene.id}`, { waitUntil: "networkidle" });
    if (scene.drive) await scene.drive(page);
    const file = path.join(out, scene.file);
    await page.screenshot({ path: file, fullPage: true });
    // Read back what the browser actually rendered, so the run reports a fact
    // rather than "a file was written" — a blank page screenshots fine.
    const text = await page.locator("body").innerText();
    results.push({ scene: scene.id, file: scene.file, chars: text.length, errors });
    await page.close();
  }
} finally {
  await browser.close();
  await server.close();
}

for (const r of results) {
  const bad = r.errors.length ? ` ERRORS: ${r.errors.join(" | ")}` : "";
  console.log(`${r.chars.toString().padStart(5)} chars  ${r.file}${bad}`);
}
if (results.some((r) => r.errors.length)) process.exit(1);
