import { useEffect, useState } from "react";

import { api } from "@/lib/api";

/**
 * Fetch an authenticated image and hand back a URL an `<img>` can use.
 *
 * Every picture this product shows in the dashboard sits behind a bearer
 * token: a carousel slide on `/api/documents/:id/pages/:n` (T-G6) and a
 * library image on `/api/post-images/:id/content` (T-G12). A bare `<img src>`
 * cannot send that header, and neither route is presigned on purpose — a
 * presigned URL inside a persisted message or a picker somebody leaves open is
 * a broken image an hour later.
 *
 * So the bytes come through the same axios client every other request uses and
 * are shown from an object URL, revoked on unmount and on every change of
 * path. Extracted from `CarouselImage` when the picture library needed the
 * same three lines: two implementations of "show an authenticated image" is
 * two places for the revoke to be forgotten.
 */
export function useObjectUrl(path: string): { url: string | null; failed: boolean } {
  const [url, setUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let objectUrl: string | null = null;
    let cancelled = false;
    setUrl(null);
    setFailed(false);
    api
      .get<Blob>(path, { responseType: "blob" })
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
  }, [path]);

  return { url, failed };
}
