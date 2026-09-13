import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Capability, CapabilityGrant } from "@argentum/api-types";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useToast } from "@/hooks/use-toast";
import {
  CAPABILITY_COPY,
  KIND_COPY,
  accessFor,
  accessSentence,
  type AccessResource,
  type EnforcedKind,
  type Person,
} from "./access";
import { useResourceAccess } from "./use-access";

/**
 * One person's access, as an admin reads it (T-Z7): their role, what they may
 * do, which agents they may talk to, which dashboards they may open, and — since
 * T-Z6 — which data sources they may see and which documents they may read.
 *
 * **Disabled or hidden — the difference, written down where it is decided.**
 * Two rules meet in this feature and they point opposite ways, on purpose:
 *
 * - A **capability** a person lacks is a control they are shown *disabled*, with
 *   a sentence saying who to ask — the 2026-08-04 decision
 *   (`docs/coverage/watchers-ui.md`). Voice is a thing the product does; a
 *   member who cannot use it should learn that it exists and how to get it.
 * - A **resource** a person lacks is *hidden* from them (T-Z4). A picker is a
 *   list of things you can do, and an agent the product will not let them talk
 *   to does not belong in it — nor, since T-Z10, do the conversations it was in,
 *   nor, since T-Z5, a dashboard on the dashboards list.
 *
 * This panel is the admin's, so it shows both — every capability as a toggle,
 * every resource as a checkbox — and it is the one screen where a restricted
 * resource sits beside a person who cannot reach it.
 *
 * Each resource half is drawn from the same `GET /api/access/:kind` answer as
 * `ResourceAccessCard`, never from `GET /api/users/:id/grants`: two requests
 * could be read a grant apart, and the two views would then disagree about the
 * one person they both show.
 */
export function PersonAccessPanel({
  person,
  agents,
  dashboards,
  connections,
  documents,
  meId,
}: {
  person: Person;
  agents: AccessResource[];
  dashboards: AccessResource[];
  connections: AccessResource[];
  documents: AccessResource[];
  meId?: string;
}) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const isMe = person.id === meId;
  const they = isMe ? "you" : "they";

  const caps = useQuery({
    queryKey: ["capabilities", person.id],
    queryFn: async () =>
      (await api.get<{ capabilities: CapabilityGrant[] }>(`/users/${person.id}/capabilities`)).data
        .capabilities ?? [],
  });
  const held = new Set((caps.data ?? []).map((g) => g.capability));

  const toggleCap = useMutation({
    mutationFn: async ({ cap, on }: { cap: Capability; on: boolean }) =>
      on
        ? api.put(`/users/${person.id}/capabilities/${cap}`)
        : api.delete(`/users/${person.id}/capabilities/${cap}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["capabilities", person.id] }),
    onError: (e: unknown) =>
      toast({ title: "Capability unchanged", description: apiErrorMessage(e), variant: "destructive" }),
  });

  return (
    <div className="mt-3 space-y-4 rounded-md border border-border bg-muted/30 p-3.5">
      <section className="space-y-1">
        <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Role</h4>
        <p className="text-sm">
          {person.role === "admin" ? "Admin" : "Member"}
          <span className="text-muted-foreground">
            {person.role === "admin"
              ? " — manages the workspace and who may reach what. A rank is not a grant: a restricted agent, dashboard or document is closed to an admin who is not granted it, like anyone else, and so are a restricted source's tests."
              : " — chats with the agents they may use, and reads the dashboards, documents and sources they may open, and usage."}
          </span>
        </p>
      </section>

      <section className="space-y-2">
        <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          What {they} may do
        </h4>
        {caps.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
        {caps.isError && (
          <p className="text-sm text-destructive">Could not load what {they} may do.</p>
        )}
        {!caps.isLoading &&
          !caps.isError &&
          (Object.keys(CAPABILITY_COPY) as Capability[]).map((cap) => {
            const copy = CAPABILITY_COPY[cap];
            const on = held.has(cap);
            return (
              <label key={cap} className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={on}
                  disabled={toggleCap.isPending}
                  onChange={() => toggleCap.mutate({ cap, on: !on })}
                  aria-label={`${copy.label} for ${person.email}`}
                />
                <span>
                  {copy.label}
                  <span className="block text-xs text-muted-foreground">
                    {copy.what} {copy.today}
                  </span>
                </span>
              </label>
            );
          })}
      </section>

      <ResourceChecklist kind="agent" resources={agents} person={person} isMe={isMe} />
      <ResourceChecklist kind="dashboard" resources={dashboards} person={person} isMe={isMe} />
      <ResourceChecklist kind="connection" resources={connections} person={person} isMe={isMe} />
      <ResourceChecklist kind="document" resources={documents} person={person} isMe={isMe} />
    </div>
  );
}

/** One kind's checkboxes for one person, from that kind's one read. */
function ResourceChecklist({
  kind,
  resources,
  person,
  isMe,
}: {
  kind: EnforcedKind;
  resources: AccessResource[];
  person: Person;
  isMe: boolean;
}) {
  const access = useResourceAccess(kind);
  const copy = KIND_COPY[kind];
  const rows = accessFor(person.id, resources, access.byId);

  return (
    <section className="space-y-2">
      <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {copy.panelTitle(isMe ? "you" : "they")}
      </h4>
      {access.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {access.isError && <p className="text-sm text-destructive">{copy.loadError}</p>}
      {!access.isLoading && !access.isError && rows.length === 0 && (
        <p className="text-sm text-muted-foreground">{copy.empty}</p>
      )}
      {!access.isLoading &&
        !access.isError &&
        rows.map((r) => (
          <label key={r.id} className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={r.granted}
              disabled={access.busy}
              onChange={() =>
                r.granted
                  ? access.revoke.mutate({ id: r.id, userId: person.id })
                  : access.grant.mutate({ id: r.id, userId: person.id })
              }
              aria-label={`${r.name} granted to ${person.email}`}
            />
            <span>
              {r.name}
              {r.mode === "restricted" && (
                <Badge variant="secondary" className="ml-2 font-normal">
                  Restricted
                </Badge>
              )}
              <span
                className={
                  r.reaches
                    ? "block text-xs text-muted-foreground"
                    : "block text-xs font-medium text-amber-700 dark:text-amber-400"
                }
              >
                {accessSentence(r, isMe, kind)}
              </span>
            </span>
          </label>
        ))}
    </section>
  );
}
