import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

/**
 * Syntax-highlighted code (T-U6).
 *
 * ## Why this file loads shiki by hand
 *
 * Importing `shiki` loads every language and every theme it ships — megabytes,
 * on the critical path of a dashboard whose main chunk is already 1.4 MB. This
 * builds the highlighter from `shiki/core` instead and hands it exactly two
 * grammars and two themes.
 *
 * It also uses `shiki/engine/javascript` rather than the Oniguruma one, which
 * means **no WebAssembly at all** — the ~1 MB `onig.wasm` the ticket flagged as
 * a regression risk is simply never fetched. The JS engine cannot compile a
 * handful of exotic grammars; SQL and JSON are not among them.
 *
 * The whole module is behind a dynamic `import()` so none of it appears in the
 * initial chunk. Until it resolves, the plain `<pre>` below renders — which is
 * exactly what this component replaced, so the un-highlighted state is the old
 * behaviour rather than a blank space.
 */

/** The languages Argentum actually renders: SQL from `run_sql`, JSON from tool
 *  payloads. Anything else falls through to unhighlighted text rather than
 *  pulling a grammar nobody asked for. */
const LANGS = ["sql", "json"] as const;
type Lang = (typeof LANGS)[number];

function isSupported(lang: string): lang is Lang {
  return (LANGS as readonly string[]).includes(lang);
}

/** Resolved once and shared. A second `<CodeBlock>` must not build a second
 *  highlighter, and React 18's StrictMode double-invoke would do exactly that
 *  without this. */
let highlighterPromise: Promise<{
  codeToHtml: (
    code: string,
    opts: {
      lang: string;
      themes: { light: string; dark: string };
      defaultColor: false;
    },
  ) => string;
}> | null = null;

async function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = (async () => {
      const [{ createHighlighterCore }, { createJavaScriptRegexEngine }] =
        await Promise.all([
          import("shiki/core"),
          import("shiki/engine/javascript"),
        ]);
      return createHighlighterCore({
        themes: [
          import("shiki/themes/github-light.mjs"),
          import("shiki/themes/github-dark.mjs"),
        ],
        langs: [import("shiki/langs/sql.mjs"), import("shiki/langs/json.mjs")],
        engine: createJavaScriptRegexEngine(),
      });
    })();
  }
  return highlighterPromise;
}

export function CodeBlock({
  code,
  lang,
  className,
}: {
  code: string;
  lang: string;
  className?: string;
}) {
  const [html, setHtml] = useState<string | null>(null);

  useEffect(() => {
    if (!isSupported(lang)) return;
    let live = true;
    void getHighlighter()
      .then((h) => {
        if (!live) return;
        setHtml(
          h.codeToHtml(code, {
            lang,
            // Both themes at once, so flipping `.dark` re-colours the block
            // with no re-highlight and no second render.
            themes: { light: "github-light", dark: "github-dark" },
            // Emit *only* the `--shiki-light` / `--shiki-dark` custom
            // properties and no `color:` of its own.
            //
            // Without this shiki writes the light theme's colour inline on
            // every span. An inline style beats a stylesheet rule at any
            // specificity, so `.dark .shiki span { color: var(--shiki-dark) }`
            // in index.css lost to it and dark mode rendered light-theme greys
            // on a dark ground — legible in the screenshot only as the absence
            // of half the query.
            defaultColor: false,
          }),
        );
      })
      .catch(() => {
        // A grammar that fails to load leaves `html` null and the plain <pre>
        // on screen. Code the reader can still read beats an error boundary.
      });
    return () => {
      live = false;
    };
  }, [code, lang]);

  const shell =
    "my-2 overflow-x-auto rounded-md border border-border bg-inset p-3 text-[11px] leading-relaxed [&_pre]:!bg-transparent [&_code]:!bg-transparent";

  if (html) {
    return (
      <div
        className={cn(shell, "shiki-block", className)}
        // Safe: this is shiki's own output over text the agent produced, and
        // shiki escapes the source it highlights. No user HTML reaches it.
        dangerouslySetInnerHTML={{ __html: html }}
      />
    );
  }

  return (
    <pre className={cn(shell, "font-mono whitespace-pre-wrap", className)}>
      {code}
    </pre>
  );
}
