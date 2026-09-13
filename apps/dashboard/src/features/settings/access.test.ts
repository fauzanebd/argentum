import { describe, expect, it } from "vitest";
import type { AccessMode, AgentChannelBinding, ResourceAccessView } from "@argentum/api-types";
import {
  DECISION_4,
  DISABLED_REASON_COPY,
  ENFORCED_KINDS,
  accessFor,
  accessSentence,
  bindingSilenceNotice,
  grantedIds,
  liveShareCount,
  losesAccessOnRestrict,
  reaches,
  restrictWarning,
  shareRevocationNotice,
  silencedBindings,
  type Person,
} from "./access";

/**
 * The rules Settings → Team draws the access matrix by (T-Z7).
 *
 * The one that matters most is the one a reader would most expect to be
 * otherwise: **role is not an input.** `internal/authz` refuses an admin a
 * restricted agent they are not granted (roadmap 12, decision 4), and a matrix
 * that drew them as reaching it would be the single screen in the product
 * telling an admin the opposite of what happens when they press send.
 */

const PEOPLE: Person[] = [
  { id: "u-dewi", email: "dewi@acme.id", role: "admin", status: "active" },
  { id: "u-rina", email: "rina@acme.id", role: "member", status: "active" },
  { id: "u-budi", email: "budi@acme.id", role: "member", status: "active" },
  { id: "u-agus", email: "agus@acme.id", role: "admin", status: "active" },
  { id: "u-sari", email: "sari@acme.id", role: "member", status: "pending" },
  { id: "u-old", email: "old@acme.id", role: "member", status: "deactivated" },
];

function view(id: string, mode: AccessMode, users: string[]): ResourceAccessView {
  return {
    resource_kind: "agent",
    resource_id: id,
    access_mode: mode,
    grants: users.map((u) => ({
      user_id: u,
      resource_kind: "agent",
      resource_id: id,
      granted_at: "2026-09-12T00:00:00Z",
    })),
  } as ResourceAccessView;
}

describe("reaches", () => {
  it("refuses an admin a restricted agent they are not granted, as it refuses anyone", () => {
    const hr = view("ag-hr", "restricted", ["u-rina"]);
    expect(reaches(hr, "u-dewi")).toBe(false);
    expect(reaches(hr, "u-budi")).toBe(false);
    expect(reaches(hr, "u-rina")).toBe(true);
  });

  it("opens an open agent to everyone, and treats one the answer has not caught up with as open", () => {
    expect(reaches(view("ag-fin", "open", []), "u-budi")).toBe(true);
    // An agent created after the matrix was read. 084's default is open, so is
    // this — drawing it restricted would show a lock that is not there.
    expect(reaches(undefined, "u-budi")).toBe(true);
  });
});

describe("losesAccessOnRestrict", () => {
  it("counts everyone who can sign in and holds no grant — admins too — and nobody pending or removed", () => {
    const losing = losesAccessOnRestrict(view("ag-hr", "open", ["u-rina"]), PEOPLE);
    expect(losing.map((p) => p.id)).toEqual(["u-dewi", "u-budi", "u-agus"]);
  });
});

describe("restrictWarning", () => {
  it("says nobody, the admin included, when nobody is granted — the lock-out T-Z2 warns about", () => {
    expect(restrictWarning("Finance", view("ag-fin", "open", []), PEOPLE, "u-dewi")).toMatch(
      /^Nobody is granted Finance\. Restricting it means nobody — you included — can talk to it/,
    );
  });

  it("names the count, and whether the person restricting it is in it", () => {
    const hr = view("ag-hr", "open", ["u-rina"]);
    expect(restrictWarning("HR", hr, PEOPLE, "u-dewi")).toMatch(
      /^3 people will lose access to HR, you included:/,
    );
    expect(restrictWarning("HR", hr, PEOPLE, "u-rina")).toMatch(/^3 people will lose access to HR:/);
  });

  it("does not word 'everyone is granted' the way it words 'nobody is'", () => {
    const everyone = view("ag-ops", "open", ["u-dewi", "u-rina", "u-budi", "u-agus"]);
    expect(restrictWarning("Ops", everyone, PEOPLE, "u-dewi")).toBe(
      "Everyone who can sign in is granted Ops, so nobody loses access.",
    );
  });
});

describe("a dashboard's restrict warning (T-Z5)", () => {
  it("says what a dashboard takes away, in each of the three shapes", () => {
    expect(
      restrictWarning("Payroll", view("db-pay", "open", []), PEOPLE, "u-dewi", "dashboard"),
    ).toBe(
      "Nobody is granted Payroll. Restricting it means nobody — you included — can open it, until you grant someone.",
    );
    expect(
      restrictWarning("Payroll", view("db-pay", "open", ["u-rina"]), PEOPLE, "u-dewi", "dashboard"),
    ).toMatch(/^3 people will lose access to Payroll, you included: it will be gone from their dashboards, and a link to it/);
    // A dashboard's warning makes none of an agent's promises — not being offered
    // in chat, not conversations hidden — because restricting a dashboard does
    // neither. (It may mention a chat reply: that is where its link often is.)
    expect(
      restrictWarning("Payroll", view("db-pay", "open", ["u-rina"]), PEOPLE, "u-dewi", "dashboard"),
    ).not.toMatch(/offered it in chat|conversations it was in/);
  });

  it("counts only the links that would still open, which are the ones restricting revokes", () => {
    const now = new Date("2026-09-12T12:00:00Z");
    const links = [
      { expires_at: "2026-10-01T00:00:00Z" },
      { expires_at: "2026-10-01T00:00:00Z", revoked_at: null },
      { expires_at: "2026-10-01T00:00:00Z", revoked_at: "2026-09-10T00:00:00Z" },
      { expires_at: "2026-09-01T00:00:00Z" },
    ];
    expect(liveShareCount(links, now)).toBe(2);
    expect(liveShareCount([], now)).toBe(0);
  });

  it("names the count before the press, and never promises less than it will do", () => {
    expect(shareRevocationNotice(2, false)).toMatch(/^Its 2 live share links will be revoked/);
    expect(shareRevocationNotice(1, false)).toMatch(/^Its 1 live share link will be revoked/);
    expect(shareRevocationNotice(0, false)).toMatch(/^It has no live share links/);
    expect(shareRevocationNotice(undefined, false)).toBe("Checking for live share links…");
    // A count that could not be read must not read as "none".
    expect(shareRevocationNotice(undefined, true)).toMatch(/still revokes every live one/);
  });

  it("words a person's dashboard row by what opening is, not by what talking is", () => {
    const row = { id: "db-pay", name: "Payroll", mode: "restricted" as const, granted: false, reaches: false };
    expect(accessSentence(row, false, "dashboard")).toBe("Restricted. They cannot open it.");
    expect(accessSentence({ ...row, granted: true, reaches: true }, true, "dashboard")).toBe(
      "Restricted. You can open it: granted.",
    );
  });
});

describe("accessFor", () => {
  it("is the agent side read the other way — no cell can disagree with the grant list it came from", () => {
    const agents = [
      { id: "ag-hr", name: "HR" },
      { id: "ag-fin", name: "Finance" },
      { id: "ag-new", name: "Created after the read" },
    ];
    const byId = new Map([
      ["ag-hr", view("ag-hr", "restricted", ["u-rina", "u-agus"])],
      ["ag-fin", view("ag-fin", "open", ["u-budi"])],
    ]);
    for (const p of PEOPLE) {
      for (const row of accessFor(p.id, agents, byId)) {
        const v = byId.get(row.id);
        expect(row.granted, `${p.id} × ${row.id}`).toBe(grantedIds(v).has(p.id));
        expect(row.reaches, `${p.id} × ${row.id}`).toBe(reaches(v, p.id));
        expect(row.mode).toBe(v?.access_mode ?? "open");
      }
    }
  });
});

describe("what Settings offers", () => {
  it("offers a switch for every kind, now that T-Z6 makes a restriction refuse something for sources and documents", () => {
    // Each kind joined this list in the commit that enforced it: T-Z5 added
    // `dashboard`, T-Z6 `connection` and `document`. A switch for a kind nothing
    // enforces is a control that does nothing and says it did.
    expect([...ENFORCED_KINDS]).toEqual(["agent", "dashboard", "connection", "document"]);
  });

  it("says what restricting a source takes away, and that an agent's reach is not it", () => {
    const people = PEOPLE;
    const src = view("src-hr", "open", ["u-rina"]);
    const warning = restrictWarning("HR warehouse", src, people, "u-dewi", "connection");
    expect(warning).toMatch(/^3 people will be refused HR warehouse, you included:/);
    expect(warning).toContain("Agents that query it keep querying it");
    // The row a person sees for a source they are refused says "as a member":
    // an admin still sees every source where sources are configured.
    const row = accessFor("u-budi", [{ id: "src-hr", name: "HR warehouse" }], new Map([["src-hr", view("src-hr", "restricted", [])]]))[0];
    expect(accessSentence(row, false, "connection")).toBe("Restricted. They cannot see it as a member, or run its tests.");
  });

  it("says what restricting a document takes away, including what an agent will find", () => {
    const doc = view("doc-pay", "open", ["u-rina"]);
    const warning = restrictWarning("Payroll 2026.pdf", doc, PEOPLE, "u-dewi", "document");
    expect(warning).toMatch(/^3 people will lose access to Payroll 2026\.pdf, you included:/);
    expect(warning).toContain("an agent searching documents for them will find nothing in it");
    expect(restrictWarning("Payroll 2026.pdf", view("doc-pay", "open", []), PEOPLE, "u-dewi", "document"))
      .toMatch(/^Nobody is granted Payroll 2026\.pdf\./);
  });

  it("quotes decision 4 in the roadmap's words", () => {
    expect(DECISION_4).toContain(
      "this is a boundary against accident and casual browsing, not against a determined admin",
    );
    expect(DECISION_4.toLowerCase()).not.toContain("secure");
  });
});

describe("the channels restricting an agent silences (T-Z8)", () => {
  const at = "2026-09-01T00:00:00Z";
  const bindings: (AgentChannelBinding | undefined)[] = [
    { id: "b-ops-discord", company_id: "co", agent_id: "ag-ops", channel: "discord", external_id: "118", created_at: at },
    {
      id: "b-ops-slack",
      company_id: "co",
      agent_id: "ag-ops",
      channel: "slack",
      external_id: "C01",
      created_at: at,
      restricted_acknowledged_at: at,
    },
    { id: "b-fin", company_id: "co", agent_id: "ag-fin", channel: "lark", external_id: "oc", created_at: at },
    undefined,
  ];

  it("counts the agent's bindings nobody acknowledged, and none of another agent's", () => {
    // The acknowledged Slack channel keeps answering, so it is not counted:
    // nothing about it changes when the agent is restricted.
    expect(silencedBindings(bindings, "ag-ops").map((b) => b.id)).toEqual(["b-ops-discord"]);
    expect(silencedBindings(bindings, "ag-hr")).toEqual([]);
  });

  it("says what the press will do before it is pressed, in each shape", () => {
    expect(bindingSilenceNotice(undefined, false)).toMatch(/^Checking/);
    expect(bindingSilenceNotice(0, false)).toMatch(/^No channel stops answering/);
    expect(bindingSilenceNotice(1, false)).toMatch(/^1 channel bound to it will stop answering/);
    expect(bindingSilenceNotice(3, false)).toMatch(/^3 channels bound to it will stop answering/);
    // A read that failed must never sound like "nothing will happen".
    expect(bindingSilenceNotice(undefined, true)).toMatch(/still stops every channel/);
  });

  it("words every reason the product switches a watcher or a schedule off for", () => {
    expect(Object.keys(DISABLED_REASON_COPY).sort()).toEqual(["creator_not_granted", "creator_removed"]);
    for (const sentence of Object.values(DISABLED_REASON_COPY)) {
      expect(sentence).toMatch(/^Turned off: /);
    }
  });
});
