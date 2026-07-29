#!/usr/bin/env node
// Generates apps/backend/docs/postman/ from the spec.
//
//   node scripts/gen-postman.mjs            # write
//   node scripts/gen-postman.mjs --check    # fail if the committed copy is stale
//
// The collection it replaces was hand-maintained and had rotted past the point
// of being misleading: it described a server with no auth layer and a tenant
// fixed at startup by `TENANT_ID`, which stopped being true several releases
// before the monorepo. That is the argument for generating it — a collection
// nobody regenerates is a collection that documents a system that no longer
// exists, and it looks authoritative right up until someone presses Send.
//
// **Only `/v1` is in here now.** The dashboard's `/api` routes are first-party:
// they change with the dashboard, they are not a published contract, and
// nothing outside this repository should be calling them.
import { resolve } from 'node:path';
import { deref, emit, loadSpec, operations, repoRoot } from '../lib/spec.mjs';

const doc = loadSpec();
const out = resolve(repoRoot, 'apps/backend/docs/postman');

emit(resolve(out, 'argentum.postman_collection.json'), JSON.stringify(collection(doc), null, 2) + '\n');
emit(resolve(out, 'argentum.postman_environment.json'), JSON.stringify(environment(), null, 2) + '\n');

function collection(doc) {
  // Requests are grouped by their tag, in the order the spec lists tags, so the
  // folder order is a reading order rather than an alphabetical one.
  const folders = new Map((doc.tags ?? []).map((t) => [t.name, { name: t.name, description: t.description, item: [] }]));
  const untagged = { name: 'Other', item: [] };

  for (const { method, path, op } of operations(doc)) {
    const folder = folders.get(op.tags?.[0]) ?? untagged;
    folder.item.push(request(doc, method, path, op));
  }
  const item = [...folders.values(), untagged].filter((f) => f.item.length > 0);

  return {
    info: {
      name: `${doc.info.title} — /v1`,
      // A stable id, because Postman treats it as the collection's identity:
      // a fresh one on every generation would re-import as a duplicate rather
      // than as an update.
      _postman_id: 'argentum-v1-generated',
      description: [
        `Generated from apps/backend/openapi/v1.yaml (contract version ${doc.info.version}).`,
        '',
        'Do not edit by hand — run `pnpm --filter @argentum/openapi-tools build` and commit the result.',
        '',
        'Set `apiKey` in the environment first. `GET /v1/me` is the call to make before any other:',
        'it tells you which scopes your key has, what it may spend, and which contract version you',
        'are talking to.',
      ].join('\n'),
      schema: 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json',
    },
    auth: {
      type: 'bearer',
      bearer: [{ key: 'token', value: '{{apiKey}}', type: 'string' }],
    },
    variable: [
      { key: 'baseUrl', value: 'http://localhost:8080', type: 'string' },
      { key: 'apiKey', value: 'arg_replace_me', type: 'string' },
      { key: 'userRef', value: 'u_42', type: 'string' },
      { key: 'reportId', value: '00000000-0000-0000-0000-000000000000', type: 'string' },
      { key: 'documentId', value: '00000000-0000-0000-0000-000000000000', type: 'string' },
      { key: 'threadId', value: '00000000-0000-0000-0000-000000000000', type: 'string' },
    ],
    item,
  };
}

function request(doc, method, path, op) {
  const params = (op.parameters ?? []).map((p) => deref(doc, p));
  const headers = [];
  const query = [];

  for (const p of params) {
    if (p.in === 'header') {
      headers.push({
        key: p.name,
        // `{{$guid}}` is Postman's own dynamic variable: a fresh UUID per send,
        // which is exactly what an idempotency key should be. A hard-coded one
        // would make the second Send a replay of the first, and someone would
        // report that as a bug in the API.
        value: p.name === 'Idempotency-Key' ? '{{$guid}}' : '',
        type: 'text',
        disabled: !p.required,
        description: firstLine(p.description),
      });
    }
    if (p.in === 'query') {
      query.push({
        key: p.name,
        value: variableFor(p.name),
        disabled: true,
        description: firstLine(p.description),
      });
    }
  }

  const body = requestBody(doc, op);
  if (body) headers.unshift({ key: 'Content-Type', value: 'application/json', type: 'text' });
  headers.unshift({ key: 'Accept', value: accept(op), type: 'text' });

  const segments = path.replace(/^\//, '').split('/');
  const pathVars = [];
  const postmanSegments = segments.map((seg) => {
    const match = /^\{(.+)\}$/.exec(seg);
    if (!match) return seg;
    const name = variableFor(pathVarName(path, match[1]));
    pathVars.push({ key: match[1], value: name });
    return `:${match[1]}`;
  });

  return {
    name: op.summary ?? `${method} ${path}`,
    request: {
      method,
      header: headers,
      ...(body ? { body: { mode: 'raw', raw: body, options: { raw: { language: 'json' } } } } : {}),
      url: {
        raw: `{{baseUrl}}/${postmanSegments.join('/')}${query.length ? '?' + query.map((q) => `${q.key}=${q.value}`).join('&') : ''}`,
        host: ['{{baseUrl}}'],
        path: postmanSegments,
        ...(query.length ? { query } : {}),
        ...(pathVars.length ? { variable: pathVars } : {}),
      },
      description: description(op),
    },
  };
}

/**
 * Which `Accept` to send.
 *
 * It matters on two routes rather than being cosmetic: `Accept` is what
 * chooses between a document object and the file's bytes on the render door,
 * and between a stream and a completed turn on `POST /v1/chat`. Postman renders
 * neither binary nor SSE usefully, so both default to JSON here.
 */
function accept(op) {
  const types = Object.keys(op.responses?.['200']?.content ?? op.responses?.['202']?.content ?? {});
  if (types.includes('application/json')) return 'application/json';
  return types[0] ?? 'application/json';
}

function requestBody(doc, op) {
  const media = op.requestBody?.content?.['application/json'];
  if (!media) return null;
  if (media.example !== undefined) return JSON.stringify(media.example, null, 2);
  return JSON.stringify(synthesize(doc, deref(doc, media.schema), 0), null, 2);
}

/**
 * Builds a placeholder body for an operation with no curated example.
 *
 * Required properties only, one level of nesting per array: a skeleton someone
 * fills in beats an empty `{}`, and a full expansion of every optional field
 * beats neither.
 */
function synthesize(doc, schema, depth) {
  if (!schema || depth > 4) return null;
  schema = deref(doc, schema);
  if (schema.example !== undefined) return schema.example;
  if (Array.isArray(schema.examples) && schema.examples.length) return schema.examples[0];
  if (schema.const !== undefined) return schema.const;
  if (Array.isArray(schema.enum) && schema.enum.length) return schema.enum[0];
  if (schema.oneOf) return synthesize(doc, schema.oneOf[0], depth + 1);
  if (schema.allOf) return synthesize(doc, schema.allOf[0], depth + 1);

  const type = Array.isArray(schema.type) ? schema.type[0] : schema.type;
  switch (type) {
    case 'object': {
      const out = {};
      for (const name of schema.required ?? []) {
        out[name] = synthesize(doc, schema.properties?.[name], depth + 1);
      }
      return out;
    }
    case 'array':
      return [synthesize(doc, schema.items, depth + 1)];
    case 'integer':
    case 'number':
      return 0;
    case 'boolean':
      return false;
    default:
      return schema.format === 'date-time' ? '2026-07-28T00:00:00Z' : '';
  }
}

/** Maps a parameter to the collection variable that stands in for it. */
function variableFor(name) {
  switch (name) {
    case 'user_ref':
      return '{{userRef}}';
    case 'reportId':
      return '{{reportId}}';
    case 'documentId':
      return '{{documentId}}';
    case 'threadId':
      return '{{threadId}}';
    default:
      return '';
  }
}

/**
 * Every path parameter in this spec is called `id`, because it is the id of
 * whatever the path names. Postman variables are collection-wide, so they need
 * the resource back: `/v1/reports/{id}` uses `{{reportId}}`.
 */
function pathVarName(path, param) {
  if (param !== 'id') return param;
  if (path.startsWith('/v1/reports')) return 'reportId';
  if (path.startsWith('/v1/documents')) return 'documentId';
  if (path.startsWith('/v1/threads')) return 'threadId';
  return param;
}

function description(op) {
  const parts = [];
  if (op.description) parts.push(op.description.trim());
  if (op['x-argentum-scope']) parts.push(`Scope: \`${op['x-argentum-scope']}\``);
  else parts.push('Scope: none — any key reaches this route.');
  return parts.join('\n\n');
}

function firstLine(text) {
  if (!text) return undefined;
  const line = text.trim().split('\n')[0].trim();
  return line.length > 160 ? line.slice(0, 157) + '…' : line;
}

function environment() {
  return {
    id: 'argentum-v1-local',
    name: 'Argentum /v1 — local',
    values: [
      { key: 'baseUrl', value: 'http://localhost:8080', type: 'default', enabled: true },
      { key: 'apiKey', value: 'arg_replace_me', type: 'secret', enabled: true },
      { key: 'userRef', value: 'u_42', type: 'default', enabled: true },
      { key: 'reportId', value: '', type: 'default', enabled: true },
      { key: 'documentId', value: '', type: 'default', enabled: true },
      { key: 'threadId', value: '', type: 'default', enabled: true },
    ],
    _postman_variable_scope: 'environment',
  };
}
