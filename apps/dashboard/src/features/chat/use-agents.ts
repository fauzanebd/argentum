import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Agent, AgentsResponse } from "@argentum/api-types";

/**
 * The company's roster, as the chat needs it (T-S3).
 *
 * Same query key as Settings → Agents, so opening the chat after editing the
 * roster does not refetch and the two views cannot show different names.
 *
 * `selectable` is the picker's list and `byId` is the label lookup, and they
 * are deliberately not the same set: a disabled agent must not be offered, but
 * a thread already bound to one still has to render its name. Dropping it from
 * both would leave an old conversation captioned "Default agent" while it is
 * demonstrably still running as Finance.
 */
export function useAgents() {
  const { data, isLoading } = useQuery({
    queryKey: ["agents"],
    queryFn: async () => (await api.get<AgentsResponse>("/agents")).data,
    // The roster changes when an admin edits it, which is rarely and never
    // from this screen. Refetching on every chat mount buys nothing.
    staleTime: 5 * 60 * 1000,
  });

  // `[]*Agent` generates as `(Agent | undefined)[]` (T-02b), so the nulls the
  // Go slice could technically hold are dropped once, here, rather than
  // guarded at every use below.
  const agents = useMemo(
    () => (data?.agents ?? []).filter((a): a is Agent => !!a),
    [data],
  );

  return useMemo(() => {
    const byId = new Map<string, Agent>();
    for (const a of agents) byId.set(a.id, a);
    return {
      isLoading,
      /** Every agent, including disabled ones — for naming what a thread runs as. */
      byId,
      /** What a new conversation may be opened on. */
      selectable: agents.filter((a) => a.enabled),
      /** The agent a new conversation runs as when the user picks nothing. */
      fallback: agents.find((a) => a.is_default) ?? null,
    };
  }, [agents, isLoading]);
}
