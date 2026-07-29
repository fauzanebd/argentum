#!/usr/bin/env node
// Generates the Python SDK's wire types from the spec.
//
//   node scripts/gen-python-types.mjs            # write
//   node scripts/gen-python-types.mjs --check    # fail if the committed copy is stale
//
// The Node SDK gets this from `openapi-typescript`. Python's equivalents
// (datamodel-code-generator and friends) would put a Python toolchain in the
// path of a CI job that otherwise needs none, to emit a file this small — so it
// is written here, from the same document, by the same package that writes the
// Postman collection.
//
// **Requiredness is documented rather than typed.** Saying it in the type needs
// `typing.Required`, which is 3.11, or `typing_extensions`, which is a runtime
// dependency for a package that otherwise has one. Every TypedDict below is
// therefore `total=False` with the required keys named in its docstring: a
// client reading a response the server produced is not the place where a
// missing-key checker earns its cost.
import { resolve } from 'node:path';
import { emit, loadSpec, repoRoot } from '../lib/spec.mjs';

const doc = loadSpec();
const target = resolve(repoRoot, 'packages/argentum-python/src/argentum/types.py');

function render(doc) {
  const schemas = doc.components?.schemas ?? {};
  const names = Object.keys(schemas).sort();

  const out = [
    '"""Wire types for the Argentum API.',
    '',
    'Generated from apps/backend/openapi/v1.yaml — the same document the server',
    'serves at GET /v1/openapi.json and CI diffs against the gin route tree.',
    '',
    'Do not edit. Run `pnpm --filter @argentum/openapi-tools build` and commit.',
    `Contract version: ${doc.info.version}`,
    '"""',
    '',
    'from __future__ import annotations',
    '',
    'from typing import Any, Dict, List, Literal, TypedDict, Union',
    '',
    `API_VERSION = ${JSON.stringify(doc.info.version)}`,
    '',
  ];

  for (const name of names) {
    const schema = schemas[name];
    if (!isObject(schema)) continue;
    out.push(...classFor(name, schema));
  }

  // Aliases last, because they are the one thing here that is evaluated at
  // import time: `from __future__ import annotations` makes a TypedDict field
  // referring to `Scope` lazy, but `ChatEvent = Union[ChatEventStarted, …]` is
  // an ordinary assignment and every name in it has to exist already.
  out.push('# Type aliases. These are evaluated at import time, so they follow the', '# classes they name.', '');
  const aliases = new Map(names.filter((n) => !isObject(schemas[n])).map((n) => [n, pyType(schemas[n], n)]));
  for (const [name, type] of orderAliases(aliases)) {
    out.push(`${name} = ${type}`, '');
  }

  // `__all__` so `from argentum.types import *` in a REPL brings the names
  // someone is about to type, and nothing else.
  out.push('__all__ = [', `    "API_VERSION",`, ...names.map((n) => `    ${JSON.stringify(n)},`), ']', '');
  return out.join('\n');
}

/**
 * Orders the aliases so each is defined before it is used.
 *
 * Alphabetical would not do: `ChatEvent` is a union over `ChatEventMessage`,
 * which is itself an alias, and sorting by name puts the union first — an
 * import-time `NameError` in a file whose whole job is to be importable.
 */
function orderAliases(aliases) {
  const emitted = new Set();
  const out = [];
  const visit = (name, stack = new Set()) => {
    if (emitted.has(name) || stack.has(name)) return;
    stack.add(name);
    const type = aliases.get(name);
    for (const dep of aliases.keys()) {
      if (dep !== name && new RegExp(`\\b${dep}\\b`).test(type)) visit(dep, stack);
    }
    emitted.add(name);
    out.push([name, type]);
  };
  for (const name of aliases.keys()) visit(name);
  return out;
}

function classFor(name, schema) {
  const required = new Set(schema.required ?? []);
  const props = Object.entries(schema.properties ?? {});
  const lines = [`class ${name}(TypedDict, total=False):`];

  const doc = [];
  if (schema.description) doc.push(...wrap(schema.description.trim(), 72));
  if (required.size) {
    if (doc.length) doc.push('');
    doc.push(`Always present: ${[...required].join(', ')}.`);
  }
  if (doc.length) {
    lines.push('    """' + doc[0]);
    for (const line of doc.slice(1)) lines.push(line ? '    ' + line : '');
    lines.push('    """');
  }

  if (props.length === 0) {
    lines.push('    pass');
  }
  for (const [prop, sub] of props) {
    const comment = firstSentence(sub.description);
    if (comment) lines.push(`    # ${comment}`);
    lines.push(`    ${pyName(prop)}: ${pyType(sub, `${name}_${prop}`)}`);
  }
  lines.push('', '');
  return lines;
}

function pyType(schema, hint) {
  if (!schema || typeof schema !== 'object') return 'Any';
  if (schema.$ref) return schema.$ref.split('/').pop();
  if (Array.isArray(schema.enum)) return `Literal[${schema.enum.map((v) => JSON.stringify(v)).join(', ')}]`;
  if (schema.const !== undefined) return `Literal[${JSON.stringify(schema.const)}]`;
  if (schema.oneOf) return union(schema.oneOf.map((s) => pyType(s, hint)));
  if (schema.anyOf) return union(schema.anyOf.map((s) => pyType(s, hint)));
  // `allOf` in this spec is only ever "this is that, with a description", which
  // is how you attach prose to a `$ref` without the prose being ignored.
  if (schema.allOf) return union(schema.allOf.map((s) => pyType(s, hint)));

  const types = Array.isArray(schema.type) ? schema.type : [schema.type];
  const mapped = types.map((t) => {
    switch (t) {
      case 'string':
        return 'str';
      case 'integer':
        return 'int';
      case 'number':
        return 'float';
      case 'boolean':
        return 'bool';
      case 'null':
        return 'None';
      case 'array':
        return `List[${pyType(schema.items, hint)}]`;
      case 'object':
        // An inline object with no name of its own. `Dict[str, Any]` rather
        // than a synthesised class: a generated name nobody chose is a name
        // that changes when the spec is reordered.
        return schema.properties ? 'Dict[str, Any]' : 'Dict[str, Any]';
      default:
        return 'Any';
    }
  });
  return union(mapped);
}

function union(parts) {
  const unique = [...new Set(parts)];
  if (unique.length === 0) return 'Any';
  if (unique.length === 1) return unique[0];
  if (unique.includes('Any')) return 'Any';
  return `Union[${unique.join(', ')}]`;
}

function isObject(schema) {
  return schema && schema.type === 'object' && schema.properties;
}

/** Python keywords that cannot be a TypedDict field written as an identifier. */
const RESERVED = new Set(['from', 'import', 'class', 'def', 'return', 'None', 'True', 'False', 'in', 'is', 'not', 'or', 'and']);

function pyName(prop) {
  return RESERVED.has(prop) ? `${prop}_` : prop;
}

function firstSentence(text) {
  if (!text) return '';
  const flat = text.trim().replace(/\s+/g, ' ');
  const end = flat.indexOf('. ');
  const sentence = end === -1 ? flat : flat.slice(0, end + 1);
  return sentence.length > 100 ? sentence.slice(0, 97) + '…' : sentence;
}

function wrap(text, width) {
  const out = [];
  for (const paragraph of text.split('\n\n')) {
    let line = '';
    for (const word of paragraph.replace(/\s+/g, ' ').trim().split(' ')) {
      if ((line + ' ' + word).trim().length > width) {
        out.push(line.trim());
        line = word;
      } else {
        line += ' ' + word;
      }
    }
    if (line.trim()) out.push(line.trim());
    out.push('');
  }
  while (out.length && out[out.length - 1] === '') out.pop();
  return out;
}

// Last, not first: the helpers above are `const`, so calling render() at the
// top of the file would read RESERVED before its declaration is initialised.
emit(target, render(doc));
