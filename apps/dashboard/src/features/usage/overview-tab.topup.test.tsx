// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Usage → Overview after a top-up (RIZ-83 R19, AC15): the caption says where
 * the bar measures from, the used-up line drops "Free", and Top up is offered
 * to admins only. A workspace that never topped up reads exactly as RIZ-79
 * left it — overview-tab.test.tsx holds that, unchanged.
 */

let credits: unknown;

vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children, ...rest }: { to: string; children?: React.ReactNode }) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/lib/api", () => ({
  api: {
    get: (path: string) => {
      if (path === "/usage/summary") {
        return Promise.resolve({
          data: {
            from: "2026-10-01T00:00:00Z",
            to: "2026-11-01T00:00:00Z",
            total_cost_usd: 1.84,
            total_tokens_in: 0,
            total_tokens_out: 0,
            event_counts: {},
            cost_by_event_type_usd: {},
            cost_by_model_usd: {},
          },
        });
      }
      if (path === "/usage/credits") return Promise.resolve({ data: credits });
      return Promise.reject(new Error(`unexpected GET ${path}`));
    },
  },
}));

import { useAuthStore } from "@/store/auth";
import { OverviewTab } from "./overview-tab";

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OverviewTab />
    </QueryClientProvider>,
  );
}

function asRole(role: "admin" | "member") {
  useAuthStore.setState({
    accessToken: "t",
    user: { id: "u-1", company_id: "co-1", email: "dina@tokosegar.example", role },
  });
}

const toppedUp = (balance: number) => ({
  company_id: "co-1",
  balance_micro_usd: balance,
  monthly_grant_micro_usd: 5_880_000,
  updated_at: "",
  last_topup_at: "2026-10-13T08:02:11Z",
});

beforeEach(() => {
  credits = toppedUp(5_630_000);
});

describe("credit card after a top-up", () => {
  it("captions the grant as the last top-up and offers Top up to an admin", async () => {
    asRole("admin");
    mount();
    expect(await screen.findByText("of $5.88 at your last top-up")).toBeInTheDocument();
    expect(screen.getByText("$5.63")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Top up" })).toHaveAttribute("href", "/top-up");
    expect(screen.queryByText(/free credit/)).toBeNull();
  });

  it("says Credit used up, not Free credit used up, at zero", async () => {
    asRole("admin");
    credits = toppedUp(0);
    mount();
    expect(
      await screen.findByText("Credit used up. New chats, scheduled reports and API calls are paused."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Free credit used up/)).toBeNull();
  });

  it("offers a member no Top up", async () => {
    asRole("member");
    mount();
    expect(await screen.findByText("of $5.88 at your last top-up")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Top up" })).toBeNull();
  });

  it("reads exactly as before for a workspace that never topped up", async () => {
    asRole("admin");
    credits = { company_id: "co-1", balance_micro_usd: 0, monthly_grant_micro_usd: 500_000, updated_at: "", last_topup_at: null };
    mount();
    expect(await screen.findByText("of $0.50 free credit")).toBeInTheDocument();
    expect(
      screen.getByText("Free credit used up. New chats, scheduled reports and API calls are paused."),
    ).toBeInTheDocument();
  });
});
