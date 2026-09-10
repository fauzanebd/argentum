import { useQuery } from "@tanstack/react-query";
import { isAxiosError } from "axios";

import { api } from "@/lib/api";

/**
 * The words beside a carousel's slides (T-G7).
 *
 * `caption` is the string as it would be pasted — text, blank line, hashtags —
 * assembled by the backend rather than here, so "Copy caption" copies what a
 * channel was actually sent instead of a second assembly of the same parts.
 */
export type Carousel = {
  caption: string;
  text?: string;
  hashtags?: string[];
  /** One alt per page, in page order. */
  alts?: string[];
  pages: number;
};

export const carouselKey = (documentId: string) => ["carousel", documentId];

/**
 * Reads a document's carousel manifest, and is how a caller finds out that a
 * document *is* a carousel.
 *
 * The route answers 404 for every other format, so an approval card naming an
 * ordinary PDF gets `undefined` here and renders exactly as it did before this
 * ticket — which is the second half of T-G7's acceptance. A 404 is therefore a
 * normal answer rather than a failure, and is not retried; anything else keeps
 * the default retry, because a manifest that failed to read once is worth
 * asking for twice.
 */
export function useCarousel(documentId: string | null | undefined) {
  return useQuery({
    queryKey: carouselKey(documentId ?? ""),
    enabled: Boolean(documentId),
    queryFn: async () => (await api.get<Carousel>(`/documents/${documentId}/carousel`)).data,
    retry: (count, err) => !(isAxiosError(err) && err.response?.status === 404) && count < 2,
    staleTime: 5 * 60 * 1000,
  });
}
