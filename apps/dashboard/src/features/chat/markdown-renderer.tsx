import { lazy, Suspense } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { CodeBlock } from "@/components/ui/code-block";

// recharts is ~390 kB and most turns never draw a chart, so the panel renderer
// arrives with the first dashboard link rather than with the app.
const DashboardView = lazy(() =>
  import("@/features/dashboards/dashboard-view").then((m) => ({
    default: m.DashboardView,
  })),
);

interface MarkdownRendererProps {
  content: string;
}

/**
 * A link to a native dashboard, or null.
 *
 * The agent is told to return the URL as a markdown link with descriptive text,
 * so the link is where the chart belongs: the sentence around it is the caption
 * somebody wrote for it, and swapping the anchor for the panels puts the answer
 * where the reader is already looking.
 *
 * Matched by shape rather than by a marker in the message, because the message
 * is the model's own prose and a marker is one more thing for it to get wrong.
 */
const DASHBOARD_HREF =
  /^(?:https?:\/\/[^/]+)?\/dashboards\/([0-9a-fA-F-]{36})\/?$/;

function dashboardIdFrom(href: unknown): string | null {
  if (typeof href !== "string") return null;
  return DASHBOARD_HREF.exec(href)?.[1] ?? null;
}

/**
 * Whether this paragraph is going to contain a dashboard embed.
 *
 * Found by the browser gate of 2026-08-17: a link lives inside the paragraph
 * react-markdown built for the prose around it, so swapping the anchor for
 * `DashboardView` mounted a `<section>` — a grid, three panels, a header — as a
 * child of a `<p>`. React inserts that happily and it renders, because nothing
 * ever re-parses the markup. An HTML *parser* does not: it closes the `<p>` at
 * the block element and moves what follows out of it, so any path that parses
 * this instead of constructing it — server rendering, then hydration — gets a
 * different tree than the one React expects.
 *
 * **The question is asked of the markdown node, not of the rendered children.**
 * react-markdown hands `p` the *component* it will call for the anchor, not
 * what that component returns, so a check on `child.type` sees the `a`
 * override and never the embed — which is the first way this fix was written
 * and the reason it changed nothing. The hast node is the thing that already
 * knows: it carries the href before anybody decides what to draw for it.
 *
 * Nested because a link can arrive wrapped — `**[title](/dashboards/…)**` is a
 * paragraph whose child is `strong`.
 */
function hasDashboardLink(node: unknown): boolean {
  if (!node || typeof node !== "object") return false;
  const el = node as { tagName?: string; properties?: Record<string, unknown>; children?: unknown[] };
  if (el.tagName === "a" && dashboardIdFrom(el.properties?.href)) return true;
  return (el.children ?? []).some(hasDashboardLink);
}

export function MarkdownRenderer({ content }: MarkdownRendererProps) {
  return (
    <div className="prose prose-sm dark:prose-invert max-w-none prose-p:my-1.5 prose-ul:my-1.5 prose-ol:my-1.5 prose-li:my-0.5">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children }) => {
            // A native dashboard renders where the link is, live: the panels
            // re-query on open, so what the reader sees is the warehouse now
            // rather than what it said when the turn ran.
            const embedded = dashboardIdFrom(href);
            if (embedded) {
              return (
                <Suspense
                  fallback={
                    <div className="my-2 rounded-xl border border-border bg-card/40 p-4 text-xs text-muted-foreground">
                      Loading dashboard…
                    </div>
                  }
                >
                  <DashboardView id={embedded} compact />
                </Suspense>
              );
            }
            return (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary underline underline-offset-2 hover:text-primary/80"
              >
                {children}
              </a>
            );
          },
          // A paragraph holding an embed becomes a <div>, and only that one.
          // `<p>` may contain phrasing content and nothing else, so the tag has
          // to give way to the panels rather than the panels to the tag —
          // and a div carrying the same spacing keeps prose that shares the
          // paragraph ("Here it is: <link> — note the window") reading as it
          // did. Every other paragraph in every other message is untouched.
          p: ({ node, children }) =>
            hasDashboardLink(node) ? (
              <div className="my-1.5">{children}</div>
            ) : (
              <p>{children}</p>
            ),
          strong: ({ children }) => (
            <strong className="font-semibold">{children}</strong>
          ),
          ul: ({ children }) => (
            <ul className="list-disc pl-4 space-y-0.5">{children}</ul>
          ),
          ol: ({ children }) => (
            <ol className="list-decimal pl-4 space-y-0.5">{children}</ol>
          ),
          // react-markdown routes both `inline code` and ``` fences ``` through
          // this one component, and tells them apart by the language class it
          // puts on a fence. Inline stays a plain chip: a highlighter on a
          // three-word span costs a module load and buys nothing (T-U6).
          code: ({ className, children }) => {
            const lang = /language-(\w+)/.exec(className ?? "")?.[1];
            if (!lang) {
              return (
                <code className="rounded bg-muted px-1 py-0.5 text-xs font-mono">
                  {children}
                </code>
              );
            }
            return <CodeBlock code={String(children).replace(/\n$/, "")} lang={lang} />;
          },
          // The fence's own <pre> would wrap CodeBlock in a second scroll
          // container with its own padding. CodeBlock is the block element.
          pre: ({ children }) => <>{children}</>,
          table: ({ children }) => (
            <div className="my-2 -mx-1 overflow-x-auto">
              <table className="min-w-full text-sm">{children}</table>
            </div>
          ),
        }}
      >
        {content}
      </Markdown>
    </div>
  );
}
