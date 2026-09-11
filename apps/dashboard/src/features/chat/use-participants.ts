import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { ThreadParticipant } from "@argentum/api-types";

export const participantsKey = (threadId: string) => ["participants", threadId];

/**
 * Who is in this conversation (T-N4, over T-N2's routes).
 *
 * The list includes the **default speaker**, which has no participant row —
 * `ThreadParticipantService.List` returns the union, and the reason is in
 * migration 078: a membership table written by one of six thread-creating paths
 * is a table five paths forget. The browser therefore never has to know that
 * distinction exists.
 *
 * Fetched per thread and not folded into the thread detail query, because the
 * two invalidate on different events: adding an agent changes this and nothing
 * about the thread, and a new message changes the thread and nothing about
 * this.
 */
export function useParticipants(threadId: string | null) {
  const qc = useQueryClient();

  const { data, isLoading } = useQuery({
    queryKey: participantsKey(threadId ?? ""),
    enabled: !!threadId,
    queryFn: async () =>
      (
        await api.get<{ participants: ThreadParticipant[] | null }>(
          `/threads/${threadId}/participants`,
        )
      ).data.participants ?? [],
    // A room changes when somebody edits it, which is rarely and always from
    // this screen — so the mutations below invalidate it and nothing else has
    // to poll.
    staleTime: 5 * 60 * 1000,
  });

  const participants = useMemo(
    () => (data ?? []).filter((p): p is ThreadParticipant => !!p),
    [data],
  );

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: participantsKey(threadId ?? "") });
  };

  const add = useMutation({
    mutationFn: async (agentID: string) =>
      (await api.post(`/threads/${threadId}/participants`, { agent_id: agentID })).data,
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (agentID: string) =>
      (await api.delete(`/threads/${threadId}/participants/${agentID}`)).data,
    onSuccess: invalidate,
  });

  return useMemo(
    () => ({
      isLoading,
      participants,
      /** A room is more than one agent. One is an ordinary conversation, and
       *  every surface that renders differently for a room checks this rather
       *  than counting the array itself. */
      isRoom: participants.length > 1,
      ids: new Set(participants.map((p) => p.agent_id)),
      add,
      remove,
    }),
    [isLoading, participants, add, remove],
  );
}
