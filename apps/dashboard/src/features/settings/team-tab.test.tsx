// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Settings → Team's access matrix (T-Z7, and T-Z5's dashboards), driven through
 * the real tab against an API stub that remembers what it was told.
 *
 * The stub is stateful on purpose. The acceptance lines are about a *round
 * trip* — a grant made on one side shows on the other without a reload — and a
 * stub that answered the same fixture before and after would pass a screen that
 * never refetched.
 */

type Mode = "open" | "restricted";
type Kind = "agent" | "dashboard";
let modes: Record<string, Mode>;
let grants: Record<string, string[]>;
let caps: Record<string, string[]>;
let shares: Record<string, { id: string; expires_at: string; revoked_at?: string }[]>;
let accessFails: boolean;

const PEOPLE = [
  { id: "u-dewi", email: "dewi@acme.id", role: "admin", status: "active", created_at: "" },
  { id: "u-rina", email: "rina@acme.id", role: "member", status: "active", created_at: "" },
  { id: "u-budi", email: "budi@acme.id", role: "member", status: "active", created_at: "" },
  { id: "u-sari", email: "sari@acme.id", role: "member", status: "pending", created_at: "" },
  { id: "u-old", email: "old@acme.id", role: "member", status: "deactivated", created_at: "" },
];

const AGENTS = [
  { id: "ag-hr", name: "HR", enabled: true, is_default: false },
  { id: "ag-fin", name: "Finance", enabled: true, is_default: true },
];

// Dashboards exist on this screen only as the access list names them — the
// stub answers no `/dashboards`, so a tab that read names from there would
// render none (T-Z5).
const DASHBOARDS = [
  { id: "db-pay", name: "Payroll" },
  { id: "db-sales", name: "Weekly sales" },
];

const get = vi.fn();
const put = vi.fn();
const del = vi.fn();

function views(kind: Kind, resources: { id: string; name: string }[]) {
  return resources.map((r) => ({
    resource_kind: kind,
    resource_id: r.id,
    name: r.name,
    access_mode: modes[r.id],
    grants: (grants[r.id] ?? []).map((u) => ({
      user_id: u,
      resource_kind: kind,
      resource_id: r.id,
      granted_at: "",
    })),
  }));
}

async function getImpl(path: string) {
  if (path === "/users") return { data: { users: PEOPLE } };
  if (path === "/agents") {
    return { data: { agents: AGENTS, reachable_agent_ids: [], tools: [], templates: [] } };
  }
  if (path === "/access/agent" || path === "/access/dashboard") {
    if (accessFails) throw new Error("could not check access");
    return {
      data: { resources: path === "/access/agent" ? views("agent", AGENTS) : views("dashboard", DASHBOARDS) },
    };
  }
  const links = path.match(/^\/dashboards\/([^/]+)\/shares$/);
  if (links) return { data: { shares: shares[links[1]] ?? [] } };
  const cap = path.match(/^\/users\/([^/]+)\/capabilities$/);
  if (cap) {
    return {
      data: {
        capabilities: (caps[cap[1]] ?? []).map((c) => ({ user_id: cap[1], capability: c, granted_at: "" })),
      },
    };
  }
  return { data: {} };
}

async function putImpl(path: string, body?: { access_mode?: Mode }) {
  let m = path.match(/^\/access\/(?:agent|dashboard)\/([^/]+)\/grants\/([^/]+)$/);
  if (m) grants[m[1]] = [...new Set([...(grants[m[1]] ?? []), m[2]])];
  m = path.match(/^\/access\/(?:agent|dashboard)\/([^/]+)\/mode$/);
  if (m && body?.access_mode) {
    modes[m[1]] = body.access_mode;
    return { data: { access_mode: body.access_mode, revoked_shares: 0 } };
  }
  m = path.match(/^\/users\/([^/]+)\/capabilities\/([^/]+)$/);
  if (m) caps[m[1]] = [...(caps[m[1]] ?? []), m[2]];
  return { data: {} };
}

async function delImpl(path: string) {
  const m = path.match(/^\/access\/(?:agent|dashboard)\/([^/]+)\/grants\/([^/]+)$/);
  if (m) grants[m[1]] = (grants[m[1]] ?? []).filter((u) => u !== m[2]);
  return { data: {} };
}

vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: vi.fn(),
    patch: vi.fn(),
    put: (...args: unknown[]) => put(...args),
    delete: (...args: unknown[]) => del(...args),
  },
}));
vi.mock("@/store/auth", () => ({
  useAuthStore: (select: (s: { user: { id: string } }) => unknown) => select({ user: { id: "u-dewi" } }),
  useIsAdmin: () => true,
}));
vi.mock("@/hooks/use-toast", () => ({ useToast: () => ({ toast: vi.fn() }) }));
vi.mock("@/lib/api-error", () => ({ apiErrorMessage: (e: Error) => e.message }));

import { TeamTab } from "./team-tab";

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TeamTab />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  modes = { "ag-hr": "open", "ag-fin": "open", "db-pay": "open", "db-sales": "open" };
  grants = {};
  caps = {};
  shares = {};
  accessFails = false;
  get.mockReset().mockImplementation(getImpl);
  put.mockReset().mockImplementation(putImpl);
  del.mockReset().mockImplementation(delImpl);
});

describe("TeamTab — who may reach what", () => {
  it("shows a grant made on a person on the agent's side without a reload, and a revoke there back on the person", async () => {
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Access for rina@acme.id" }));
    const box = await screen.findByRole("checkbox", { name: "HR granted to rina@acme.id" });
    expect(box).not.toBeChecked();

    fireEvent.click(box);
    await waitFor(() => expect(put).toHaveBeenCalledWith("/access/agent/ag-hr/grants/u-rina"));
    const revoke = await screen.findByRole("button", { name: "Revoke HR from rina@acme.id" });
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "HR granted to rina@acme.id" })).toBeChecked(),
    );

    fireEvent.click(revoke);
    await waitFor(() => expect(del).toHaveBeenCalledWith("/access/agent/ag-hr/grants/u-rina"));
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "HR granted to rina@acme.id" })).not.toBeChecked(),
    );
    expect(screen.queryByRole("button", { name: "Revoke HR from rina@acme.id" })).toBeNull();

    // One read, two renders: neither side asked the per-person grants route,
    // which is the second opinion that could have disagreed.
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/users/u-rina/grants");
  });

  it("warns before restricting, names how many lose access, and changes nothing until confirmed", async () => {
    grants = { "ag-hr": ["u-rina"] };
    mount();

    fireEvent.click(await screen.findByRole("button", { name: "Restrict HR…" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Restrict HR" });
    // Dewi and Budi can sign in and hold nothing. Sari's invitation is pending
    // and Old was removed — neither has access to lose, so neither is counted.
    expect(within(dialog).getByText(/^2 people will lose access to HR, you included:/)).toBeInTheDocument();
    expect(within(dialog).getByText("dewi@acme.id (you), budi@acme.id")).toBeInTheDocument();
    // An agent has no links to revoke, and its warning does not ask for any.
    expect(within(dialog).queryByText(/share link/)).toBeNull();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(put).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Restrict HR…" }));
    const again = await screen.findByRole("alertdialog", { name: "Restrict HR" });
    fireEvent.click(within(again).getByRole("button", { name: "Restrict HR" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/access/agent/ag-hr/mode", { access_mode: "restricted" }),
    );
    expect(await screen.findByRole("button", { name: "Open to everyone" })).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("says nobody — the admin included — can reach an agent restricted with no grants", async () => {
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Restrict Finance…" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Restrict Finance" });
    expect(
      within(dialog).getByText(/^Nobody is granted Finance\. Restricting it means nobody — you included/),
    ).toBeInTheDocument();
  });

  it("shows an admin that they themselves are refused a restricted agent they are not granted", async () => {
    modes["ag-hr"] = "restricted";
    grants = { "ag-hr": ["u-rina"] };
    mount();

    expect(await screen.findByText(/^You are not granted HR, so you cannot talk to it/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Access for dewi@acme.id" }));
    expect(
      await screen.findByText("Restricted. You cannot talk to it or open a conversation it was in."),
    ).toBeInTheDocument();
  });

  it("toggles a capability for that person, and says what it changes on screen today", async () => {
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Access for rina@acme.id" }));
    const voice = await screen.findByRole("checkbox", { name: "Voice for rina@acme.id" });
    // T-W9 rewrote this sentence when the microphone shipped, as T-Z7 said it would.
    expect(screen.getByText(/a microphone in the chat that puts what they say in the message box/)).toBeInTheDocument();

    fireEvent.click(voice);
    await waitFor(() => expect(put).toHaveBeenCalledWith("/users/u-rina/capabilities/voice"));
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "Voice for rina@acme.id" })).toBeChecked(),
    );
  });

  it("offers no switch when the access read fails, and says why", async () => {
    accessFails = true;
    mount();
    expect(
      await screen.findByText(/^Could not load who can reach each agent\. Nothing here can be changed/),
    ).toBeInTheDocument();
    expect(
      await screen.findByText(/^Could not load who can open each dashboard\. Nothing here can be changed/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Restrict / })).toBeNull();
  });
});

describe("TeamTab — who may open which dashboard (T-Z5)", () => {
  it("warns before restricting a dashboard, counts the live links it will revoke, and changes nothing until confirmed", async () => {
    grants = { "db-sales": ["u-rina"] };
    shares = {
      "db-sales": [
        { id: "sh-1", expires_at: "2099-01-01T00:00:00Z" },
        { id: "sh-2", expires_at: "2099-01-01T00:00:00Z" },
        { id: "sh-3", expires_at: "2099-01-01T00:00:00Z", revoked_at: "2026-09-01T00:00:00Z" },
        { id: "sh-4", expires_at: "2020-01-01T00:00:00Z" },
      ],
    };
    mount();

    // Nothing asks for a dashboard's links until somebody presses Restrict.
    await screen.findByRole("button", { name: "Restrict Weekly sales…" });
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/dashboards/db-sales/shares");

    fireEvent.click(screen.getByRole("button", { name: "Restrict Weekly sales…" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Restrict Weekly sales" });
    expect(
      within(dialog).getByText(/^2 people will lose access to Weekly sales, you included: it will be gone from their dashboards/),
    ).toBeInTheDocument();
    // Two of the four links would still open — one was revoked and one expired.
    expect(await within(dialog).findByText(/^Its 2 live share links will be revoked/)).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(put).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Restrict Weekly sales…" }));
    const again = await screen.findByRole("alertdialog", { name: "Restrict Weekly sales" });
    fireEvent.click(within(again).getByRole("button", { name: "Restrict Weekly sales" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/access/dashboard/db-sales/mode", { access_mode: "restricted" }),
    );
    expect(await screen.findByRole("button", { name: "Open to everyone" })).toBeInTheDocument();
  });

  it("lists a restricted dashboard the admin is refused by the name the access list carries, on both sides", async () => {
    modes["db-pay"] = "restricted";
    grants = { "db-pay": ["u-rina"] };
    mount();

    expect(await screen.findByText(/^You are not granted Payroll, so you cannot open it\./)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Access for rina@acme.id" }));
    expect(await screen.findByRole("checkbox", { name: "Payroll granted to rina@acme.id" })).toBeChecked();

    // Granted from the person's side, shown on the dashboard's without a reload.
    fireEvent.click(screen.getByRole("checkbox", { name: "Weekly sales granted to rina@acme.id" }));
    await waitFor(() => expect(put).toHaveBeenCalledWith("/access/dashboard/db-sales/grants/u-rina"));
    expect(await screen.findByRole("button", { name: "Revoke Weekly sales from rina@acme.id" })).toBeInTheDocument();

    // The narrowed dashboards list was never the source of a name here.
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/dashboards");
  });
});
