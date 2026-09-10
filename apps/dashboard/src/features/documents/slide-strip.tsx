import { useState } from "react";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { CarouselImage } from "@/features/chat/carousel-image";

/**
 * Every slide of a carousel, as the horizontal snapping row the reader will
 * swipe on a phone (T-G7).
 *
 * The strip existed before this ticket, but only as a className on a markdown
 * paragraph override — reachable from a chat message and nowhere else. An
 * approval card has no markdown to hang it off, so it lives here now and the
 * renderer keeps its own copy of the layout for the paragraph case, where the
 * children are already-built elements rather than a page count.
 */
export function SlideStrip({
  documentId,
  pages,
  alts,
  className = "",
}: {
  documentId: string;
  pages: number;
  /** One alt per page, in page order. Short falls back to a positional label. */
  alts?: string[];
  className?: string;
}) {
  if (pages < 1) return null;
  return (
    <div
      className={`flex snap-x snap-mandatory gap-2 overflow-x-auto pb-2 ${className}`}
      role="group"
      aria-label={`${pages} slide${pages === 1 ? "" : "s"}`}
    >
      {Array.from({ length: pages }, (_, i) => (
        <CarouselImage
          key={i}
          documentId={documentId}
          page={i + 1}
          alt={alts?.[i] ?? `Slide ${i + 1} of ${pages}`}
        />
      ))}
    </div>
  );
}

/**
 * The caption as it would be pasted, with a button that copies it.
 *
 * Rendered in a `<pre>` rather than a `<p>`: a caption's line breaks are part
 * of it — the blank line before the hashtags is what separates them in every
 * feed this gets pasted into — and prose wrapping would show the reader
 * something other than what the button copies.
 */
export function Caption({ caption, className = "" }: { caption: string; className?: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(caption);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      // A browser that refuses the clipboard — an insecure context, or a
      // permission denied — leaves the caption on screen to select by hand,
      // which is the thing the button was saving rather than enabling.
    }
  }

  if (!caption.trim()) return null;

  return (
    <div className={`rounded-md border border-border bg-muted/40 p-3 ${className}`}>
      <div className="flex items-start justify-between gap-2">
        <pre className="m-0 min-w-0 flex-1 whitespace-pre-wrap break-words font-sans text-xs text-foreground">
          {caption}
        </pre>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => void copy()}
          aria-label={copied ? "Caption copied" : "Copy caption"}
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-positive-ink" />
          ) : (
            <Copy className="h-3.5 w-3.5" />
          )}
          <span className="ml-1.5">{copied ? "Copied" : "Copy caption"}</span>
        </Button>
      </div>
    </div>
  );
}
