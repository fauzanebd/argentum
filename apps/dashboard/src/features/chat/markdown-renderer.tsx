import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ExternalLink } from "lucide-react";
import { CodeBlock } from "@/components/ui/code-block";

interface MarkdownRendererProps {
  content: string;
}

export function MarkdownRenderer({ content }: MarkdownRendererProps) {
  return (
    <div className="prose prose-sm dark:prose-invert max-w-none prose-p:my-1.5 prose-ul:my-1.5 prose-ol:my-1.5 prose-li:my-0.5">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children }) => {
            const isDashboard =
              typeof href === "string" &&
              (href.includes("/metabase/public/dashboard/") ||
                href.includes("/metabase/public/card/"));
            return (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className={
                  isDashboard
                    ? "inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground no-underline hover:bg-primary/90 transition-colors"
                    : "text-primary underline underline-offset-2 hover:text-primary/80"
                }
              >
                {children}
                {isDashboard && <ExternalLink className="h-3 w-3" />}
              </a>
            );
          },
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
