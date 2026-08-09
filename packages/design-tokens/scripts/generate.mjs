// Entry point for both generators. `make tokens` runs it; CI runs it with
// --check, which writes nothing and exits non-zero if a generated file on disk
// disagrees with tokens.json.
//
// Usage:
//   node scripts/generate.mjs            write both outputs
//   node scripts/generate.mjs --check    verify only (CI, and `pnpm lint`)

import { readFileSync, writeFileSync } from 'node:fs';
import { relative } from 'node:path';
import { loadTokens, REPO_ROOT } from '../lib/tokens.mjs';
import { CSS_OUT, renderCSS } from './gen-css.mjs';
import { GO_OUT, renderGo } from './gen-go.mjs';
import { MOTION_OUT, renderMotion } from './gen-motion.mjs';

const check = process.argv.includes('--check');
const tokens = loadTokens();

const outputs = [
  { path: CSS_OUT, content: renderCSS(tokens) },
  { path: GO_OUT, content: renderGo(tokens) },
  { path: MOTION_OUT, content: renderMotion(tokens) },
];

let stale = 0;
for (const { path, content } of outputs) {
  const rel = relative(REPO_ROOT, path);
  const current = read(path);
  if (current === content) {
    console.log(`${check ? 'ok      ' : 'unchanged'} ${rel}`);
    continue;
  }
  if (check) {
    stale++;
    console.error(`STALE    ${rel}`);
    console.error(firstDifference(current, content));
    continue;
  }
  writeFileSync(path, content);
  console.log(`${current === null ? 'created  ' : 'wrote    '} ${rel}`);
}

if (stale > 0) {
  console.error(
    `\n${stale} generated file(s) do not match tokens.json. Run \`make tokens\` and commit the result.`,
  );
  process.exit(1);
}

function read(path) {
  try {
    return readFileSync(path, 'utf8');
  } catch (err) {
    if (err.code === 'ENOENT') return null;
    throw err;
  }
}

/** Enough of a diff to see what drifted, without pulling in a diff library. */
function firstDifference(current, want) {
  if (current === null) return '  file does not exist';
  const a = current.split('\n');
  const b = want.split('\n');
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (a[i] !== b[i]) {
      return [
        `  first difference at line ${i + 1}:`,
        `    on disk:   ${a[i] ?? '<end of file>'}`,
        `    generated: ${b[i] ?? '<end of file>'}`,
      ].join('\n');
    }
  }
  return '  files differ only in trailing content';
}
