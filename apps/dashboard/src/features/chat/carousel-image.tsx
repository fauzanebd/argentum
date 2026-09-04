import { useEffect, useState } from "react";

import { api } from "@/lib/api";

/**
 * A carousel slide inside a message (T-G6, decision 6).
 *
 * A persisted message never carries a presigned image URL — the presign TTL
 * is an hour and an `<img>` cannot be re-signed on click the way a link can —
 * so the slide is `/api/documents/:id/pages/:n`, an authenticated route. A
 * bare `<img src>` cannot send the bearer header the API wants, so the bytes
 * are fetched through the same axios client every other request uses and
 * shown from an object URL, revoked on unmount. The alt is what the reader
 * gets while it loads and if it cannot.
 */

/** A same-origin page URL, with or without the host. */
const PAGE_HREF = /^(?:https?:\/\/[^/]+)?\/api\/documents\/([0-9a-fA-F-]{36})\/pages\/(\d{1,3})\/?$/;

export function pageFrom(src: unknown): { documentId: string; page: number } | null {
  if (typeof src !== "string") return null;
  const m = PAGE_HREF.exec(src);
  return m ? { documentId: m[1], page: Number(m[2]) } : null;
}

/** Whether a markdown paragraph node holds at least one slide image. */
export function hasPageImage(node: unknown): boolean {
  if (!node || typeof node !== "object") return false;
  const el = node as { tagName?: string; properties?: Record<string, unknown>; children?: unknown[] };
  if (el.tagName === "img" && pageFrom(el.properties?.src)) return true;
  return (el.children ?? []).some(hasPageImage);
}

export function CarouselImage({
  documentId,
  page,
  alt,
  className = "w-56 snap-start rounded-lg border border-border",
}: {
  documentId: string;
  page: number;
  alt: string;
  /** Sizing: the strip's card by default; the documents list passes a thumbnail. */
  className?: string;
}) {
  const [url, setUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let objectUrl: string | null = null;
    let cancelled = false;
    setUrl(null);
    setFailed(false);
    api
      .get<Blob>(`/documents/${documentId}/pages/${page}`, { responseType: "blob" })
      .then((res) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(res.data);
        setUrl(objectUrl);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [documentId, page]);

  return (
    <figure
      className={`m-0 shrink-0 overflow-hidden bg-muted/40 ${className}`}
      style={{ aspectRatio: "4 / 5" }}
      aria-label={alt}
    >
      {url ? (
        <img src={url} alt={alt} className="h-full w-full object-cover" loading="lazy" />
      ) : failed ? (
        <figcaption className="flex h-full items-center justify-center p-3 text-center text-xs text-muted-foreground">
          {alt}
        </figcaption>
      ) : (
        <div className="h-full w-full animate-pulse bg-muted" aria-hidden />
      )}
    </figure>
  );
}
