// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

/**
 * A turn's tools behind one line (RIZ-95).
 *
 * The pairing is the part a reader cannot see go wrong: a call and its result
 * are two stream events, and counting events instead of calls is how seven
 * queries became fifteen chips. So the pure function is held to the exact
 * sequences the PRD names, and the component to the captions in its mockups.
 */

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}));

import {
  ToolStepsGroup,
  pairToolSteps,
  savedToolSteps,
  snapshotToolEntries,
  type ToolEntry,
  type ToolStep,
} from "./tool-steps-group";

const call = (name: string, payload: unknown = {}): ToolEntry => ({ name, payload, kind: "call" });
const result = (name: string, payload: unknown = {}): ToolEntry => ({ name, payload, kind: "result" });

/** The turn in the live mockups: seven real calls, the last still running. */
function sevenCallTurn(): ToolEntry[] {
  return [
    call("get_schema"),
    result("get_schema", { tables: ["orders"] }),
    call("run_sql", { sql: "SELECT 1" }),
    result("run_sql", { row_count: 1 }),
    call("run_sql", { sql: "SELECT 2" }),
    result("run_sql", { row_count: 2 }),
    call("run_sql", { sql: "SELECT 3" }),
    result("run_sql", { row_count: 3 }),
    call("compute", { expression: "1+1" }),
    result("compute", { value: 2 }),
    call("run_sql", { sql: "SELECT 4" }),
    result("run_sql", { row_count: 4 }),
    call("run_sql", { sql: "SELECT 5" }),
  ];
}

/** The tool chips inside the open panel, in DOM order. */
function chips(): HTMLElement[] {
  const panel = document.getElementById(
    screen.getByRole("button", { name: /Using tools|Used/ }).getAttribute("aria-controls")!,
  );
  if (!panel) return [];
  return within(panel).getAllByRole("button");
}

describe("pairToolSteps", () => {
  it("pairs a result with the oldest open call of its name (AC2)", () => {
    const steps = pairToolSteps([
      call("run_sql", { sql: "SELECT a", source_id: "s1" }),
      call("get_schema", { source_id: "s1" }),
      result("run_sql", { row_count: 3, source_id: "s2" }),
      result("get_schema", { tables: [] }),
      call("run_sql", { sql: "SELECT b" }),
    ]);
    expect(steps).toEqual<ToolStep[]>([
      // Arguments and result merged, the result's key winning where both have one.
      { name: "run_sql", payload: { sql: "SELECT a", source_id: "s2", row_count: 3 }, running: false },
      { name: "get_schema", payload: { source_id: "s1", tables: [] }, running: false },
      { name: "run_sql", payload: { sql: "SELECT b" }, running: true },
    ]);
  });

  it("counts seven calls, each followed by its result, as seven finished steps", () => {
    const entries: ToolEntry[] = [];
    for (let i = 0; i < 7; i++) entries.push(call("run_sql", { sql: `SELECT ${i}` }), result("run_sql"));
    const steps = pairToolSteps(entries);
    expect(steps).toHaveLength(7);
    expect(steps.every((s) => s.running === false)).toBe(true);
    expect(steps.map((s) => (s.payload as { sql: string }).sql)).toEqual(
      [0, 1, 2, 3, 4, 5, 6].map((i) => `SELECT ${i}`),
    );
  });

  it("keeps a result with no call of its name as a finished step of its own", () => {
    expect(pairToolSteps([result("create_dashboard", { url: "https://x" })])).toEqual([
      { name: "create_dashboard", payload: { url: "https://x" }, running: false },
    ]);
  });

  it("does not pair a result with a call of another tool", () => {
    const steps = pairToolSteps([call("run_sql"), result("get_schema")]);
    expect(steps.map((s) => [s.name, s.running])).toEqual([
      ["run_sql", true],
      ["get_schema", false],
    ]);
  });

  it("returns nothing for nothing", () => {
    expect(pairToolSteps([])).toEqual([]);
  });

  it("leaves the caller's payloads untouched", () => {
    const args = { sql: "SELECT 1" };
    pairToolSteps([call("run_sql", args), result("run_sql", { row_count: 1 })]);
    expect(args).toEqual({ sql: "SELECT 1" });
  });
});

describe("snapshotToolEntries", () => {
  it("reads a resumed turn of five calls and five results as five steps (AC8)", () => {
    const snapshot = [];
    for (let i = 0; i < 5; i++) {
      snapshot.push({ name: "run_sql", arguments: { sql: `SELECT ${i}` } });
      snapshot.push({ name: "run_sql", result: { row_count: i } });
    }
    const steps = pairToolSteps(snapshotToolEntries(snapshot));
    expect(steps).toHaveLength(5);
    expect(steps.every((s) => !s.running)).toBe(true);
  });

  it("tags by which field the snapshot kept", () => {
    expect(
      snapshotToolEntries([
        { name: "run_sql", arguments: { sql: "SELECT 1" } },
        { name: "run_sql", result: { row_count: 1 } },
      ]).map((e) => [e.kind, e.payload]),
    ).toEqual([
      ["call", { sql: "SELECT 1" }],
      ["result", { row_count: 1 }],
    ]);
  });

  // `omitempty` drops an empty map, so a load_skill result (which parses to
  // nothing) arrives as a bare name, exactly like a call made with no arguments.
  it("reads a bare entry as the result of an open call of its name", () => {
    const steps = pairToolSteps(
      snapshotToolEntries([
        { name: "load_skill", arguments: { name: "Weekly revenue" } },
        { name: "load_skill" },
      ]),
    );
    expect(steps).toEqual([
      { name: "load_skill", payload: { name: "Weekly revenue" }, running: false },
    ]);
  });

  it("reads the server's oversize stubs by their marker", () => {
    const steps = pairToolSteps(
      snapshotToolEntries([
        { name: "run_sql", arguments: { truncated: true } },
        { name: "run_sql", arguments: { sql: "SELECT 2" } },
        { name: "run_sql", result: { truncated: true } },
      ]),
    );
    expect(steps.map((s) => s.running)).toEqual([false, true]);
  });

  it("reads a bare entry with no open call as a call made with no arguments", () => {
    const steps = pairToolSteps(
      snapshotToolEntries([{ name: "get_schema" }, { name: "get_schema", result: { tables: [] } }]),
    );
    expect(steps).toEqual([{ name: "get_schema", payload: { tables: [] }, running: false }]);
  });
});

describe("ToolStepsGroup, live", () => {
  it("is one closed line naming the count and the running call (AC1)", () => {
    render(<ToolStepsGroup live steps={pairToolSteps(sevenCallTurn())} />);
    const header = screen.getByRole("button", { name: /Using tools/ });
    expect(header).toHaveTextContent("Using tools");
    expect(header).toHaveTextContent("7 calls · SQL query");
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(screen.getAllByRole("button")).toHaveLength(1);
    // The spinner in the primary colour is the live mark.
    expect(header.querySelector(".animate-spin.text-primary")).not.toBeNull();
  });

  it("says 1 call, not 1 calls", () => {
    render(<ToolStepsGroup live steps={[{ name: "get_schema", payload: {}, running: true }]} />);
    expect(screen.getByRole("button")).toHaveTextContent("1 call · Schema lookup");
  });

  it("names the newest call when none is running", () => {
    render(
      <ToolStepsGroup
        live
        steps={pairToolSteps([call("run_sql"), result("run_sql"), call("compute"), result("compute")])}
      />,
    );
    expect(screen.getByRole("button")).toHaveTextContent("2 calls · Calculation");
  });

  it("names the running call, not the newest finished one", () => {
    render(
      <ToolStepsGroup
        live
        steps={pairToolSteps([call("run_sql"), call("compute"), result("compute")])}
      />,
    );
    expect(screen.getByRole("button")).toHaveTextContent("2 calls · SQL query");
  });

  it("opens onto one chip per call in run order, stays open as calls arrive, and closes (AC3, AC4)", async () => {
    const turn = sevenCallTurn();
    const { rerender } = render(<ToolStepsGroup live steps={pairToolSteps(turn)} />);
    const header = screen.getByRole("button", { name: /Using tools/ });

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "true");
    let open = chips();
    expect(open.map((c) => c.textContent)).toEqual([
      "Schema lookup",
      "SQL query",
      "SQL query",
      "SQL query",
      "Calculation",
      "SQL query",
      "SQL query",
    ]);
    // Only the running call spins.
    expect(open.map((c) => c.querySelector(".animate-spin") !== null)).toEqual([
      false, false, false, false, false, false, true,
    ]);

    // An 8th call arrives: the group stays open and the chip lands at the end.
    rerender(
      <ToolStepsGroup
        live
        steps={pairToolSteps([...turn, result("run_sql"), call("get_schema")])}
      />,
    );
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(header).toHaveTextContent("8 calls · Schema lookup");
    open = chips();
    expect(open).toHaveLength(8);
    expect(open[7]).toHaveTextContent("Schema lookup");
    expect(open[6].querySelector(".animate-spin")).toBeNull();

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    await waitFor(() => expect(chips()).toHaveLength(0));
  });

  it("keeps a closed group closed while calls arrive, changing only the caption", () => {
    const turn = sevenCallTurn();
    const { rerender } = render(<ToolStepsGroup live steps={pairToolSteps(turn)} />);
    rerender(<ToolStepsGroup live steps={pairToolSteps([...turn, result("run_sql"), call("run_sql")])} />);
    const header = screen.getByRole("button");
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(header).toHaveTextContent("8 calls · SQL query");
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });

  it("keeps each chip's detail behind its own chevron (Q4)", () => {
    render(
      <ToolStepsGroup
        live
        steps={pairToolSteps([
          call("run_sql", { sql: "SELECT region FROM orders" }),
          result("run_sql", { row_count: 3 }),
          call("create_dashboard", { title: "Sales" }),
          result("create_dashboard", { url: "https://metabase.example/dashboard/7" }),
        ])}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Using tools/ }));
    const [sqlChip, dashChip] = chips();

    // The SQL came from the call and the row count from the result; the chip
    // still opens because the merged payload kept the statement.
    expect(sqlChip).toBeEnabled();
    expect(sqlChip).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(sqlChip);
    expect(screen.getByText("SELECT region FROM orders")).toBeInTheDocument();

    fireEvent.click(dashChip);
    expect(screen.getByRole("link", { name: /Open dashboard/ })).toHaveAttribute(
      "href",
      "https://metabase.example/dashboard/7",
    );
  });
});

/** The turn in the finished mockups, as the api now serves it: one record per
 *  call in run order, already paired. */
const SAVED = [
  { name: "get_schema", result: { status: "ok" } },
  { name: "run_sql", arguments: { sql: "SELECT region, SUM(total) FROM orders GROUP BY 1" } },
  { name: "run_sql", arguments: { sql: "SELECT 2" }, result: { status: "ok" } },
  { name: "compute", arguments: { expression: "a/b" } },
];

describe("savedToolSteps", () => {
  it("reads the list one step per call, arguments under the result (R8)", () => {
    expect(
      savedToolSteps([
        { name: "run_sql", arguments: { sql: "SELECT 1", source_id: "s1" }, result: { source_id: "s2" } },
        { name: "create_dashboard", result: { url: "https://x" } },
        { name: "get_schema" },
      ]),
    ).toEqual<ToolStep[]>([
      { name: "run_sql", payload: { sql: "SELECT 1", source_id: "s2" } },
      { name: "create_dashboard", payload: { url: "https://x" } },
      { name: "get_schema", payload: {} },
    ]);
  });

  it("reads the legacy by-name object an older api still serves, one step per key", () => {
    expect(savedToolSteps({ run_sql: { sql: "SELECT 1" }, get_schema: {} })).toEqual<ToolStep[]>([
      { name: "run_sql", payload: { sql: "SELECT 1" } },
      { name: "get_schema", payload: {} },
    ]);
  });

  it("reads anything else as no steps, and skips records with no name", () => {
    for (const junk of [undefined, null, "x", 42, []]) expect(savedToolSteps(junk)).toEqual([]);
    expect(savedToolSteps([1, "x", { arguments: {} }, { name: "run_sql" }])).toEqual([
      { name: "run_sql", payload: {} },
    ]);
  });
});

describe("ToolStepsGroup, finished", () => {
  const saved = savedToolSteps(SAVED);

  it("reads Used tools, counts the calls and names each tool once in run order (AC5)", () => {
    render(<ToolStepsGroup steps={saved} />);
    const header = screen.getByRole("button");
    expect(header).toHaveTextContent("Used tools");
    expect(header).toHaveTextContent("4 calls · Schema lookup, SQL query, Calculation");
    expect(header).toHaveAttribute("aria-expanded", "false");
    // A wrench, not a spinner: nothing is running.
    expect(header.querySelector(".animate-spin")).toBeNull();
    expect(header.querySelector(".lucide-wrench")).not.toBeNull();
  });

  it("opens onto one chip per call, repeats included, and each SQL chip opens its own statement", () => {
    render(<ToolStepsGroup steps={saved} />);
    fireEvent.click(screen.getByRole("button"));
    const open = chips();
    expect(open.map((c) => c.textContent)).toEqual([
      "Schema lookup",
      "SQL query",
      "SQL query",
      "Calculation",
    ]);
    expect(open.some((c) => c.querySelector(".animate-spin"))).toBe(false);
    fireEvent.click(open[1]);
    expect(screen.getByText(/SELECT region, SUM\(total\)/)).toBeInTheDocument();
    fireEvent.click(open[2]);
    expect(screen.getByText("SELECT 2")).toBeInTheDocument();
  });

  it("names a label once even when two tools share it", () => {
    render(
      <ToolStepsGroup
        steps={[
          { name: "run_sql", payload: {} },
          { name: "mcp__helpdesk__search", payload: {} },
          { name: "mcp__helpdesk__search_", payload: {} },
        ]}
      />,
    );
    expect(screen.getByRole("button")).toHaveTextContent("SQL query, Helpdesk · Search");
    expect(screen.getByRole("button")).not.toHaveTextContent("Search, Helpdesk");
  });

  it("renders nothing for no steps", () => {
    const { container } = render(<ToolStepsGroup steps={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("ToolStepsGroup, keyboard and screen readers (AC10)", () => {
  // jsdom does not turn Enter or Space on a button into a click; a browser
  // does, for a native <button> and only for one. So the contract held here is
  // that the header *is* that native button, takes focus, shows the ring, and
  // toggles on the activation a key press produces.
  it("is a focusable native button with a focus ring that toggles on activation", () => {
    render(<ToolStepsGroup live steps={pairToolSteps(sevenCallTurn())} />);
    const header = screen.getByRole("button");
    expect(header.tagName).toBe("BUTTON");
    expect(header).toHaveAttribute("type", "button");
    expect(header.className).toContain("focus-visible:ring-2");
    header.focus();
    expect(header).toHaveFocus();

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "true");
    const panel = document.getElementById(header.getAttribute("aria-controls")!);
    expect(panel).not.toBeNull();
    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
  });

  it("is not a live region", () => {
    const { container } = render(<ToolStepsGroup live steps={pairToolSteps(sevenCallTurn())} />);
    fireEvent.click(screen.getByRole("button"));
    expect(container.querySelector("[aria-live]")).toBeNull();
    expect(container.querySelector("[role=status]")).toBeNull();
  });
});
