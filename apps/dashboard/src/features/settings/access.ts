import type {
  AccessMode,
  AgentChannelBinding,
  Capability,
  DisabledReason,
  ResourceAccessView,
  ResourceKind,
} from "@argentum/api-types";

/**
 * The rules Settings → Team draws its access matrix by (T-Z7), kept apart from
 * the components so each one is a pure function with a test beside it.
 *
 * Nothing here decides anything. `internal/authz` does, on every request; these
 * functions only have to agree with it about what an admin is shown, and the one
 * place they could quietly disagree — whether a role is an input — is pinned by
 * `access.test.ts`.
 */

/** A person as `GET /api/users` sends one. No Go struct generates this shape
 *  (the handler answers a TeamService row inside `gin.H`), so it is declared
 *  here with only the fields the matrix reads; `team-tab.tsx`'s `Member`
 *  satisfies it. */
export interface Person {
  id: string;
  email: string;
  role: string;
  status: string;
}

/** A resource as the matrix lists it: an id, what it is called, and whether it
 *  is switched off — only an agent can be. */
export interface AccessResource {
  id: string;
  name: string;
  disabled?: boolean;
}

/** The kinds a restriction refuses something for — since T-Z6, every kind. */
export type EnforcedKind = ResourceKind;

/**
 * The kinds Settings offers an open/restricted switch for: the ones whose
 * restriction refuses something.
 *
 * `agent` is enforced — talking to one, at every dashboard seam (T-Z4), and
 * reading a conversation it was in (T-Z10). `dashboard` is enforced on opening
 * one, its data and its links, on the dashboards list, and on sharing it (T-Z5).
 * `connection` is enforced on a member's source list and on the three routes that
 * read what is in a source, and never on what an agent may query; `document` on
 * Knowledge's list, pages and tables, and on the passages `search_documents`
 * quotes for a person (T-Z6). Until each landed, the API stored a restriction on
 * it that nothing read, and this list was what kept a switch for it off the page.
 */
export const ENFORCED_KINDS: readonly EnforcedKind[] = ["agent", "dashboard", "connection", "document"];

/** Roadmap 12's decision 4, whose words are fixed: *"The dashboard says exactly
 *  this sentence; it does not say 'secure'."* */
export const DECISION_4 =
  "An admin can grant themselves, so this is a boundary against accident and casual browsing, not against a determined admin.";

/**
 * What reaching each kind means, in the words every sentence on this screen is
 * built from. `Record<EnforcedKind, …>` so T-Z6's kinds fail `tsc` here until
 * somebody writes what restricting a source or a document takes away.
 */
export const KIND_COPY: Record<
  EnforcedKind,
  {
    cardTitle: string;
    panelTitle: (they: string) => string;
    /** What a person granted it may do: "talk to it", "open it". */
    reach: string;
    /** What a person refused it loses, which for an agent is more than the reach. */
    refused: string;
    loadError: string;
    empty: string;
  }
> = {
  agent: {
    cardTitle: "Who can talk to which agent",
    panelTitle: (they) => `Agents ${they} may talk to`,
    reach: "talk to it",
    refused: "talk to it or open a conversation it was in",
    loadError: "Could not load who can reach each agent.",
    empty: "No agents yet.",
  },
  dashboard: {
    cardTitle: "Who can open which dashboard",
    panelTitle: (they) => `Dashboards ${they} may open`,
    reach: "open it",
    refused: "open it",
    loadError: "Could not load who can open each dashboard.",
    empty: "No dashboards yet.",
  },
  // "See", not "use": an agent's use of a source is its own source list, and a
  // grant never changes it. And "as a member", because an admin still sees every
  // source where sources are configured.
  connection: {
    cardTitle: "Who can see which data source",
    panelTitle: (they) => `Data sources ${they} may see`,
    reach: "see it",
    refused: "see it as a member, or run its tests",
    loadError: "Could not load who can see each data source.",
    empty: "No data sources yet.",
  },
  document: {
    cardTitle: "Who can read which document",
    panelTitle: (they) => `Documents ${they} may read`,
    reach: "read it",
    refused: "read it, or find a passage from it through an agent",
    loadError: "Could not load who can read each document.",
    empty: "No documents uploaded yet.",
  },
};

/**
 * What each capability is, and — the half that matters on this screen — what
 * granting it does **today**.
 *
 * `capabilityPolicy` in `cmd/api/policy.go` gates one route: voice's
 * transcription (T-W7). The other two ask for nothing yet, and that is
 * deliberate (gating an existing route without a backfill locks out everyone
 * who uses it). A toggle that did not say so would read as a switch that works.
 * Granting ahead is still the point of offering them (`domain.AllCapabilities`'
 * comment).
 *
 * Voice gates two routes and, since T-W9, two controls: the microphone and the
 * play button. Its `today` says what a grant changes on screen, and that both
 * depend on the workspace having voice set up — a grant does not make a missing
 * provider work, and an admin toggling it on a deployment without one should not
 * be told otherwise.
 *
 * `Record<Capability, …>` so a capability added to the Go vocabulary fails
 * `tsc` here until somebody writes what it does. Whichever ticket first gates
 * approving or exporting rewrites that one's `today`.
 */
export const CAPABILITY_COPY: Record<Capability, { label: string; what: string; today: string }> = {
  voice: {
    label: "Voice",
    what: "Speak a question and hear the answer.",
    today: "Where this workspace has voice set up: a microphone in the chat that puts what they say in the message box to check before sending, and a button that reads an answer aloud. Without the grant, the microphone tells them to ask an admin.",
  },
  approve_actions: {
    label: "Approve actions",
    what: "Decide an action an agent proposed.",
    today: "Nothing asks for this yet: every member can still approve and reject, as before.",
  },
  export_data: {
    label: "Export data",
    what: "Download the company's data out of Argentum.",
    today: "Nothing asks for this yet: exporting is still for admins, by role.",
  },
};

/** Everyone holding a grant on a resource. */
export function grantedIds(view: ResourceAccessView | undefined): Set<string> {
  return new Set((view?.grants ?? []).map((g) => g.user_id));
}

/**
 * Whether a person may reach a resource right now.
 *
 * **Role is not an input**, and that is decision 4 rather than an omission: an
 * admin who is not granted a restricted agent is refused it at every seam, so a
 * screen that showed them reaching it would be the one place the product claimed
 * otherwise. A resource the answer does not list — created after it was read —
 * is open, which is what 084's default makes every new one.
 */
export function reaches(view: ResourceAccessView | undefined, userId: string): boolean {
  if (!view || view.access_mode !== "restricted") return true;
  return grantedIds(view).has(userId);
}

/**
 * Who a flip to `restricted` takes access from: everyone who can sign in and
 * holds no grant — admins like anyone else, and the person pressing the button
 * included. A pending invitation has no access to lose yet and a removed person
 * has none left, so neither is counted; counting them would inflate the one
 * number this warning exists to get right.
 */
export function losesAccessOnRestrict(
  view: ResourceAccessView | undefined,
  people: Person[],
): Person[] {
  const granted = grantedIds(view);
  return people.filter((p) => p.status === "active" && !granted.has(p.id));
}

/**
 * The sentence the confirmation leads with (T-Z2's acceptance: *"the UI warns
 * before the flip, because this is the one transition that can lock a company
 * out"*). Three shapes, because "0 people lose access" and "nobody is granted"
 * are opposite situations that a count alone would word the same way.
 */
export function restrictWarning(
  name: string,
  view: ResourceAccessView | undefined,
  people: Person[],
  meId: string | undefined,
  kind: EnforcedKind = "agent",
): string {
  const copy = KIND_COPY[kind];
  if (grantedIds(view).size === 0) {
    return `Nobody is granted ${name}. Restricting it means nobody — you included — can ${copy.refused}, until you grant someone.`;
  }
  if (kind === "connection" && losesAccessOnRestrict(view, people).length > 0) {
    const losing = losesAccessOnRestrict(view, people);
    const who = losing.length === 1 ? "1 person" : `${losing.length} people`;
    const me = meId && losing.some((p) => p.id === meId) ? ", you included" : "";
    return `${who} will be refused ${name}${me}: a member will not see it in the source list, and nobody not granted it can run its tests. Agents that query it keep querying it — that is each agent's own source list, not this.`;
  }
  if (kind === "document" && losesAccessOnRestrict(view, people).length > 0) {
    const losing = losesAccessOnRestrict(view, people);
    const who = losing.length === 1 ? "1 person" : `${losing.length} people`;
    const me = meId && losing.some((p) => p.id === meId) ? ", you included" : "";
    return `${who} will lose access to ${name}${me}: it will be gone from Knowledge, its pages and tables will not open for them, and an agent searching documents for them will find nothing in it.`;
  }
  const losing = losesAccessOnRestrict(view, people);
  if (losing.length === 0) {
    return `Everyone who can sign in is granted ${name}, so nobody loses access.`;
  }
  const who = losing.length === 1 ? "1 person" : `${losing.length} people`;
  const me = meId && losing.some((p) => p.id === meId) ? ", you included" : "";
  if (kind === "dashboard") {
    return `${who} will lose access to ${name}${me}: it will be gone from their dashboards, and a link to it — in a chat reply or bookmarked — will not open for them.`;
  }
  return `${who} will lose access to ${name}${me}: they will not be offered it in chat, and conversations it was in — with the documents they produced and the actions proposed there — will be hidden from them. Public links to those documents stop opening until it is open again.`;
}

/** A dashboard share link as `GET /api/dashboards/:id/shares` sends one, with
 *  only the two fields the warning reads. Declared here rather than imported
 *  because `@argentum/api-types` has no usable `DashboardShare`: the generator
 *  emits that name as a union of the two refresh-limit constants declared under
 *  the struct, and the struct itself never arrives (access-grants §14c). */
export interface ShareLink {
  revoked_at?: string | null;
  expires_at: string;
}

/** Links that would still open today: not revoked, not expired. The server
 *  revokes exactly these on restrict (`closeOnRestrict` in
 *  `resource_grant_repo.go`), so this is the count the warning can promise. */
export function liveShareCount(shares: ShareLink[], now: Date): number {
  return shares.filter((s) => !s.revoked_at && new Date(s.expires_at) > now).length;
}

/**
 * The second line of a dashboard's restrict warning — T-Z5's *"restricting a
 * dashboard that already has a live share revokes the share and says so in the
 * confirmation"*. A link is a door with no person behind it, so a grant cannot
 * follow it; restricting takes every live one back, and re-opening does not
 * return them.
 */
export function shareRevocationNotice(live: number | undefined, failed: boolean): string {
  if (failed) {
    return "Could not check its share links. Restricting it still revokes every live one.";
  }
  if (live === undefined) return "Checking for live share links…";
  if (live === 0) {
    return "It has no live share links, and it cannot be shared while it is restricted.";
  }
  const links = live === 1 ? "Its 1 live share link" : `Its ${live} live share links`;
  return `${links} will be revoked, and it cannot be shared while it is restricted. Opening it again does not bring ${live === 1 ? "the link" : "them"} back.`;
}

/**
 * The channel bindings restricting an agent silences (T-Z8): every binding to it
 * that no admin has acknowledged.
 *
 * Roadmap 12's decision 8 makes a channel the grant — but only once somebody has
 * said so out loud. A binding made while the agent was open said nothing about
 * who may use it restricted, so from the flip it stops answering until an admin
 * acknowledges it on Settings → Agents. An acknowledged one keeps answering, and
 * is not counted: nothing about it changes.
 */
export function silencedBindings(
  bindings: (AgentChannelBinding | undefined)[],
  agentId: string,
): AgentChannelBinding[] {
  return bindings.filter(
    (b): b is AgentChannelBinding =>
      !!b && b.agent_id === agentId && !b.restricted_acknowledged_at,
  );
}

/**
 * The line an agent's restrict warning adds about its channels (T-Z8) — the
 * agent's version of `shareRevocationNotice`, and for its reason: the warning
 * exists to say everything the press will do before it is pressed.
 */
export function bindingSilenceNotice(silenced: number | undefined, failed: boolean): string {
  if (failed) {
    return "Could not check its channel bindings. Restricting it still stops every channel bound to it from answering until you acknowledge that channel.";
  }
  if (silenced === undefined) return "Checking its channel bindings…";
  if (silenced === 0) {
    return "No channel stops answering: none is bound to it without an acknowledgement.";
  }
  const channels = silenced === 1 ? "1 channel bound to it" : `${silenced} channels bound to it`;
  return `${channels} will stop answering until you acknowledge, on Settings → Agents, that anyone who can post there can use it.`;
}

/**
 * Why the product switched a watcher or a schedule off (T-Z8). `Record<…>` so a
 * reason added in Go fails `tsc` here until somebody words it. Shorter than the
 * sentence the run history carries, because this sits under a row's name.
 */
export const DISABLED_REASON_COPY: Record<DisabledReason, string> = {
  creator_not_granted:
    "Turned off: the agent it runs as is restricted, and whoever created it is not granted it. Grant them, then switch it back on.",
  creator_removed:
    "Turned off: the agent it runs as is restricted, and whoever created it is no longer in this workspace.",
};

/** One resource, from one person's side of the matrix. */
export interface PersonResourceRow {
  id: string;
  name: string;
  mode: AccessMode;
  granted: boolean;
  reaches: boolean;
}

/**
 * One person's side of the matrix, built from the **same** views the
 * per-resource cards read. T-Z7's acceptance is *"the resource-side view and the
 * user-side view cannot disagree — one query, two renders"*, and this function
 * is the second render: it takes no data the cards do not also hold.
 */
export function accessFor(
  userId: string,
  resources: { id: string; name: string }[],
  byId: Map<string, ResourceAccessView>,
): PersonResourceRow[] {
  return resources.map((r) => {
    const view = byId.get(r.id);
    return {
      id: r.id,
      name: r.name,
      mode: view?.access_mode ?? "open",
      granted: grantedIds(view).has(userId),
      reaches: reaches(view, userId),
    };
  });
}

/** What a row in a person's panel says under the resource's name. */
export function accessSentence(
  row: PersonResourceRow,
  isMe: boolean,
  kind: EnforcedKind = "agent",
): string {
  const who = isMe ? "You" : "They";
  const copy = KIND_COPY[kind];
  if (row.mode === "restricted") {
    return row.reaches
      ? `Restricted. ${who} can ${copy.reach}: granted.`
      : `Restricted. ${who} cannot ${copy.refused}.`;
  }
  return row.granted
    ? "Open to everyone. This grant takes effect if it is restricted."
    : "Open to everyone.";
}
