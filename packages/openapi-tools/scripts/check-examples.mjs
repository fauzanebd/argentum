#!/usr/bin/env node
// Checks that every code block in the quickstart is byte-for-byte the file CI
// actually runs.
//
//   node scripts/check-examples.mjs
//
// The ticket's rule is that "an example that has never been executed is a
// support ticket with a delay fuse". Executing them is `docs/api/examples/run.sh`;
// this is the other half, and without it the two copies drift the first time
// someone fixes a sample in the runner and not in the prose — which is the
// version an integrator reads.
//
// The binding is a marker comment above each fence:
//
//     <!-- example: examples/curl/me.sh -->
//     ```bash
//     …the file's exact contents…
//     ```
//
// It also fails on an example file that no block quotes, because an example
// nobody publishes is one nobody reads and one nothing keeps honest.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { repoRoot } from '../lib/spec.mjs';

const quickstartPath = resolve(repoRoot, 'docs/api/quickstart.md');
const examplesDir = resolve(repoRoot, 'docs/api/examples');

// The runner and its notes are not published samples; everything else under
// examples/ is expected to appear in the quickstart.
const notPublished = new Set(['run.sh', 'README.md']);

const quickstart = readFileSync(quickstartPath, 'utf8');
const pattern = /<!--\s*example:\s*(\S+)\s*-->\r?\n```[a-zA-Z0-9]*\r?\n([\s\S]*?)```/g;

let failures = 0;
const quoted = new Set();

for (const match of quickstart.matchAll(pattern)) {
  const [, ref, block] = match;
  const path = resolve(repoRoot, 'docs/api', ref);
  quoted.add(path);
  let actual;
  try {
    actual = readFileSync(path, 'utf8');
  } catch {
    console.error(`  quickstart quotes ${ref}, which does not exist`);
    failures++;
    continue;
  }
  if (block !== actual) {
    failures++;
    console.error(`  ${ref} has drifted from the block that quotes it in docs/api/quickstart.md`);
    console.error(diff(actual, block));
  }
}

for (const path of walk(examplesDir)) {
  const name = relative(examplesDir, path);
  if (notPublished.has(name) || name.startsWith('node_modules')) continue;
  if (!quoted.has(path)) {
    failures++;
    console.error(`  docs/api/examples/${name} is executed by run.sh but appears nowhere in the quickstart`);
  }
}

if (failures > 0) {
  console.error(`\n${failures} example(s) out of step. The file is the truth: copy it into the quickstart block.`);
  process.exit(1);
}
console.log(`ok      docs/api/quickstart.md quotes ${quoted.size} example files exactly`);

function walk(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) out.push(...walk(path));
    else out.push(path);
  }
  return out;
}

/** The first differing line, which is almost always the whole story. */
function diff(expected, actual) {
  const a = expected.split('\n');
  const b = actual.split('\n');
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (a[i] !== b[i]) {
      return `      line ${i + 1}\n      file:       ${JSON.stringify(a[i] ?? null)}\n      quickstart: ${JSON.stringify(b[i] ?? null)}`;
    }
  }
  return '';
}
