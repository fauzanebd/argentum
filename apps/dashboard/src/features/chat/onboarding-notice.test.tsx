// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The new-chat screen's onboarding slot (RIZ-76): the connect-your-data card
 * and the reading / ready / timeout notice, driven through the real hook
 * against an API stub whose source list the test changes between polls.
 *
 * Fake timers, because the acceptance lines are about time — a 5s poll and a
 * 5-minute give-up — and a test that waited for them would be a test nobody
 * runs.
 */

type Conn = { id: string; description: string };
let connections: Conn[] | null;
let connectionsFail: boolean;
let connectionsPending: boolean;

const get = vi.fn();

vi.mock("@/lib/api", () => ({
  api: { get: (...args: unknown[]) => get(...args) },
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children }: { to: string; children: React.ReactNode }) => <a href={to}>{children}</a>,
}));

import { OnboardingNotice } from "./onboarding-notice";
import {
  SCHEMA_POLL_MS,
  SCHEMA_WAIT_MS,
  useOnboardingStatus,
  type OnboardingStatus,
} from "./use-onboarding-status";

let seen: { status: OnboardingStatus; hasData: boolean };

function Harness({ isAdmin, active = true }: { isAdmin: boolean; active?: boolean }) {
  const s = useOnboardingStatus(active);
  seen = s;
  return <OnboardingNotice status={s.status} isAdmin={isAdmin} />;
}

let qc: QueryClient;

async function mount(isAdmin = true, active = true) {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Harness isAdmin={isAdmin} active={active} />
    </QueryClientProvider>,
  );
  await flush();
}

/** Moves the clock, then lets whatever request that started resolve and
 *  React render what it returned. React Query hands results to observers on a
 *  timer of its own, scheduled only once the request's promise chain settles —
 *  after the advance that started it has returned — so a few more
 *  millisecond steps are what deliver it. */
async function flush(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
  for (let i = 0; i < 10; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
  }
}

const connectionCalls = () => get.mock.calls.filter(([p]) => p === "/connections").length;

beforeEach(() => {
  vi.useFakeTimers();
  connections = [];
  connectionsFail = false;
  connectionsPending = false;
  get.mockReset().mockImplementation(async (path: string) => {
    if (path !== "/connections") return { data: {} };
    if (connectionsPending) return new Promise(() => {});
    if (connectionsFail) throw new Error("500");
    return { data: { connections } };
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("the connect-your-data card (R3)", () => {
  it("offers an admin the way to /onboarding when nothing is connected (AC3)", async () => {
    await mount(true);
    expect(screen.getByRole("heading", { name: "Connect your data to start asking questions" })).toBeTruthy();
    expect(
      screen.getByText(
        "Argentum answers from your own database. Connecting one takes a few minutes and needs read-only credentials.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("link", { name: "Connect your data" }).getAttribute("href")).toBe("/onboarding");
    expect(seen).toEqual({ status: "no-data", hasData: false });
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("tells a member to ask an admin, with no button (AC4)", async () => {
    await mount(false);
    expect(screen.getByRole("heading", { name: "No data connected yet" })).toBeTruthy();
    expect(
      screen.getByText(
        "Ask an admin of this workspace to connect a database. Until then, answers can't use your company's data.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("reads a null list — a Go nil slice — as nothing connected", async () => {
    connections = null;
    await mount(true);
    expect(seen.status).toBe("no-data");
  });

  it("shows nothing while the list is loading, so nothing flashes (Q7)", async () => {
    connectionsPending = true;
    await mount(true);
    await flush(3000);
    expect(seen).toEqual({ status: "unknown", hasData: false });
    expect(document.body.textContent).toBe("");
  });

  it("shows nothing when the list cannot be read — the screen as it was (Q7)", async () => {
    connectionsFail = true;
    await mount(true);
    expect(seen).toEqual({ status: "unknown", hasData: false });
    expect(document.body.textContent).toBe("");
  });

  it("neither fetches nor shows anything in a thread", async () => {
    await mount(true, false);
    expect(connectionCalls()).toBe(0);
    expect(seen.status).toBe("unknown");
  });
});

describe("the reading-your-schema notice (R4)", () => {
  it("shows while a description is empty, then flips to Ready and stops polling (AC5)", async () => {
    connections = [{ id: "c1", description: "" }];
    await mount(true);
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    expect(screen.getByRole("status").textContent).toBe(
      "Reading your schema. Answers get better once this finishes, usually within a minute.",
    );
    expect(seen).toEqual({ status: "reading", hasData: true });

    const before = connectionCalls();
    await flush(SCHEMA_POLL_MS);
    expect(connectionCalls()).toBe(before + 1);
    // The roster is asked again on each tick, which is how the questions move
    // from the generic three to the workspace's own tables without a reload.
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["agents"] });

    connections = [{ id: "c1", description: "Orders and customers of a grocery chain." }];
    await flush(SCHEMA_POLL_MS);
    expect(screen.getByRole("status").textContent).toBe("Ready. Try one of the questions below.");
    expect(seen.status).toBe("ready");

    const settled = connectionCalls();
    await flush(SCHEMA_POLL_MS * 4);
    expect(connectionCalls()).toBe(settled);
    expect(screen.getByRole("status").textContent).toBe("Ready. Try one of the questions below.");
  });

  it("turns to the timeout copy after five minutes and stops polling (AC5, Q9)", async () => {
    connections = [{ id: "c1", description: "Done." }, { id: "c2", description: "" }];
    await mount(true);
    expect(seen.status).toBe("reading");

    await flush(SCHEMA_WAIT_MS);
    expect(screen.getByRole("status").textContent).toBe(
      "This is taking longer than usual. You can ask now; answers may be less precise until it finishes.",
    );
    expect(seen.status).toBe("timeout");

    const stopped = connectionCalls();
    await flush(SCHEMA_POLL_MS * 4);
    expect(connectionCalls()).toBe(stopped);
  });

  it("says nothing when every source was described before this visit (AC8)", async () => {
    connections = [{ id: "c1", description: "A described warehouse." }];
    await mount(true);
    expect(seen).toEqual({ status: "described", hasData: true });
    expect(document.body.textContent).toBe("");
    const once = connectionCalls();
    await flush(SCHEMA_POLL_MS * 3);
    expect(connectionCalls()).toBe(once);
  });
});
