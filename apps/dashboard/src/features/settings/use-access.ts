import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  AccessMode,
  AccessModeChange,
  ResourceAccessView,
  ResourceKind,
} from "@argentum/api-types";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useToast } from "@/hooks/use-toast";

/**
 * Every resource of a kind with its mode and grants, and the three changes an
 * admin makes to them (T-Z7).
 *
 * **One query key per kind, and both halves of Settings → Team read it.** The
 * per-resource card and the per-person panel are two renders of this answer, so
 * a grant made from either is on the other the moment the refetch lands — no
 * reload, and no second request that could be read a grant apart from the first.
 */
export function useResourceAccess(kind: ResourceKind) {
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data, isLoading, isError } = useQuery({
    queryKey: ["access", kind],
    queryFn: async () =>
      (await api.get<{ resources: ResourceAccessView[] }>(`/access/${kind}`)).data.resources ?? [],
  });

  const byId = useMemo(
    () => new Map((data ?? []).map((v) => [v.resource_id, v] as const)),
    [data],
  );

  /** What else a change makes stale. For agents that is two things outside this
   *  screen: the roster's `reachable_agent_ids`, which is how an admin who grants
   *  themselves is offered the agent in chat (T-Z4), and the conversation list,
   *  which hides a conversation from anyone not granted every agent in it
   *  (T-Z10). For dashboards it is the list and whatever is open (T-Z5): an admin
   *  who grants themselves Payroll should find it on the dashboards page without
   *  a reload, and one who restricts it should stop seeing it there. */
  function settle() {
    qc.invalidateQueries({ queryKey: ["access", kind] });
    if (kind === "agent") {
      qc.invalidateQueries({ queryKey: ["agents"] });
      qc.invalidateQueries({ queryKey: ["threads"] });
      // A binding's row says whether its agent is restricted and it is
      // silenced (T-Z8); flipping the agent changes that answer.
      qc.invalidateQueries({ queryKey: ["agent-bindings"] });
    }
    // T-Z6: a member's source list, and Knowledge's list, pages and tables.
    if (kind === "connection") {
      qc.invalidateQueries({ queryKey: ["connections"] });
    }
    if (kind === "document") {
      qc.invalidateQueries({ queryKey: ["knowledge-documents"] });
      qc.invalidateQueries({ queryKey: ["knowledge-document"] });
      qc.invalidateQueries({ queryKey: ["knowledge-tables"] });
    }
    if (kind === "dashboard") {
      qc.invalidateQueries({ queryKey: ["dashboards"] });
      qc.invalidateQueries({ queryKey: ["dashboard-data"] });
      qc.invalidateQueries({ queryKey: ["dashboard-shares"] });
    }
  }

  const failed = (title: string) => (e: unknown) =>
    toast({ title, description: apiErrorMessage(e), variant: "destructive" });

  const setMode = useMutation({
    mutationFn: async ({ id, mode }: { id: string; mode: AccessMode }) =>
      (await api.put<AccessModeChange>(`/access/${kind}/${id}/mode`, { access_mode: mode })).data,
    onSuccess: (change) => {
      settle();
      // The warning said how many live links there were when it opened; this is
      // how many the server actually took back, which a link minted in between
      // would make different.
      const revoked = change?.revoked_shares ?? 0;
      if (revoked > 0) {
        toast({
          title: "Restricted",
          description:
            revoked === 1 ? "1 live share link was revoked." : `${revoked} live share links were revoked.`,
        });
      }
    },
    onError: failed("Access unchanged"),
  });

  const grant = useMutation({
    mutationFn: async ({ id, userId }: { id: string; userId: string }) =>
      api.put(`/access/${kind}/${id}/grants/${userId}`),
    onSuccess: settle,
    onError: failed("Nothing granted"),
  });

  const revoke = useMutation({
    mutationFn: async ({ id, userId }: { id: string; userId: string }) =>
      api.delete(`/access/${kind}/${id}/grants/${userId}`),
    onSuccess: settle,
    onError: failed("Nothing revoked"),
  });

  return {
    /** Every resource of the kind, in the API's order — with its name, which is
     *  how Settings lists dashboards an admin may not open themselves (T-Z5). */
    resources: data ?? [],
    byId,
    isLoading,
    isError,
    setMode,
    grant,
    revoke,
    busy: setMode.isPending || grant.isPending || revoke.isPending,
  };
}

export type ResourceAccess = ReturnType<typeof useResourceAccess>;
