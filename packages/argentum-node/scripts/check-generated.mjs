#!/usr/bin/env node
// Fails when src/types.generated.ts is stale against apps/backend/openapi/v1.yaml,
// without writing to the working tree.
//
// The other three artifacts generated from that document — the Postman
// collection and the Python SDK's types — have had a `--check` mode since
// `T-A4`, and `@argentum/openapi-tools` runs it. This file did not: it is
// written by `openapi-typescript`, which the Node SDK's own `generate` script
// calls, and nothing verified it outside CI's `pnpm -r build` plus a `git diff`.
//
// That gap is why the two scopes `T-14` added on 2026-08-03 sat unregenerated
// here for six days: `make openapi-check` reported ok, and only a pushed CI run
// would have said otherwise. Same lesson as the sprint overview's "a gate in
// the wrong bucket is a gate nobody runs" — a check a developer cannot run
// locally is a check that reports late.
//
// It shells out to the same CLI `generate` uses, into a temporary file, rather
// than calling openapi-typescript's Node API: the API's output and the CLI's
// differ in their preamble, and a checker that disagrees with the writer is
// worse than no checker.
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const pkgRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const spec = resolve(pkgRoot, '../../apps/backend/openapi/v1.yaml');
const committed = resolve(pkgRoot, 'src/types.generated.ts');

const scratch = mkdtempSync(join(tmpdir(), 'argentum-sdk-types-'));
try {
  const fresh = join(scratch, 'types.generated.ts');
  execFileSync('pnpm', ['exec', 'openapi-typescript', spec, '--output', fresh], {
    cwd: pkgRoot,
    stdio: ['ignore', 'ignore', 'inherit'],
  });

  if (readFileSync(fresh, 'utf8') === readFileSync(committed, 'utf8')) {
    console.log('ok      packages/argentum-node/src/types.generated.ts');
  } else {
    console.error(
      "STALE   packages/argentum-node/src/types.generated.ts — run `make openapi` and commit the result",
    );
    process.exitCode = 1;
  }
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
