import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { ActionInvocation } from "@argentum/api-types";

/** The query key the chat stream invalidates when an `action_proposed` event
 *  arrives, so a proposal shows in the approvals strip without a refresh (T-11). */
export const PENDING_ACTIONS_KEY = ["actions", "pending"] as const;

interface ActionsResponse {
  actions: (ActionInvocation | undefined)[];
}
interface ActionResponse {
  action: ActionInvocation;
}

/** usePendingActions is the company's proposals awaiting a decision. Polled
 *  lightly as a backstop, but the live path is invalidation from the WS event —
 *  a proposal should appear the moment the agent raises it, not on the next poll. */
export function usePendingActions() {
  const { data, isLoading } = useQuery({
    queryKey: PENDING_ACTIONS_KEY,
    queryFn: async () => (await api.get<ActionsResponse>("/actions/pending")).data,
    refetchInterval: 60_000,
  });
  const pending = useMemo(
    () => (data?.actions ?? []).filter((a): a is ActionInvocation => !!a),
    [data],
  );
  return { pending, isLoading };
}

/** useDecideAction approves or rejects a proposal. On success it refreshes the
 *  pending list and the thread's messages, so the card reflects the outcome and
 *  the badge count drops in the same tick. */
export function useDecideAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, decision }: { id: string; decision: "approve" | "reject" }) =>
      (await api.post<ActionResponse>(`/actions/${id}/${decision}`)).data.action,
    onSuccess: (action) => {
      qc.invalidateQueries({ queryKey: PENDING_ACTIONS_KEY });
      if (action.thread_id) {
        qc.invalidateQueries({ queryKey: ["messages", action.thread_id] });
      }
    },
  });
}
