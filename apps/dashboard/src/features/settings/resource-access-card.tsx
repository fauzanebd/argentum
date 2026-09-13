import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Lock, X } from "lucide-react";
import type { AgentBindingsResponse } from "@argentum/api-types";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { api } from "@/lib/api";
import {
  DECISION_4,
  KIND_COPY,
  bindingSilenceNotice,
  grantedIds,
  liveShareCount,
  losesAccessOnRestrict,
  reaches,
  restrictWarning,
  shareRevocationNotice,
  silencedBindings,
  type AccessResource,
  type EnforcedKind,
  type Person,
  type ShareLink,
} from "./access";
import { useResourceAccess, type ResourceAccess } from "./use-access";

/**
 * Who can reach which agent or dashboard — the resource side of the access
 * matrix (T-Z7, and T-Z5 for dashboards). It was `AgentAccessCard` until the
 * second kind arrived; the rows, the warning and the grant controls are the same
 * for both, and a copy of 250 lines per kind is how one of them stops saying
 * "you included".
 *
 * The other side is `PersonAccessPanel`, one person at a time, and both render
 * `useResourceAccess(kind)`. An admin asks this question both ways — *who can
 * reach Payroll?* and *what can Rina reach?* — and a screen that answers only one
 * of them makes the other a spreadsheet exercise.
 *
 * Only the kinds in `ENFORCED_KINDS`: a source or a document gets no switch until
 * the ticket that makes restricting one refuse something.
 */

/** What each card says about the boundary it sets, in the words the roadmap
 *  fixed. Stated on the screen that sets it, because an admin cannot reason about
 *  a boundary whose edges are in a roadmap. */
const DESCRIPTION: Record<EnforcedKind, string> = {
  // What a grant does in the dashboard. What a restriction does on the doors
  // with no person is DESCRIPTION_AFTER's, below decision 4.
  agent:
    "Every agent is open to everyone in the workspace until you restrict it. A restricted agent is offered in chat only to the people granted it, and a conversation it was in — with any document that conversation produced and any action proposed there — is hidden from everyone else, admins included. Public links to those documents do not open, and none can be made, while it is restricted.",
  // The sentence after DECISION_4 is where T-Z12 stops: update_dashboard asks for
  // the person on a dashboard turn, and a turn with no person — T-Z8's doors —
  // asks nothing (access-grants §15).
  dashboard:
    "Every dashboard is open to everyone in the workspace until you restrict it. A restricted dashboard is missing from the dashboards list of anyone not granted it and will not open for them, admins included. It cannot be shared, and restricting it revokes its live share links.",
  // T-Z6. What a source's restriction is, and — as loudly — what it is not.
  connection:
    "Every data source is open to everyone in the workspace until you restrict it. A restricted source is missing from the source list of any member not granted it, and its freshness test, description rebuild and retrieval test refuse anyone not granted it, admins included. Admins still see every source in Settings → Data sources, where sources are configured.",
  document:
    "Every uploaded document is open to everyone in the workspace until you restrict it. A restricted document is missing from Knowledge for anyone not granted it and its pages and tables will not open for them, admins included — and an agent searching documents for them finds nothing in it.",
};

/** Roadmap 12's cut order said Settings must state, *"in those words"*, that
 *  grants bound the dashboard and nothing else, for as long as T-Z8 was unbuilt.
 *  T-Z8 decided each other door, so the agent's sentence now says which rule each
 *  one follows — decisions 8 to 11, in the order an admin meets them. */
const DESCRIPTION_AFTER: Record<EnforcedKind, string> = {
  agent:
    "Beyond this dashboard: a channel answers as a restricted agent only where you have acknowledged that anyone who can post there can use it; the website widget never reaches one; an API key reaches one unless the key lists its agents and this is not among them; and a watcher or a schedule switches itself off, saying why, when the person who made it loses access.",
  dashboard:
    "An agent asked to change a dashboard checks this too, in this dashboard only: through a channel, an API key or the website widget, an agent can still change a restricted one.",
  // Roadmap 12's T-Z6: "conflating them would make a user grant look like a data
  // boundary it is not". The sentence that stops an admin believing it is one.
  connection:
    "A grant decides what a person is shown, never what an agent may query: an agent reaches exactly the sources in its own list in Settings → Agents, restricted or not.",
  document:
    "A turn with no person — a channel, an API key, the website widget, a watcher — still searches every document. A table already published from it is a data source's rows, and agents with that source still query them.",
};

export function ResourceAccessCard({
  kind,
  resources,
  people,
  meId,
}: {
  kind: EnforcedKind;
  resources: AccessResource[];
  people: Person[];
  meId?: string;
}) {
  const access = useResourceAccess(kind);
  const copy = KIND_COPY[kind];

  return (
    <Card>
      <CardHeader>
        <CardTitle>{copy.cardTitle}</CardTitle>
        <CardDescription>
          {DESCRIPTION[kind]} {DECISION_4} {DESCRIPTION_AFTER[kind]}
        </CardDescription>
      </CardHeader>
      <CardContent className="divide-y divide-border/50">
        {access.isLoading && <div className="text-sm text-muted-foreground py-4">Loading…</div>}
        {access.isError && (
          <div className="text-sm text-destructive py-4">
            {copy.loadError} Nothing here can be changed until it loads.
          </div>
        )}
        {!access.isLoading && !access.isError && resources.length === 0 && (
          <div className="text-sm text-muted-foreground py-4">{copy.empty}</div>
        )}
        {!access.isLoading &&
          !access.isError &&
          resources.map((r) => (
            <ResourceAccessRow
              key={r.id}
              kind={kind}
              resource={r}
              people={people}
              meId={meId}
              access={access}
            />
          ))}
      </CardContent>
    </Card>
  );
}

function ResourceAccessRow({
  kind,
  resource,
  people,
  meId,
  access,
}: {
  kind: EnforcedKind;
  resource: AccessResource;
  people: Person[];
  meId?: string;
  access: ResourceAccess;
}) {
  // The flip to restricted is confirmed inline rather than through
  // window.confirm: the warning names people, and a native dialog can neither
  // lay out a list nor be photographed for the record.
  const [confirming, setConfirming] = useState(false);
  const copy = KIND_COPY[kind];
  const { id, name } = resource;
  const view = access.byId.get(id);
  const restricted = view?.access_mode === "restricted";
  const granted = grantedIds(view);
  const holders = people.filter((p) => granted.has(p.id));
  // A removed person cannot sign in, so granting them is a row that opens
  // nothing; a pending invitee can, once they accept, so they are offered.
  const candidates = people.filter((p) => p.status !== "deactivated" && !granted.has(p.id));
  const losing = losesAccessOnRestrict(view, people);
  // Decision 4 is invisible unless the screen shows it: the admin reading this
  // card is refused a restricted resource they are not granted, like anyone else.
  const meRefused = !!meId && !reaches(view, meId);

  function restrict() {
    access.setMode.mutate({ id, mode: "restricted" }, { onSettled: () => setConfirming(false) });
  }

  return (
    <div className="space-y-3 py-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            <span className="truncate">{name}</span>
            {restricted ? (
              <Badge variant="secondary" className="gap-1">
                <Lock className="h-3 w-3" />
                Restricted
              </Badge>
            ) : (
              <Badge variant="outline">Open</Badge>
            )}
            {resource.disabled && <Badge variant="outline">Disabled</Badge>}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {restricted
              ? holders.length === 1
                ? `Only the 1 person granted it can ${copy.reach}.`
                : `Only the ${holders.length} people granted it can ${copy.reach}.`
              : `Everyone in the workspace can ${copy.reach}.`}
          </p>
          {meRefused && (
            <p className="mt-1 text-xs font-medium text-amber-700 dark:text-amber-400">
              You are not granted {name}, so you cannot {copy.refused}. Grant yourself below if you
              need to.
            </p>
          )}
        </div>
        {restricted ? (
          <Button
            variant="outline"
            size="sm"
            disabled={access.busy}
            onClick={() => access.setMode.mutate({ id, mode: "open" })}
          >
            Open to everyone
          </Button>
        ) : (
          <Button
            variant="outline"
            size="sm"
            disabled={access.busy || confirming}
            onClick={() => setConfirming(true)}
          >
            Restrict {name}…
          </Button>
        )}
      </div>

      {confirming && (
        <div
          role="alertdialog"
          aria-label={`Restrict ${name}`}
          className="space-y-2 rounded-md border border-amber-300 bg-amber-50 p-3.5 dark:border-amber-900 dark:bg-amber-950/40"
        >
          <p className="text-sm font-medium">{restrictWarning(name, view, people, meId, kind)}</p>
          {losing.length > 0 && granted.size > 0 && (
            <p className="text-xs text-muted-foreground">
              {losing.map((p) => (p.id === meId ? `${p.email} (you)` : p.email)).join(", ")}
            </p>
          )}
          {kind === "dashboard" && <ShareRevocationNotice dashboardId={id} />}
          {kind === "agent" && <BindingSilenceNotice agentId={id} />}
          <p className="text-xs text-muted-foreground">
            Opening it again restores everyone, and keeps every grant you made. {DECISION_4}
          </p>
          <div className="flex gap-2 pt-1">
            <Button size="sm" disabled={access.busy} onClick={restrict}>
              {access.setMode.isPending ? "Restricting…" : `Restrict ${name}`}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted-foreground">
          {holders.length === 0
            ? "Nobody is granted it."
            : restricted
              ? "Granted:"
              : "Granted, for when it is restricted:"}
        </span>
        {holders.map((p) => (
          <span
            key={p.id}
            className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 py-0.5 pl-2.5 pr-1 text-xs"
          >
            {p.email}
            {p.id === meId && <span className="text-muted-foreground">(you)</span>}
            <button
              type="button"
              className="rounded-full p-0.5 hover:bg-accent disabled:opacity-50"
              aria-label={`Revoke ${name} from ${p.email}`}
              disabled={access.busy}
              onClick={() => access.revoke.mutate({ id, userId: p.id })}
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
        {candidates.length > 0 && (
          <Select
            value=""
            disabled={access.busy}
            onValueChange={(userId) => access.grant.mutate({ id, userId })}
          >
            <SelectTrigger className="h-7 w-48 text-xs" aria-label={`Grant ${name} to someone`}>
              <SelectValue placeholder="Grant to…" />
            </SelectTrigger>
            <SelectContent>
              {candidates.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.id === meId ? `${p.email} (you)` : p.email}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>
    </div>
  );
}

/**
 * How many live links restricting this dashboard will revoke — T-Z5's *"says so
 * in the confirmation"*. Read when the confirmation opens rather than for every
 * row on the card: it is one request an admin asked for by pressing *Restrict*,
 * not one per dashboard on every visit to Settings.
 */
function ShareRevocationNotice({ dashboardId }: { dashboardId: string }) {
  const shares = useQuery({
    queryKey: ["dashboard-shares", dashboardId],
    queryFn: async () =>
      (await api.get<{ shares: ShareLink[] }>(`/dashboards/${dashboardId}/shares`)).data.shares ??
      [],
  });
  const live = shares.data ? liveShareCount(shares.data, new Date()) : undefined;
  return <p className="text-sm font-medium">{shareRevocationNotice(live, shares.isError)}</p>;
}

/**
 * How many channels restricting this agent will silence (T-Z8) — the agent's
 * version of the link count above, read when the confirmation opens for the same
 * reason. The query key is Settings → Agents' own, so the two screens cannot
 * count different bindings.
 */
function BindingSilenceNotice({ agentId }: { agentId: string }) {
  const bindings = useQuery({
    queryKey: ["agent-bindings"],
    queryFn: async () => (await api.get<AgentBindingsResponse>("/agent-bindings")).data,
  });
  const silenced = bindings.data
    ? silencedBindings(bindings.data.bindings ?? [], agentId).length
    : undefined;
  return <p className="text-sm font-medium">{bindingSilenceNotice(silenced, bindings.isError)}</p>;
}
