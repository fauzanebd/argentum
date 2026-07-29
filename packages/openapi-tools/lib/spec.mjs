// Shared plumbing for the generators. Each of them reads the same document and
// writes one artifact from it; this is the part they must not each have their
// own version of.
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

const here = dirname(fileURLToPath(import.meta.url));

/** The repository root, from this file's own location. */
export const repoRoot = resolve(here, '..', '..', '..');

/** The one authored copy of the contract. */
export const specPath = resolve(repoRoot, 'apps/backend/openapi/v1.yaml');

/** Reads and parses the spec. */
export function loadSpec(path = specPath) {
  return parse(readFileSync(path, 'utf8'));
}

/**
 * Resolves a local `$ref` against the document.
 *
 * Only `#/`-rooted pointers, because that is all this spec uses and a remote
 * ref would silently produce `undefined` — a generator that emitted nothing for
 * a schema it could not find would be worse than one that stopped.
 */
export function deref(doc, node) {
  let seen = 0;
  while (node && typeof node === 'object' && typeof node.$ref === 'string') {
    if (!node.$ref.startsWith('#/')) throw new Error(`only local refs are supported: ${node.$ref}`);
    let target = doc;
    for (const part of node.$ref.slice(2).split('/')) {
      target = target?.[part.replace(/~1/g, '/').replace(/~0/g, '~')];
    }
    if (target === undefined) throw new Error(`dangling $ref: ${node.$ref}`);
    node = target;
    if (++seen > 32) throw new Error(`$ref cycle at ${node.$ref}`);
  }
  return node;
}

/** Every operation in the document, in a stable order. */
export function operations(doc) {
  const methods = ['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'];
  const out = [];
  for (const [path, item] of Object.entries(doc.paths ?? {})) {
    for (const method of methods) {
      if (item[method]) out.push({ method: method.toUpperCase(), path, op: item[method] });
    }
  }
  return out.sort((a, b) => `${a.path} ${a.method}`.localeCompare(`${b.path} ${b.method}`));
}

/**
 * Writes a generated file, or — with `--check` — reports whether the committed
 * copy already says this.
 *
 * The two modes exist for the same reason the design tokens have them: the
 * generated files are committed, so CI needs a way to fail on a stale one
 * without writing to the working tree it is about to diff.
 *
 * Returns true when the file was already correct.
 */
export function emit(path, content, { check = process.argv.includes('--check') } = {}) {
  let current = null;
  try {
    current = readFileSync(path, 'utf8');
  } catch {
    current = null;
  }
  if (current === content) {
    console.log(`ok      ${short(path)}`);
    return true;
  }
  if (check) {
    console.error(`STALE   ${short(path)} — run \`pnpm --filter @argentum/openapi-tools build\` and commit the result`);
    process.exitCode = 1;
    return false;
  }
  writeFileSync(path, content);
  console.log(`written ${short(path)}`);
  return false;
}

function short(path) {
  return path.startsWith(repoRoot) ? path.slice(repoRoot.length + 1) : path;
}
