// Publishes the client-facing API docs as static pages under the landing
// domain: `docs/api/quickstart.md`, the examples it quotes, the OpenAPI
// contract and the Postman collection.
//
// The sources are NOT copied into the repository. Everything lands in
// `public/docs/`, which is gitignored and regenerated on every `pnpm build` and
// `pnpm dev`, so there is exactly one copy of the quickstart in the tree and it
// is the one CI executes (`docs/api/examples/run.sh`, and the block-equals-file
// check in `packages/openapi-tools`). A second committed copy would be the same
// two-copies-of-one-truth failure the design tokens and the hand-written
// dashboard types both had.
//
// Every relative link in the rendered output is resolved against the files this
// script actually emitted, and an unresolvable one fails the build. A published
// page whose links 404 is the reader-facing version of silent clipping.

import { createHash } from 'node:crypto';
import { cp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { marked } from 'marked';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '../../..');
const out = path.resolve(here, '../public/docs');

const QUICKSTART = path.join(repo, 'docs/api/quickstart.md');
const EXAMPLES = path.join(repo, 'docs/api/examples');
const SPEC = path.join(repo, 'apps/backend/openapi/v1.yaml');
const POSTMAN = path.join(repo, 'apps/backend/docs/postman');

// A file the reader can open, keyed by the extension the example is written in.
// Anything not listed is copied raw and linked as a download.
const LANGUAGE = {
  '.sh': 'bash',
  '.mjs': 'javascript',
  '.js': 'javascript',
  '.py': 'python',
  '.json': 'json',
  '.yaml': 'yaml',
  '.yml': 'yaml',
};

const emitted = new Set();

async function emit(relPath, contents) {
  const dest = path.join(out, relPath);
  await mkdir(path.dirname(dest), { recursive: true });
  await writeFile(dest, contents);
  emitted.add(relPath);
}

async function emitCopy(relPath, from) {
  const dest = path.join(out, relPath);
  await mkdir(path.dirname(dest), { recursive: true });
  await cp(from, dest);
  emitted.add(relPath);
}

function escapeHtml(s) {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// The landing page's own palette and type, inlined rather than imported: this
// page is served without React, and the four values it needs are not worth a
// build graph. They are the same ones `src/index.css` declares in `@theme`.
function page({ title, description, body, nav }) {
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="description" content="${escapeHtml(description)}" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet" />
    <title>${escapeHtml(title)}</title>
    <style>
      :root {
        --bg: #212427; --surface: #2A2D31; --surface-2: #313539;
        --fg: #F0F0EE; --muted: #8A8F98; --border: rgba(255,255,255,0.08);
        --red: #F25C5C; --rose: #FB7185;
      }
      * { box-sizing: border-box; }
      html { scroll-behavior: smooth; scroll-padding-top: 5rem; }
      body {
        margin: 0; background-color: var(--bg); color: var(--fg);
        font-family: Inter, ui-sans-serif, system-ui, sans-serif;
        font-size: 16px; line-height: 1.7; -webkit-font-smoothing: antialiased;
        background-image:
          radial-gradient(1200px 600px at 10% -10%, rgba(242,92,92,0.18), transparent 60%),
          radial-gradient(900px 500px at 110% 0%, rgba(251,113,133,0.14), transparent 60%);
        background-attachment: fixed;
      }
      header {
        position: sticky; top: 0; z-index: 50;
        border-bottom: 1px solid var(--border);
        background: rgba(33,36,39,0.72); backdrop-filter: blur(16px);
      }
      header .bar {
        max-width: 56rem; margin: 0 auto; padding: 0 1.5rem; height: 4rem;
        display: flex; align-items: center; justify-content: space-between; gap: 1rem;
      }
      .brand { display: flex; align-items: center; gap: 0.625rem; text-decoration: none; color: var(--fg); }
      .mark {
        display: grid; place-items: center; height: 1.75rem; width: 1.75rem;
        border-radius: 0.5rem; background: linear-gradient(135deg,#F25C5C,#FB7185);
        color: #212427; font-weight: 700;
      }
      .brand span.name { font-weight: 600; letter-spacing: -0.01em; }
      header nav { display: flex; gap: 0.25rem; flex-wrap: wrap; }
      header nav a {
        border-radius: 9999px; padding: 0.5rem 0.9rem; font-size: 0.875rem;
        color: rgba(255,255,255,0.7); text-decoration: none;
      }
      header nav a:hover { background: rgba(255,255,255,0.05); color: #fff; }
      main { max-width: 56rem; margin: 0 auto; padding: 3.5rem 1.5rem 6rem; }
      h1, h2, h3 { line-height: 1.25; letter-spacing: -0.02em; }
      h1 { font-size: 2.25rem; margin: 0 0 1.5rem; }
      h2 { font-size: 1.5rem; margin: 3rem 0 1rem; padding-top: 1.5rem; border-top: 1px solid var(--border); }
      h3 { font-size: 1.125rem; margin: 2rem 0 0.75rem; }
      p, li { color: rgba(240,240,238,0.82); }
      a { color: var(--red); text-decoration-color: rgba(242,92,92,0.4); text-underline-offset: 3px; }
      a:hover { color: var(--rose); }
      code {
        font-family: "JetBrains Mono", ui-monospace, monospace; font-size: 0.85em;
        background: var(--surface-2); border: 1px solid var(--border);
        border-radius: 0.375rem; padding: 0.1rem 0.35rem;
      }
      pre {
        background: var(--surface); border: 1px solid var(--border);
        border-radius: 0.75rem; padding: 1rem 1.15rem; overflow-x: auto;
      }
      pre code { background: none; border: 0; padding: 0; font-size: 0.82rem; line-height: 1.6; }
      blockquote {
        margin: 1.5rem 0; padding: 0.25rem 1.15rem; border-left: 3px solid var(--red);
        background: rgba(242,92,92,0.06); border-radius: 0 0.5rem 0.5rem 0;
        color: rgba(240,240,238,0.75);
      }
      table { width: 100%; border-collapse: collapse; margin: 1.5rem 0; font-size: 0.9rem; display: block; overflow-x: auto; }
      th, td { border: 1px solid var(--border); padding: 0.6rem 0.8rem; text-align: left; vertical-align: top; }
      th { background: var(--surface); font-weight: 600; }
      hr { border: 0; border-top: 1px solid var(--border); margin: 3rem 0; }
      .lede { color: var(--muted); font-size: 0.95rem; margin: -0.75rem 0 2.5rem; }
      .files { list-style: none; padding: 0; }
      .files li { border-bottom: 1px solid var(--border); padding: 0.75rem 0; }
      .files .raw { color: var(--muted); font-size: 0.8rem; margin-left: 0.75rem; }
      footer {
        border-top: 1px solid var(--border); margin-top: 4rem; padding: 2rem 1.5rem;
        color: rgba(255,255,255,0.4); font-size: 0.8rem; text-align: center;
      }
    </style>
  </head>
  <body>
    <header>
      <div class="bar">
        <a class="brand" href="/"><span class="mark">A</span><span class="name">Argentum</span></a>
        <nav>${nav
          .map((n) => `<a href="${n.href}">${escapeHtml(n.label)}</a>`)
          .join('')}</nav>
      </div>
    </header>
    <main>${body}</main>
    <footer>© 2026 Argentum by Smartsoft, Inc. · <a href="/">argentum.com</a></footer>
  </body>
</html>
`;
}

const NAV = [
  { href: '/docs/', label: 'Quickstart' },
  { href: '/docs/examples/', label: 'Examples' },
  { href: '/docs/v1.yaml', label: 'OpenAPI' },
  { href: '/docs/postman/', label: 'Postman' },
];

// Links written for a reader holding the repository. On the published site the
// repository is not there, so each one is pointed at the copy this script
// emits. A link the map does not cover is caught by checkLinks below rather
// than shipped broken.
const LINK_REWRITES = new Map([
  ['examples/', 'examples/'],
  ['../../apps/backend/openapi/v1.yaml', 'v1.yaml'],
  ['../../apps/backend/docs/postman/', 'postman/'],
]);

async function walk(dir, base = dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) files.push(...(await walk(full, base)));
    else files.push(path.relative(base, full));
  }
  return files.sort();
}

async function buildQuickstart() {
  const md = await readFile(QUICKSTART, 'utf8');
  const renderer = new marked.Renderer();
  const baseLink = renderer.link.bind(renderer);
  renderer.link = (token) => {
    const href = token.href;
    if (!/^(https?:|mailto:|#|\/)/.test(href)) {
      const rewritten = LINK_REWRITES.get(href);
      if (!rewritten) {
        throw new Error(
          `quickstart.md links to "${href}", which does not exist on the published site.\n` +
            `Add it to LINK_REWRITES in apps/landing/scripts/build-docs.mjs, or make the link absolute.`,
        );
      }
      token.href = rewritten;
    }
    return baseLink(token);
  };

  // marked stopped emitting heading ids in v12, and a reference page whose
  // sections cannot be linked is a page support has to describe by scrolling
  // ("the bit about 504s") instead of by URL.
  const slugs = new Map();
  renderer.heading = function heading(token) {
    const text = this.parser.parseInline(token.tokens);
    const base =
      token.text
        .toLowerCase()
        .replace(/[^\w\s-]/g, '')
        .trim()
        .replace(/\s+/g, '-') || 'section';
    const seen = slugs.get(base) ?? 0;
    slugs.set(base, seen + 1);
    const id = seen === 0 ? base : `${base}-${seen}`;
    return `<h${token.depth} id="${id}">${text}</h${token.depth}>\n`;
  };

  const html = marked.parse(md, { renderer, gfm: true });
  await emit(
    'index.html',
    page({
      title: 'Argentum API — quickstart',
      description:
        'Ten minutes from an empty directory to a branded PDF: keys, reports, chat, and the five things worth knowing before production.',
      nav: NAV,
      body: html,
    }),
  );
}

async function buildExamples() {
  const files = await walk(EXAMPLES);
  const runnable = files.filter((f) => f !== 'run.sh');

  for (const rel of runnable) {
    const source = await readFile(path.join(EXAMPLES, rel), 'utf8');
    const lang = LANGUAGE[path.extname(rel)] ?? '';
    await emitCopy(path.join('examples/raw', rel), path.join(EXAMPLES, rel));
    await emit(
      path.join('examples', `${rel}.html`),
      page({
        title: `${rel} — Argentum API examples`,
        description: `The ${rel} example, executed by CI against a live server.`,
        nav: NAV,
        body:
          `<h1>${escapeHtml(rel)}</h1>` +
          `<p class="lede">CI runs this file against a real server. ` +
          `<a href="/docs/examples/raw/${rel}">Raw file</a> · ` +
          `<a href="/docs/examples/">All examples</a></p>` +
          `<pre><code class="language-${lang}">${escapeHtml(source)}</code></pre>`,
      }),
    );
  }

  const list = runnable
    .map(
      (rel) =>
        `<li><a href="/docs/examples/${rel}.html"><code>${escapeHtml(rel)}</code></a>` +
        `<a class="raw" href="/docs/examples/raw/${rel}">raw</a></li>`,
    )
    .join('');

  await emit(
    'examples/index.html',
    page({
      title: 'Argentum API — examples',
      description: 'Every code block in the quickstart, as a file CI executes.',
      depth: 1,
      nav: NAV,
      body:
        '<h1>Examples</h1>' +
        '<p class="lede">Every fenced block in the ' +
        '<a href="/docs/">quickstart</a> is one of these files, and CI executes them — ' +
        'the deterministic ones on every push, the ones that spend LLM tokens nightly. ' +
        'A block that has drifted from its file is a red build.</p>' +
        `<ul class="files">${list}</ul>`,
    }),
  );
}

async function buildSpec() {
  await emitCopy('v1.yaml', SPEC);
}

async function buildPostman() {
  const files = await walk(POSTMAN);
  for (const rel of files) await emitCopy(path.join('postman', rel), path.join(POSTMAN, rel));

  const list = files
    .map((rel) => `<li><a href="/docs/postman/${rel}"><code>${escapeHtml(rel)}</code></a></li>`)
    .join('');

  await emit(
    'postman/index.html',
    page({
      title: 'Argentum API — Postman collection',
      description: 'A Postman collection and environment, generated from the OpenAPI contract.',
      depth: 1,
      nav: NAV,
      body:
        '<h1>Postman</h1>' +
        '<p class="lede">Generated from <a href="/docs/v1.yaml">the contract</a> and ' +
        'regenerated by CI, so it cannot drift from the routes. Import both files, then set ' +
        '<code>base_url</code> and <code>api_key</code> in the environment.</p>' +
        `<ul class="files">${list}</ul>`,
    }),
  );
}

// Every relative href in the emitted HTML has to resolve to something this
// script wrote. Catches a rewrite that stopped matching, a renamed example, and
// a directory link with no index.html behind it — all of which are 404s the
// author never sees, because the author has the repository.
function checkLinks(html, fromRel) {
  const broken = [];
  for (const match of html.matchAll(/(?:href|src)="([^"]+)"/g)) {
    const href = match[1];
    if (/^(https?:|mailto:|data:|#)/.test(href)) continue;

    let target = href.startsWith('/')
      ? href.replace(/^\/docs\/?/, '')
      : path.posix.join(path.posix.dirname(fromRel), href);

    if (href === '/' || href === '/favicon.svg' || target.startsWith('..')) continue;
    if (target === '' || target.endsWith('/')) target = path.posix.join(target, 'index.html');
    if (!emitted.has(target)) broken.push(`${href} → ${target}`);
  }
  return broken;
}

async function verify() {
  const problems = [];
  for (const rel of emitted) {
    if (!rel.endsWith('.html')) continue;
    const html = await readFile(path.join(out, rel), 'utf8');
    for (const broken of checkLinks(html, rel)) problems.push(`  ${rel}: ${broken}`);
  }
  if (problems.length) {
    throw new Error(`Published docs contain links to files that were never emitted:\n${problems.join('\n')}`);
  }
}

async function main() {
  await rm(out, { recursive: true, force: true });
  await mkdir(out, { recursive: true });

  await buildQuickstart();
  await buildExamples();
  await buildSpec();
  await buildPostman();
  await verify();

  const digest = createHash('sha256').update(await readFile(QUICKSTART)).digest('hex').slice(0, 12);
  console.log(`docs: ${emitted.size} files in public/docs (quickstart ${digest})`);
}

await main();
