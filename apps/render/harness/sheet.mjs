/**
 * Assembles the scene contact sheet `T-V5` owes, and the pale-brand comparison
 * beside it.
 *
 *   ARGENTUM_PLAN_OUT=/tmp/plans go test ./internal/report/videoplan -run WritePlans
 *   pnpm --filter @argentum/render render:fixture /tmp/plans/monthly_sales.plan.json /tmp/stills --stills
 *   pnpm --filter @argentum/render sheet /tmp/stills
 *
 * **Laid out in a browser rather than by an image library.** The alternative is
 * a compositing dependency in a service whose whole selling point is that it
 * has no database, no object storage and no outbound network — for a picture
 * that goes in a document. Chromium is already here because Remotion needs one,
 * and this repo now has two other harnesses that photograph a page.
 *
 * One frame per scene *kind*, not per scene: `monthly_sales` has thirteen
 * scenes across eight kinds, and a sheet with `section` on it three times is a
 * sheet whose reader stops reading.
 */
import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "playwright-core";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, "../../../docs/coverage/assets");
const stillsDir = process.argv[2] ?? "/tmp/stills";

/** The order a reader meets them in a finished video, not alphabetical. */
const ORDER = ["cover", "section", "kpi", "statement", "chart", "table", "quote", "closing"];

const files = (await readdir(stillsDir)).filter((f) => f.endsWith(".png"));
if (files.length === 0) {
  console.error(`no stills in ${stillsDir} — run render:fixture --stills first`);
  process.exit(2);
}

/** First still of each kind wins; `-NN-kind.png` is the CLI's naming. */
const byKind = new Map();
for (const f of files.sort()) {
  const kind = f.replace(/\.png$/, "").split("-").pop();
  if (!byKind.has(kind)) byKind.set(kind, f);
}

const cells = ORDER.filter((k) => byKind.has(k)).map((k) => ({ kind: k, file: byKind.get(k) }));
const missing = ORDER.filter((k) => !byKind.has(k));

async function dataURI(file) {
  const b = await readFile(path.join(stillsDir, file));
  return `data:image/png;base64,${b.toString("base64")}`;
}

const tiles = await Promise.all(
  cells.map(async (c) => `
    <figure>
      <img src="${await dataURI(c.file)}" alt="${c.kind}" />
      <figcaption>${c.kind}</figcaption>
    </figure>`),
);

const html = `<!doctype html>
<html><head><meta charset="utf-8" /><style>
  body { margin: 0; background: #0b0d10; font: 13px/1.4 ui-sans-serif, system-ui, sans-serif; }
  .sheet { display: grid; grid-template-columns: repeat(2, 1fr); gap: 18px; padding: 24px; }
  figure { margin: 0; }
  img { width: 100%; display: block; border-radius: 6px; }
  figcaption {
    color: #9aa4b2; padding-top: 6px; letter-spacing: .08em;
    text-transform: uppercase; font-size: 11px;
  }
</style></head>
<body><div class="sheet">${tiles.join("")}</div></body></html>`;

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
await page.setContent(html, { waitUntil: "load" });
const file = path.join(out, "video-scene-contact-sheet.png");
await page.screenshot({ path: file, fullPage: true });
await browser.close();

console.log(`${cells.length} kinds: ${cells.map((c) => c.kind).join(", ")}`);
if (missing.length) console.log(`missing: ${missing.join(", ")}`);
console.log(`wrote ${file}`);
