#!/usr/bin/env node
// Validates a document against the OpenAPI 3.1 meta-schema.
//
//   node scripts/validate.mjs                 # the authored spec
//   node scripts/validate.mjs served.json     # what a deployment actually serves
//
// The second form is the acceptance item: the bytes on the wire are what an
// integrator's generator reads, and a spec that is valid in the repository but
// mangled by the route that serves it is valid nowhere that matters.
//
// The meta-schema is vendored rather than fetched. A build step that reaches
// spec.openapis.org is a build step that fails when someone else's DNS does,
// and pinning the 2022-10-07 revision means the rules do not change under us.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import Ajv2020 from 'ajv/dist/2020.js';
import { loadSpec, repoRoot, specPath } from '../lib/spec.mjs';

const metaSchema = pinDynamicRefs(
  JSON.parse(readFileSync(resolve(repoRoot, 'packages/openapi-tools/meta/oas-3.1-schema.json'), 'utf8')),
);

/**
 * Rewrites the meta-schema's `$dynamicRef: "#meta"` to a plain
 * `$ref: "#/$defs/schema"`.
 *
 * The indirection exists so that a document declaring a different JSON Schema
 * dialect can substitute its own Schema Object definition. We do not — the spec
 * uses the default 2020-12 dialect — so the dynamic target is always the
 * `$dynamicAnchor: "meta"` sitting on `$defs.schema`, and pinning it says the
 * same thing statically.
 *
 * It is here rather than in the vendored file so that the vendored file stays a
 * byte-for-byte copy of what spec.openapis.org publishes: a hand-edited
 * meta-schema is a meta-schema nobody can diff against the original.
 *
 * Without this, Ajv resolves `#meta` against the wrong dynamic scope and
 * validates every `schema:` value as whatever object encloses it — reporting,
 * for instance, that `parameters[2].schema` "must have required property
 * 'name'". Ajv's own documentation records the limitation.
 */
function pinDynamicRefs(node) {
  if (Array.isArray(node)) return node.map(pinDynamicRefs);
  if (!node || typeof node !== 'object') return node;
  const out = {};
  for (const [key, value] of Object.entries(node)) {
    if (key === '$dynamicRef' && value === '#meta') {
      out.$ref = '#/$defs/schema';
      continue;
    }
    if (key === '$dynamicAnchor') continue;
    out[key] = pinDynamicRefs(value);
  }
  return out;
}

const arg = process.argv.slice(2).find((a) => !a.startsWith('--'));
const target = arg ? resolve(process.cwd(), arg) : specPath;
const doc = arg && arg.endsWith('.json') ? JSON.parse(readFileSync(target, 'utf8')) : loadSpec(target);

// `strict: false` because the document under test is an OpenAPI document, not a
// JSON Schema: Ajv's strict mode would object to `openapi`, `info` and every
// other keyword it does not recognise, which is the whole file.
const ajv = new Ajv2020({ strict: false, allErrors: true, validateFormats: false });
const validate = ajv.compile(metaSchema);

let failed = false;

if (!validate(doc)) {
  failed = true;
  console.error(`${rel(target)} is not a valid OpenAPI 3.1 document:\n`);
  for (const err of validate.errors ?? []) {
    console.error(`  ${err.instancePath || '/'} ${err.message}`);
  }
}

// The meta-schema above is the "without schema validation" revision: it says a
// Schema Object must be an object or a boolean and stops there, deliberately,
// because 3.1 schemas are full JSON Schema and the dialect is pluggable. So the
// component schemas — the part both SDKs generate their types from — get
// compiled here, which is what catches a dangling `$ref` or a keyword that
// means nothing.
const schemas = doc?.components?.schemas ?? {};
if (Object.keys(schemas).length === 0) {
  console.error(`${rel(target)} declares no component schemas; this check would pass vacuously`);
  failed = true;
}
const schemaAjv = new Ajv2020({ strict: false, validateFormats: false });
schemaAjv.addSchema(doc, 'spec');
for (const name of Object.keys(schemas)) {
  try {
    schemaAjv.compile({ $ref: `spec#/components/schemas/${name}` });
  } catch (err) {
    failed = true;
    console.error(`  components.schemas.${name}: ${err.message}`);
  }
}

// And every `$ref` anywhere in the document, because the two checks above miss
// the one that bites: a pointer into `#/components/…` that does not resolve is
// structurally valid OpenAPI — `{"$ref": "…"}` is a legal object — and the
// meta-schema has no way to know the target is absent. A generator handed one
// emits a client with a hole in it.
for (const { pointer, ref } of localRefs(doc)) {
  if (resolvePointer(doc, ref) === undefined) {
    failed = true;
    console.error(`  ${pointer}: dangling $ref ${ref}`);
  }
}

if (failed) {
  process.exit(1);
}
console.log(`ok      ${rel(target)} is a valid OpenAPI 3.1 document (${Object.keys(doc.paths ?? {}).length} paths, ${Object.keys(schemas).length} schemas)`);

function rel(path) {
  return path.startsWith(repoRoot) ? path.slice(repoRoot.length + 1) : path;
}

/** Walks the document yielding every local `$ref` and where it was found. */
function* localRefs(node, pointer = '') {
  if (Array.isArray(node)) {
    for (const [i, item] of node.entries()) yield* localRefs(item, `${pointer}/${i}`);
    return;
  }
  if (!node || typeof node !== 'object') return;
  for (const [key, value] of Object.entries(node)) {
    if (key === '$ref' && typeof value === 'string' && value.startsWith('#/')) {
      yield { pointer: pointer || '/', ref: value };
      continue;
    }
    yield* localRefs(value, `${pointer}/${key}`);
  }
}

function resolvePointer(doc, ref) {
  let target = doc;
  for (const part of ref.slice(2).split('/')) {
    if (target === undefined || target === null) return undefined;
    target = target[part.replace(/~1/g, '/').replace(/~0/g, '~')];
  }
  return target;
}
