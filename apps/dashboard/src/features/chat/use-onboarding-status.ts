import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { DBConnection } from "@argentum/api-types";
import { api } from "@/lib/api";

/** How often the new-chat screen asks whether the describer has finished. */
export const SCHEMA_POLL_MS = 5_000;
/** How long after mount it keeps asking. The describer is one light-model call
 *  per source and normally lands inside a minute; five is long enough that a
 *  timeout means something is wrong rather than slow. */
export const SCHEMA_WAIT_MS = 5 * 60 * 1000;

/**
 * Where a workspace is between connecting data and its first answer (RIZ-76),
 * as the new-chat screen reads it.
 *
 * - `unknown`   — the source list has not loaded, or could not: show nothing,
 *                 which is the screen as it was before any of this.
 * - `no-data`   — nothing connected: the connect-your-data card.
 * - `reading`   — a source has no description yet, so the describer (and the
 *                 business inference queued beside it) is still working.
 * - `ready`     — it was reading during this visit and has finished.
 * - `timeout`   — it was reading and has not finished in SCHEMA_WAIT_MS.
 * - `described` — every source was described before this visit: no notice.
 */
export type OnboardingStatus = "unknown" | "no-data" | "reading" | "ready" | "timeout" | "described";

/** An empty description is the describer's "not yet": `description_source`
 *  is "" until it writes one, and a manual description is never empty. */
function undescribed(list: DBConnection[] | null | undefined): boolean {
  return !!list?.some((c) => !c.description?.trim());
}

/**
 * The workspace's onboarding state for the new-chat screen (RIZ-76).
 *
 * The readiness signal is the connection list's descriptions rather than a new
 * endpoint: the describer writes one per source when it finishes, and an empty
 * one is the only "still working" this product records. Same query key as
 * Settings → Connections, so the two can never disagree about what is
 * connected.
 *
 * Polls only while something is undescribed and only for SCHEMA_WAIT_MS after
 * mount, and refetches the roster on each tick so the default agent's starter
 * questions move from the generic three to the workspace's own tables as soon
 * as the inference writes them — without a reload.
 *
 * `active` is the new-chat screen. A thread never shows the notice, so it
 * neither fetches nor polls.
 */
export function useOnboardingStatus(active: boolean): {
  status: OnboardingStatus;
  /** At least one source is connected — the condition for offering the
   *  default agent's questions about it. */
  hasData: boolean;
} {
  const qc = useQueryClient();
  const [timedOut, setTimedOut] = useState(false);
  // Whether this visit has seen a source being read. It is what separates
  // "ready" (worth saying, once) from "described" (nothing to say).
  const [sawReading, setSawReading] = useState(false);

  const { data, dataUpdatedAt } = useQuery({
    queryKey: ["connections"],
    queryFn: async () =>
      (await api.get<{ connections: DBConnection[] | null }>("/connections")).data.connections ?? [],
    enabled: active,
    refetchInterval: (q) =>
      active && !timedOut && undescribed(q.state.data) ? SCHEMA_POLL_MS : false,
  });
  // Settings → Connections shares the key, and a Go nil slice arrives as null.
  // `undefined` alone means "not loaded".
  const list = data === undefined ? undefined : (data ?? []);
  const reading = undescribed(list);

  useEffect(() => {
    if (reading) setSawReading(true);
  }, [reading]);

  useEffect(() => {
    if (!active) return;
    const t = setTimeout(() => setTimedOut(true), SCHEMA_WAIT_MS);
    return () => clearTimeout(t);
  }, [active]);

  // Each poll is also the moment to ask for the questions again: the source
  // profile they are built from is written by the inference job queued beside
  // the describer, and there is no other signal that it has landed.
  useEffect(() => {
    if (!active || !sawReading || timedOut) return;
    void qc.invalidateQueries({ queryKey: ["agents"] });
  }, [active, sawReading, timedOut, dataUpdatedAt, qc]);

  let status: OnboardingStatus;
  if (!active || list === undefined) status = "unknown";
  else if (list.length === 0) status = "no-data";
  else if (reading) status = timedOut ? "timeout" : "reading";
  else status = sawReading ? "ready" : "described";

  return { status, hasData: !!list && list.length > 0 };
}
