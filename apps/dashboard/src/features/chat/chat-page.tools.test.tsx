// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatEvent, Message } from "@argentum/api-types";

/**
 * The chat page's tool areas (RIZ-95), through the page rather than the
 * component: what is under test here is the wiring — that a saved message
 * picks a group, a bare chip or nothing by how many tools it used, that the
 * live bubble pairs what the socket sends, and that a turn resumed from the
 * server's snapshot counts the same calls the live stream would have.
 */

const THREAD = "t1";

let stream: ((evt: ChatEvent) => void) | undefined;
let messages: Message[] = [];

vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ threadId: THREAD }),
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
}));
// The thread and its history load; every other read fails, as in
// chat-page.test.tsx, because none of them is what this file is about.
vi.mock("@/lib/api", () => ({
  api: {
    get: (url: string) => {
      if (url === "/threads") {
        return Promise.resolve({ data: { threads: [{ id: THREAD, title: "Weekly sales review" }] } });
      }
      if (url === `/threads/${THREAD}/messages`) return Promise.resolve({ data: { messages } });
      return Promise.reject(new Error("not under test"));
    },
    post: () => Promise.reject(new Error("not under test")),
  },
}));
// The socket, replaced by a handle the test pushes events through.
vi.mock("./use-thread-stream", () => ({
  useThreadStream: (_id: string | null, onEvent: (evt: ChatEvent) => void) => {
    stream = onEvent;
  },
}));

import { ChatPage } from "./chat-page";

function message(id: string, role: "user" | "assistant", content: string, tool_calls?: Record<string, unknown>): Message {
  return {
    id,
    thread_id: THREAD,
    role,
    content,
    tool_calls,
    created_at: "2026-10-14T07:02:00Z",
  } as unknown as Message;
}

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ChatPage />
    </QueryClientProvider>,
  );
}

function send(evt: Partial<ChatEvent>) {
  act(() => {
    stream!({ job_id: "m9", thread_id: THREAD, timestamp: new Date().toISOString(), type: "", ...evt } as ChatEvent);
  });
}

/** The bubble body a message's text sits in, where its tool area renders. */
function bubbleOf(text: string): HTMLElement {
  return screen.getByText(text).closest("div.text-sm, div.inline-block") as HTMLElement;
}

beforeEach(() => {
  stream = undefined;
  messages = [];
  // The timeline scrolls to its newest turn; jsdom lays nothing out and has no
  // element scrolling to do it with.
  Element.prototype.scrollTo = () => {};
});

describe("a saved message's tools", () => {
  it("groups two or more, keeps one bare, and draws nothing for none (AC5, AC6)", async () => {
    messages = [
      message("u1", "user", "How many orders came in yesterday?"),
      message("a1", "assistant", "Yesterday took 1,284 orders.", { run_sql: { sql: "SELECT count(*) FROM orders" } }),
      message("u2", "user", "Compare this week with last week by region."),
      message("a2", "assistant", "Surabaya dropped 7.4%.", {
        compute: { expression: "a/b" },
        run_sql: { sql: "SELECT region FROM orders" },
        get_schema: {},
      }),
      message("a3", "assistant", "Hello, how can I help?"),
    ];
    mount();
    await screen.findByText("Surabaya dropped 7.4%.");

    // Three tools: one closed line.
    const grouped = within(bubbleOf("Surabaya dropped 7.4%."));
    const header = grouped.getByRole("button", { name: /Used 3 tools/ });
    expect(header).toHaveTextContent("Calculation, SQL query, Schema lookup");
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(grouped.queryByRole("button", { name: "SQL query" })).toBeNull();
    fireEvent.click(header);
    expect(grouped.getAllByRole("button").map((b) => b.textContent)).toEqual([
      expect.stringContaining("Used 3 tools"),
      "Calculation",
      "SQL query",
      "Schema lookup",
    ]);
    fireEvent.click(grouped.getByRole("button", { name: "SQL query" }));
    expect(grouped.getByText("SELECT region FROM orders")).toBeInTheDocument();

    // One tool: the bare chip, as before, with no group around it.
    const single = within(bubbleOf("Yesterday took 1,284 orders."));
    expect(single.queryByRole("button", { name: /Used/ })).toBeNull();
    expect(single.getByRole("button", { name: "SQL query" })).toHaveAttribute("aria-expanded", "false");

    // None: no divider over nothing.
    expect(bubbleOf("Hello, how can I help?").querySelector(".border-t")).toBeNull();
  });

  it("groups a user bubble's tools the same way", async () => {
    messages = [
      message("u1", "user", "Run these for me.", { run_sql: { sql: "SELECT 1" }, get_schema: {} }),
    ];
    mount();
    await screen.findByText("Run these for me.");
    expect(
      within(bubbleOf("Run these for me.")).getByRole("button", { name: /Used 2 tools/ }),
    ).toHaveTextContent("SQL query, Schema lookup");
  });
});

describe("the turn in flight", () => {
  it("folds the live calls into one line that counts calls, not events (AC1)", async () => {
    mount();
    await screen.findByText(/No messages yet/);
    for (let i = 0; i < 7; i++) {
      send({ type: "tool_call", tool_call: { name: "run_sql", arguments: { sql: `SELECT ${i}` } } });
      if (i < 6) send({ type: "tool_result", tool_call: { name: "run_sql", result: { row_count: i } } });
    }
    const header = screen.getByRole("button", { name: /Using tools/ });
    expect(header).toHaveTextContent("7 calls · SQL query");
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: "SQL query" })).toBeNull();

    // Open, then an 8th call: still open, chip at the end.
    fireEvent.click(header);
    send({ type: "tool_result", tool_call: { name: "run_sql", result: { row_count: 6 } } });
    send({ type: "tool_call", tool_call: { name: "get_schema", arguments: {} } });
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(header).toHaveTextContent("8 calls · Schema lookup");
    const panel = document.getElementById(header.getAttribute("aria-controls")!)!;
    const chips = within(panel).getAllByRole("button");
    expect(chips).toHaveLength(8);
    expect(chips[7]).toHaveTextContent("Schema lookup");
    expect(chips[7].querySelector(".animate-spin")).not.toBeNull();
  });

  it("leaves the status line the only thing a screen reader is told (AC8)", async () => {
    mount();
    await screen.findByText(/No messages yet/);
    send({ type: "tool_call", tool_call: { name: "run_sql", arguments: { sql: "SELECT 1" } } });
    fireEvent.click(screen.getByRole("button", { name: /Using tools/ }));
    expect(screen.getAllByRole("status")).toHaveLength(1);
    expect(document.querySelectorAll("[aria-live]")).toHaveLength(1);
  });

  it("counts a resumed turn's calls once, not once per event (AC7)", async () => {
    mount();
    await screen.findByText(/No messages yet/);
    const tool_calls = [];
    for (let i = 0; i < 5; i++) {
      tool_calls.push({ name: "run_sql", arguments: { sql: `SELECT ${i}` } });
      tool_calls.push({ name: "run_sql", result: { row_count: i } });
    }
    send({
      type: "state",
      job_id: "m9",
      live: {
        job_id: "m9",
        started_at: new Date().toISOString(),
        last_event_at: new Date().toISOString(),
        tool_calls,
      },
    });
    expect(screen.getByRole("button", { name: /Using tools/ })).toHaveTextContent("5 calls · SQL query");
  });
});
