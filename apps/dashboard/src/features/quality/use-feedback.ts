import { useQuery } from "@tanstack/react-query";
import type { FeedbackListResponse, FeedbackSummaryResponse } from "@argentum/api-types";
import { api } from "@/lib/api";

/**
 * The two reads behind Answer quality (T-Q16).
 *
 * Both routes shipped with `T-Q2` and neither had a caller until this file
 * existed — the product collected the one signal a tenant gives us that an
 * answer was wrong, stored it, used it to exclude a turn from the cookbook, and
 * then showed it to nobody.
 *
 * The types are generated from the Go that serves them
 * (`handlers/wire.go`, via `make types`), which is not a detail here: the
 * hand-written half of exactly this arrangement is what shipped two P1s on the
 * embed surface earlier the same day.
 */

/** How many verdicts one screen shows. The list is a triage queue, not an
 *  archive — somebody reading twenty rows is looking for a pattern, and
 *  scrolling past a hundred is how they stop looking. */
export const FEEDBACK_PAGE_SIZE = 50;

export function useFeedbackSummary(from?: string, to?: string) {
  return useQuery({
    queryKey: ["feedback", "summary", from, to],
    queryFn: async () => {
      const params: Record<string, string> = {};
      if (from) params.from = new Date(from).toISOString();
      if (to) params.to = new Date(to).toISOString();
      const res = await api.get<FeedbackSummaryResponse>("/feedback/summary", { params });
      return res.data;
    },
  });
}

export function useFeedbackList(onlyNegative: boolean) {
  return useQuery({
    queryKey: ["feedback", "list", onlyNegative],
    queryFn: async () => {
      const res = await api.get<FeedbackListResponse>("/feedback", {
        params: { only_negative: String(onlyNegative), limit: FEEDBACK_PAGE_SIZE },
      });
      return res.data;
    },
  });
}
