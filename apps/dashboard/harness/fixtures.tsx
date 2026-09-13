/**
 * The stand-ins the harness mounts the real components against.
 *
 * **Why stubs rather than the running product.** The arms this harness is here
 * to close are about *rendering*: does the counter go red, does the amber
 * notice appear, is the Procedures tab absent for a member. Reaching those
 * through the real stack would need a production login, a tenant whose index
 * actually overflows, and — for the chat chip — a paid model turn that happens
 * to open a procedure. None of that makes the pixels more true, and two of the
 * three are things this deployment should not be asked to produce on demand.
 *
 * What it therefore does **not** prove is wiring: that `GET /api/skills` really
 * returns this shape, that the member really gets a 403. Those are proven on
 * the wire already (`docs/coverage/skills.md` §6a) and in `cmd/api/policy.go`.
 * The two halves meet in the middle; neither covers the other, and saying so is
 * the point of this comment.
 */
import type { ReactNode } from "react";
import { FORM_NAME, FORM_TRIGGER, FORM_BODY } from "./constants";

/** Whether the harness is currently pretending to be an admin. Read by the
 *  `@/store/auth` stub, set from the scene table before mount. */
export let harnessIsAdmin = true;
export function setHarnessAdmin(v: boolean) {
  harnessIsAdmin = v;
}

const LONG_TRIGGER =
  "When someone asks for revenue broken down by branch for a period, or compares two periods of it.";

/** A workspace with three procedures, one of them switched off — the state the
 *  list, the badges and the per-agent checklist all have to render. */
export const SKILLS_OK = {
  skills: [
    {
      id: "sk-1",
      company_id: "co-1",
      name: "Weekly revenue by branch",
      when_to_use: LONG_TRIGGER,
      body: "1. …",
      enabled: true,
      source: "tenant",
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    },
    {
      id: "sk-2",
      company_id: "co-1",
      name: "How a month is closed",
      when_to_use: "When a question depends on whether a period is final.",
      body: "1. …",
      enabled: true,
      source: "tenant",
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    },
    {
      id: "sk-3",
      company_id: "co-1",
      name: "Retur dan potongan",
      when_to_use: "When a revenue figure is asked for without qualification.",
      body: "1. …",
      enabled: false,
      source: "tenant",
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    },
  ],
  limits: { name_chars: 60, when_to_use_chars: 200, body_chars: 8000, per_company: 200 },
  index: { lines: 4, chars: 1204, max_chars: 4000, max_lines: 20, dropped: [] as string[] },
};

/** The same workspace, over the character bound. `dropped` is the field the
 *  amber notice exists for, and the only place a tenant is ever told that a
 *  procedure they wrote is not reaching their agents. */
export const SKILLS_OVERFLOW = {
  ...SKILLS_OK,
  index: {
    lines: 13,
    chars: 3825,
    max_chars: 4000,
    max_lines: 20,
    // Only enabled procedures are ever in the index, so only an enabled one can
    // be dropped from it. Naming the switched-off row here would put a
    // screenshot in the docs implying otherwise.
    dropped: ["How a month is closed", "Stock opname mingguan", "Harga promo dan bundling"],
  },
};

/** What `POST /api/skills/preview` answers. Written by hand here, but copied
 *  from the shape the endpoint returns — including `refusal`, which is the
 *  sentence the *save* would have used, so the two never word the rule twice. */
export const PREVIEW_OVER_CAP = {
  index_line: `- ${FORM_NAME} — ${FORM_TRIGGER}`,
  index_line_chars: [...`- ${FORM_NAME} — ${FORM_TRIGGER}`].length,
  framed_body:
    `<<<WORKSPACE_PROCEDURE name="${FORM_NAME}">>>\n` + FORM_BODY + "\n<<<END_WORKSPACE_PROCEDURE>>>",
  // `domain/skill.go:139`, word for word. The preview shows the sentence the
  // *save* would refuse with, so the form and the API never word one rule
  // twice — a fixture that paraphrased it would be the drift that rule exists
  // to prevent, photographed and filed in the docs.
  refusal: `name is ${[...FORM_NAME].length} characters; the limit is 60, because it rides in every turn's prompt`,
};

export const THREADS = {
  threads: [
    { id: "th-1", title: "Revenue by branch, August vs July", is_archived: false },
    { id: "th-2", title: "Why is Bekasi missing from the report?", is_archived: false },
  ],
};

const JOINED = "2026-07-01T00:00:00Z";

function grant(userId: string, agentId: string, kind = "agent") {
  return { user_id: userId, resource_kind: kind, resource_id: agentId, granted_at: JOINED };
}

/**
 * Settings → Team's access matrix (T-Z7), in the state the screen exists for.
 *
 * HR is restricted and granted to two people, **neither of them the admin
 * looking at it**, so decision 4's consequence — the admin is refused too — is
 * on screen rather than described. Ops is open with a grant already made, which
 * is how an admin prepares a restriction without a window where nobody can reach
 * it. Legal is disabled. Sari's invitation is pending and Yoga was removed: the
 * restrict warning's count has exactly two people it must not include.
 */
export const TEAM = {
  users: [
    { id: "u-dewi", email: "dewi@tokomaju.id", role: "admin", status: "active", created_at: JOINED },
    { id: "u-agus", email: "agus@tokomaju.id", role: "admin", status: "active", created_at: JOINED },
    { id: "u-rina", email: "rina@tokomaju.id", role: "member", status: "active", created_at: JOINED },
    { id: "u-budi", email: "budi@tokomaju.id", role: "member", status: "active", created_at: JOINED },
    {
      id: "u-sari",
      email: "sari@tokomaju.id",
      role: "member",
      status: "pending",
      created_at: JOINED,
      invite_expires_at: "2026-09-19T00:00:00Z",
    },
    { id: "u-yoga", email: "yoga@tokomaju.id", role: "member", status: "deactivated", created_at: JOINED },
  ],
  agents: [
    { id: "ag-fin", name: "Finance", enabled: true, is_default: true },
    { id: "ag-hr", name: "HR", enabled: true, is_default: false },
    { id: "ag-ops", name: "Ops", enabled: true, is_default: false },
    { id: "ag-legal", name: "Legal", enabled: false, is_default: false },
  ],
  access: [
    { resource_kind: "agent", resource_id: "ag-fin", access_mode: "open", grants: [] },
    {
      resource_kind: "agent",
      resource_id: "ag-hr",
      access_mode: "restricted",
      grants: [grant("u-rina", "ag-hr"), grant("u-agus", "ag-hr")],
    },
    { resource_kind: "agent", resource_id: "ag-ops", access_mode: "open", grants: [grant("u-budi", "ag-ops")] },
    { resource_kind: "agent", resource_id: "ag-legal", access_mode: "open", grants: [] },
  ],
  // T-Z5. Payroll is restricted and, like HR, not granted to the admin looking —
  // so the card shows her the one dashboard she cannot open, by the name the
  // access list carries. Weekly sales is open with two live links and a revoked
  // one: the restrict warning must count two.
  dashboardAccess: [
    {
      resource_kind: "dashboard",
      resource_id: "db-pay",
      name: "Payroll by department",
      access_mode: "restricted",
      grants: [grant("u-rina", "db-pay", "dashboard"), grant("u-agus", "db-pay", "dashboard")],
    },
    {
      resource_kind: "dashboard",
      resource_id: "db-sales",
      name: "Weekly sales",
      access_mode: "open",
      grants: [grant("u-budi", "db-sales", "dashboard")],
    },
    { resource_kind: "dashboard", resource_id: "db-targets", name: "Branch targets", access_mode: "open", grants: [] },
  ],
  dashboardShares: {
    "db-sales": [
      { id: "sh-1", dashboard_id: "db-sales", created_at: JOINED, expires_at: "2026-12-01T00:00:00Z" },
      { id: "sh-2", dashboard_id: "db-sales", created_at: JOINED, expires_at: "2026-12-01T00:00:00Z" },
      { id: "sh-3", dashboard_id: "db-sales", created_at: JOINED, expires_at: "2026-12-01T00:00:00Z", revoked_at: "2026-08-01T00:00:00Z" },
    ],
  } as Record<string, unknown[]>,
  capabilities: {
    "u-rina": [{ user_id: "u-rina", capability: "approve_actions", granted_at: JOINED }],
  } as Record<string, unknown[]>,
  // T-Z8. Ops is open with a Discord room bound to it and nothing acknowledged,
  // so restricting Ops warns that one channel stops answering. HR is restricted:
  // its Slack channel was acknowledged and still answers, and its WhatsApp number
  // was bound before the restriction and is silent until somebody acknowledges.
  bindings: [
    {
      id: "bind-ops",
      company_id: "co-1",
      agent_id: "ag-ops",
      agent_name: "Ops",
      channel: "discord",
      external_id: "1182736459102938475",
      created_at: JOINED,
      agent_access_mode: "open",
    },
    {
      id: "bind-hr-slack",
      company_id: "co-1",
      agent_id: "ag-hr",
      agent_name: "HR",
      channel: "slack",
      external_id: "C07PEOPLE",
      created_at: JOINED,
      agent_access_mode: "restricted",
      restricted_acknowledged_at: "2026-09-10T03:12:00Z",
      restricted_acknowledged_by: "u-dewi",
    },
    {
      id: "bind-hr-wa",
      company_id: "co-1",
      agent_id: "ag-hr",
      agent_name: "HR",
      channel: "whatsapp",
      external_id: "+6281234500077",
      created_at: JOINED,
      agent_access_mode: "restricted",
    },
  ],
  // T-Z6. The HR warehouse is restricted and granted to Agus alone, so the
  // admin looking at it is refused its tests. Payroll 2026.pdf is open with a
  // grant made ahead of restricting it — the document the new warning is shot on.
  connectionAccess: [
    { resource_kind: "connection", resource_id: "src-main", name: "Toko Maju warehouse", access_mode: "open", grants: [] },
    {
      resource_kind: "connection",
      resource_id: "src-hr",
      name: "HR warehouse",
      access_mode: "restricted",
      grants: [grant("u-agus", "src-hr", "connection")],
    },
  ],
  documentAccess: [
    {
      resource_kind: "document",
      resource_id: "doc-pay",
      name: "Payroll 2026.pdf",
      access_mode: "open",
      grants: [grant("u-rina", "doc-pay", "document")],
    },
    { resource_kind: "document", resource_id: "doc-sop", name: "Store SOP v3.pdf", access_mode: "open", grants: [] },
  ],
};

/** The `@/lib/api` stub. Unknown paths answer an empty object rather than
 *  throwing: a tab this harness is not looking at must not be able to blank the
 *  screenshot of the one it is. */
export function makeAPI(skills: typeof SKILLS_OK) {
  const ok = (data: unknown) => Promise.resolve({ data });
  return {
    get: (path: string) => {
      if (path === "/skills") return ok(skills);
      if (path === "/threads") return ok(THREADS.threads ? THREADS : { threads: [] });
      if (path === "/users") return ok({ users: TEAM.users });
      if (path === "/agents") {
        return ok({ agents: TEAM.agents, reachable_agent_ids: ["ag-fin", "ag-ops", "ag-legal"], tools: [], templates: [] });
      }
      if (path === "/access/agent") return ok({ resources: TEAM.access });
      if (path === "/access/dashboard") return ok({ resources: TEAM.dashboardAccess });
      if (path === "/access/connection") return ok({ resources: TEAM.connectionAccess });
      if (path === "/access/document") return ok({ resources: TEAM.documentAccess });
      if (path === "/agent-bindings") {
        return ok({ bindings: TEAM.bindings, channels: ["whatsapp", "discord", "lark", "slack"] });
      }
      const links = path.match(/^\/dashboards\/([^/]+)\/shares$/);
      if (links) return ok({ shares: TEAM.dashboardShares[links[1]] ?? [] });
      const caps = path.match(/^\/users\/([^/]+)\/capabilities$/);
      if (caps) return ok({ capabilities: TEAM.capabilities[caps[1]] ?? [] });
      return ok({});
    },
    // The bodies are ignored on purpose: nothing here persists, and a save in
    // the harness is only ever pressed to photograph what the button does.
    post: (path: string, _body?: unknown) => {
      if (path === "/skills/preview") return ok(PREVIEW_OVER_CAP);
      return ok({});
    },
    put: (_path: string, _body?: unknown) => ok({}),
    delete: (_path: string) => ok({}),
  };
}

/** A frame around each shot: a title, so a screenshot filed in `docs/coverage`
 *  still says what it was taken to show once it is out of this directory. */
export function Scene({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="min-h-screen bg-background p-6">
      <p className="mb-4 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {title}
      </p>
      {children}
    </div>
  );
}
