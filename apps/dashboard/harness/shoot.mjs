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

/**
 * How each engine is launched unless a scene says otherwise. Chromium gets a
 * fake microphone and a fake permission prompt that allows it (T-W9), so the
 * voice scenes record through the browser's own `getUserMedia` and
 * `MediaRecorder` over a generated tone. Neither flag changes a page that never
 * asks for a microphone.
 *
 * **The prompt flag is not optional**, and a page permission is not a
 * substitute: headless Chromium granted `microphone` by its context still
 * answers `getUserMedia` with NotSupportedError, which is how the first run of
 * these scenes photographed "could not start".
 */
const MIC = "--use-fake-device-for-media-stream";
const LAUNCH = {
  chromium: { args: [MIC, "--use-fake-ui-for-media-stream"] },
};

/** Hold the microphone the way a person does: the pointer down on it, and not up. */
async function holdMicrophone(page) {
  const mic = page.getByRole("button", { name: "Hold to speak a question" });
  await mic.waitFor();
  const box = await mic.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
}

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
    // The room's controls, driven open rather than rendered open: the add menu
    // is a Radix dropdown and a screenshot of it closed photographs a button.
    id: "room-bar",
    file: "room-participant-bar.png",
    height: 420,
    async drive(page) {
      await page.getByRole("button", { name: /Add agent/ }).click();
      await page.getByRole("menuitem", { name: /Legal/ }).waitFor();
      // Radix fades the menu in. waitFor returns on attachment, which is before
      // the animation ends, so the first run of this scene photographed a
      // half-transparent menu — the same trap the player scene above hit.
      await page.waitForTimeout(400);
    },
  },
  {
    // The same bar with every hue removed. If the agents are still tellable
    // apart here, the colour is reinforcement rather than the attribution —
    // which is the rule T-R3's palette gate set for the report charts.
    id: "room-bar-grayscale",
    file: "room-participant-bar-grayscale.png",
    height: 240,
  },
  {
    id: "room-mentions",
    file: "room-mention-menu.png",
    height: 420,
  },
  // The room's own lines and a hand-off (T-N6, T-N7), in colour and without it:
  // a limit and a settle must read differently with every hue removed.
  { id: "room-lines", file: "room-lines-and-hand-off.png", height: 1100 },
  { id: "room-lines-grayscale", file: "room-lines-and-hand-off-grayscale.png", height: 1100 },
  {
    // The flag is ticked through its own label, as an admin would tick it.
    id: "agent-form-nudge",
    file: "agent-form-may-ask.png",
    height: 720,
    async drive(page) {
      // The form is closed until a starting point is picked.
      await page.getByRole("button", { name: /Start from blank/ }).click();
      const flag = page.getByText("May ask other agents");
      await flag.scrollIntoViewIfNeeded();
      await flag.click();
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
  {
    // T-Z7's matrix: one person's panel, opened through its own button, above
    // the per-agent card drawn from the same read. HR is restricted and not
    // granted to the admin looking at it, which is the line decision 4 needs
    // on screen rather than in a roadmap.
    id: "team-access",
    file: "team-access-matrix.png",
    height: 1800,
    async drive(page) {
      await page.getByRole("button", { name: "Access for rina@tokomaju.id" }).click();
      await page.getByText("Agents they may talk to").waitFor();
      // The whole clause, not the name: since T-Z6 the HR warehouse source card
      // says "You are not granted HR warehouse" too.
      await page.getByText("You are not granted HR, so you cannot talk to it", { exact: false }).waitFor();
      // Off the button that was just pressed: the pointer left on it paints the
      // hover in this app's primary colour, which is red, and the first shot of
      // this scene read as an Access button in an error state.
      await page.mouse.move(0, 0);
    },
  },
  {
    // The flip to restricted, pressed rather than rendered open: the warning is
    // state the button produces, and a fixture claiming it would photograph
    // this file.
    id: "team-access-restrict",
    file: "team-access-restrict-warning.png",
    height: 1800,
    async drive(page) {
      await page.getByRole("button", { name: "Restrict Ops…" }).click();
      await page.getByRole("alertdialog", { name: "Restrict Ops" }).waitFor();
      // T-Z8: the channel count arrives with its own request, and a shot of
      // "Checking its channel bindings…" would file a warning that says nothing.
      await page.getByText("1 channel bound to it will stop answering", { exact: false }).waitFor();
      await page.mouse.move(0, 0);
    },
  },
  {
    // T-Z6's document warning, pressed like the others. It is the one sentence on
    // the Team tab that says what an agent will stop finding for somebody.
    id: "team-access-document-restrict",
    file: "team-access-document-restrict-warning.png",
    height: 3600,
    async drive(page) {
      await page.getByRole("button", { name: "Restrict Payroll 2026.pdf…" }).click();
      await page.getByRole("alertdialog", { name: "Restrict Payroll 2026.pdf" }).waitFor();
      await page
        .getByText("an agent searching documents for them will find nothing in it", { exact: false })
        .waitFor();
      await page.mouse.move(0, 0);
    },
  },
  {
    // T-Z8's acknowledgement, opened the way an admin opens it — by choosing a
    // restricted agent in the form. The checkbox is state that choice produces.
    id: "agent-bindings",
    file: "agent-bindings-acknowledge.png",
    height: 900,
    async drive(page) {
      await page.getByRole("combobox", { name: "Channel" }).click();
      await page.getByRole("option", { name: /slack/i }).click();
      await page.getByRole("combobox", { name: "Agent" }).click();
      await page.getByRole("option", { name: "HR" }).click();
      await page.getByText("Anyone who can post here can use this agent", { exact: false }).first().waitFor();
      await page.mouse.move(0, 0);
    },
  },
  {
    // T-Z5's confirmation: pressed, and photographed only once the link count
    // has arrived — the notice reads "Checking…" until its request lands, and a
    // shot of that would file a warning that says nothing.
    id: "team-access-dashboard-restrict",
    file: "team-access-dashboard-restrict-warning.png",
    height: 2400,
    async drive(page) {
      await page.getByRole("button", { name: "Restrict Weekly sales…" }).click();
      await page.getByRole("alertdialog", { name: "Restrict Weekly sales" }).waitFor();
      await page.getByText("Its 2 live share links will be revoked", { exact: false }).waitFor();
      await page.mouse.move(0, 0);
    },
  },
  {
    // T-W9, granted: photographed with the button still held, which is the
    // only moment the level meter exists.
    id: "voice-recording",
    file: "voice-composer-recording.png",
    height: 460,
    async drive(page) {
      await holdMicrophone(page);
      await page.getByText(/^Listening\./).waitFor();
      await page.waitForTimeout(900);
    },
  },
  {
    // Let go: the recording is uploaded and what was heard lands in the box.
    // Focus is taken off the textarea before the shot, for the skills-form
    // scene's reason — a focused field photographs red.
    id: "voice-transcript",
    file: "voice-composer-transcript.png",
    height: 460,
    async drive(page) {
      await holdMicrophone(page);
      await page.getByText(/^Listening\./).waitFor();
      await page.waitForTimeout(1500);
      await page.mouse.up();
      // A string, not a function: this file is linted as Node, where `document`
      // does not exist, and the expression runs in the page, where it does.
      await page.waitForFunction('document.querySelector("textarea")?.value.includes("gudang barat")');
      await page.locator("textarea").blur();
      await page.mouse.move(0, 0);
      // The button fades from its recording colour; the first shot of this scene
      // caught it halfway and read as a microphone still held.
      await page.waitForTimeout(400);
    },
  },
  {
    // The fake prompt set to refuse: the reason and the fix on screen are the
    // browser's own NotAllowedError, not a stub's.
    id: "voice-denied",
    file: "voice-composer-denied.png",
    height: 460,
    launch: { args: [MIC, "--use-fake-ui-for-media-stream=deny"] },
    async drive(page) {
      await holdMicrophone(page);
      await page.getByText("The browser is not allowed to use your microphone.").waitFor();
      await page.mouse.up();
      await page.mouse.move(0, 0);
    },
  },
  {
    id: "voice-ungranted",
    file: "voice-composer-ungranted.png",
    height: 460,
    async drive(page) {
      await holdMicrophone(page);
      await page.mouse.up();
      await page.getByText("An admin has not given you voice", { exact: false }).waitFor();
      await page.mouse.move(0, 0);
    },
  },
  {
    // Play pressed on the table, which T-W8's check refuses; the first answer's
    // button is left as a person first sees it.
    id: "voice-listen",
    file: "voice-listen-and-spoken.png",
    height: 900,
    async drive(page) {
      await page.getByRole("button", { name: "Read this answer aloud" }).nth(1).click();
      await page.getByText("This answer can't be read aloud — read it instead.").waitFor();
      await page.mouse.move(0, 0);
    },
  },
];

// `pnpm --filter dashboard shots team-access` shoots only the scenes named.
// With no names every scene is shot, and every PNG re-encoded — a diff in git
// on screens nobody touched, saying something changed when nothing did.
const only = process.argv.slice(2);
const scenes = only.length ? SCENES.filter((s) => only.includes(s.id)) : SCENES;
if (scenes.length !== only.length && only.length) {
  const known = new Set(SCENES.map((s) => s.id));
  console.error(`unknown scene: ${only.filter((id) => !known.has(id)).join(", ")}`);
  process.exit(1);
}

const server = await createServer({ configFile: path.join(here, "vite.config.ts") });
await server.listen();
const base = `http://localhost:${server.config.server.port}`;

const results = [];
const launched = new Map();
const skipped = new Map();
try {
  for (const scene of scenes) {
    // Most scenes are about layout and one browser answers for them. The player
    // is the exception: T-V4's arm is *three* engines, because "the same
    // compositions run in the browser" is a claim about browsers.
    for (const name of scene.engines ?? ["chromium"]) {
      // A scene may launch its engine with its own flags — the microphone's
      // refusal is a launch flag, not a page setting — so a browser is kept per
      // engine and flag set, and the scenes that share one still share it.
      const launch = scene.launch ?? LAUNCH[name];
      const key = `${name} ${JSON.stringify(launch ?? {})}`;
      if (!launched.has(key)) {
        // An engine that will not start is reported and skipped, not fatal.
        // WebKit needs system libraries (`libsecret`, `libwoff2dec`) that only
        // root can install, and a harness that aborts the whole run over the
        // third browser is one that stops producing the first two.
        try {
          launched.set(key, await ENGINES[name].launch(launch));
        } catch (e) {
          launched.set(key, null);
          skipped.set(name, e.message.split("\n")[0]);
        }
      }
      const browser = launched.get(key);
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
