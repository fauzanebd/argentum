// The bundle budget, as a check rather than as a sentence in a ticket (T-21).
//
// A widget that slows the host page gets removed by the customer's own frontend
// team, and the way a 15 KB loader becomes a 60 KB one is a dependency somebody
// added on a Tuesday with no number in front of them. Run after a build; a
// breach is a non-zero exit.

import { gzipSync } from "node:zlib";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const LIMITS = {
  // The script tag on the tenant's page. Framework-free by construction.
  loader: 15 * 1024,
  // Everything inside the iframe: markup, styles and app, since they are all
  // fetched before a visitor can type.
  app: 80 * 1024,
};

function gzipped(path) {
  return gzipSync(readFileSync(path), { level: 9 }).length;
}

const loader = gzipped("dist/argentum-widget.js");
const appDir = "dist/app";
const app = readdirSync(appDir)
  .filter((f) => /\.(js|css|html)$/.test(f))
  .reduce((total, f) => total + gzipped(join(appDir, f)), 0);

const rows = [
  ["loader", loader, LIMITS.loader],
  ["app", app, LIMITS.app],
];

let failed = false;
for (const [name, actual, limit] of rows) {
  const kb = (n) => `${(n / 1024).toFixed(1)} KB`;
  const ok = actual <= limit;
  if (!ok) failed = true;
  console.log(`${ok ? "ok     " : "FAIL   "} ${name} ${kb(actual)} gzipped (budget ${kb(limit)})`);
}

if (failed) {
  console.error("\nBundle budget exceeded. The budget is the feature: a widget that slows");
  console.error("the host page is one the customer's frontend team removes.");
  process.exit(1);
}
