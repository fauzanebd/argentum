// The loader's bundle budget, as a check rather than a sentence in a ticket
// (T-21's budget, T-22's package).
//
// A widget that slows the host page gets removed by the customer's own frontend
// team, and the way a 15 KB loader becomes a 60 KB one is a dependency somebody
// added on a Tuesday with no number in front of them. The budget moved here with
// the source: this package is now the only thing that builds the loader, and a
// budget checked somewhere other than where the file is produced is a budget
// that stops being checked the first time the two are built separately.
//
// Only the IIFE is measured. It is the file a browser downloads over the CDN
// path; the ESM and CJS builds are fed to a bundler that will tree-shake and
// re-minify them, so their bytes on disk are not bytes on anybody's page.

import { gzipSync } from "node:zlib";
import { readFileSync } from "node:fs";

const LIMIT = 15 * 1024;

const actual = gzipSync(readFileSync("dist/argentum-widget.js"), { level: 9 }).length;
const kb = (n) => `${(n / 1024).toFixed(1)} KB`;
const ok = actual <= LIMIT;

console.log(`${ok ? "ok     " : "FAIL   "} loader ${kb(actual)} gzipped (budget ${kb(LIMIT)})`);

if (!ok) {
  console.error("\nBundle budget exceeded. The budget is the feature: a widget that slows");
  console.error("the host page is one the customer's frontend team removes.");
  process.exit(1);
}
