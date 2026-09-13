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
    // The gallery rides on the same payload (T-B3), so the starter questions
    // for a picked agent cost no second request. Keyed by template rather than
    // by agent: `template_key` is the only thing a saved agent keeps of the
    // card it came from, and it is provenance — nothing else here reads it.
    const starterQuestions = new Map<string, string[]>();
    for (const t of data?.templates ?? []) {
      if (t?.key) starterQuestions.set(t.key, t.starter_questions ?? []);
    }
    // Which agents this person may talk to (T-Z4). A member is only ever sent
    // those; an admin is sent the whole roster — Settings → Agents manages it
    // from this same query — and told which of it they may use. An API that
    // predates grants sends no list, which means every agent, as it did.
    const reachableIds = data?.reachable_agent_ids
      ? new Set(data.reachable_agent_ids)
      : null;
    const usable = agents.filter((a) => !reachableIds || reachableIds.has(a.id));
    const companyDefault = agents.find((a) => a.is_default) ?? null;
    return {
      isLoading,
      /** Every agent sent, including disabled and restricted ones — for naming
       *  what a thread runs as, which is not an offer to use it. */
      byId,
      /** What a new conversation may be opened on. */
      selectable: usable.filter((a) => a.enabled),
      /** What may be added to a room: every agent this person may talk to,
       *  disabled ones included so the menu can say why they are greyed. */
      addable: usable,
      /**
       * The agent a new conversation runs as when the user picks nothing: the
       * company default, or — when that is restricted from this person — the
       * first enabled agent they may use, which is the same fall-through the
       * backend applies (ChatEnqueuer.openingAgent). Mirrored rather than
       * fetched so the caption above the composer names the agent that will
       * actually answer.
       */
      fallback:
        companyDefault && usable.includes(companyDefault)
          ? companyDefault
          : (usable.find((a) => a.enabled) ?? null),
      /**
       * What to offer on an empty thread opened on this agent. Empty for an
       * agent created from blank, for one created before templates existed, and
       * on a deployment with no gallery — all three are the same screen as
       * before, which is why this returns a list and not a placeholder.
       */
      starterQuestionsFor(agent: Agent | null | undefined): string[] {
        if (!agent?.template_key) return [];
        return starterQuestions.get(agent.template_key) ?? [];
      },
    };
  }, [agents, data, isLoading]);
}
