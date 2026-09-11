/**
 * Which settings panels exist, and which of them a member is offered.
 *
 * Its own module, away from the page that renders it, for one reason: the page
 * imports every tab component in the product, so a test of *this* rule through
 * the page would boot fifteen panels and their queries to read a list of
 * strings. Here it is a pure function with a test beside it, which is what the
 * rule needed — it was a comment and an inline conditional, and a tab was added
 * on the wrong side of the conditional anyway (Procedures, see below).
 *
 * **The rule.** A panel whose *GET* is admin-only is hidden. `AdminGate`
 * disables the controls inside a panel; it cannot make the list load. So a
 * member offered such a tab gets an empty state describing an empty workspace
 * rather than a permission they lack — "No procedures yet", of a workspace with
 * twenty. Everything else stays visible and renders read-only, which is the
 * opposite mistake avoided: a disabled control tells a member who to ask, a
 * missing tab tells them the feature does not exist.
 *
 * The authority for "admin-only GET" is `cmd/api/policy.go`, not this file.
 */
export interface SettingsTab {
  id: string;
  label: string;
}

export function settingsTabs(isAdmin: boolean): SettingsTab[] {
  return [
    { id: "general", label: "General" },
    // Databases and MCP servers are both "a place an agent reads from", so they
    // are one tab with the kind picked inside rather than two siblings that ask
    // an admin to know which one they want before they can look.
    { id: "data-sources", label: "Data sources" },
    { id: "metrics", label: "Metrics" },
    { id: "agents", label: "Agents" },
    // Beside Agents rather than under it: a procedure belongs to the workspace
    // and every agent is offered it unless an admin narrows one, so filing it
    // inside the agent form would put a workspace-level thing behind whichever
    // agent somebody happened to open.
    //
    // Admin-only on every route *including the read* — a procedure is text the
    // agents follow as an instruction — so it is hidden from a member like
    // Reports and API keys, rather than rendered read-only. It was offered to
    // members until 2026-09-11, and `GET /api/skills` answering 403 meant they
    // were shown this screen's empty state instead of its list.
    ...(isAdmin ? [{ id: "skills", label: "Procedures" }] : []),
    { id: "phones", label: "Phone numbers" },
    { id: "integrations", label: "Integrations" },
    ...(isAdmin ? [{ id: "reports", label: "Reports" }] : []),
    // Visible to members, unlike Reports beside it: the GET is member-readable
    // on purpose, because somebody composing a post has to see what they can
    // ask for by name. The writes inside are disabled rather than hidden.
    { id: "images", label: "Images" },
    // Admin-only on every route including the read, like MCP servers: the list
    // is a map of where this workspace's events go.
    ...(isAdmin ? [{ id: "webhooks", label: "Webhooks" }] : []),
    ...(isAdmin ? [{ id: "api-keys", label: "API keys" }] : []),
    // Admin-only on every route including the read, like API keys and for one
    // step more reason: an embed key decides which websites may tell us who a
    // person is.
    ...(isAdmin ? [{ id: "embed", label: "Embed" }] : []),
    ...(isAdmin ? [{ id: "team", label: "Team" }] : []),
    { id: "about", label: "About" },
  ];
}
