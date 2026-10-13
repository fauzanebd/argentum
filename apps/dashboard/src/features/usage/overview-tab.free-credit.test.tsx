// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Usage → Overview and the top-up page while free credit waits for an operator
 * (RIZ-89 R14, AC2, AC7): $0.00, no bar, and the lines that say why. A
 * workspace with a grant reads as it did — overview-tab.test.tsx and
 * overview-tab.topup.test.tsx hold that, unchanged.
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
            total_cost_usd: 0,
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
import { freeCreditLines } from "./credit-copy";

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OverviewTab />
    </QueryClientProvider>,
  );
}

const waiting = (status: "pending" | "rejected") => ({
  company_id: "co-1",
  balance_micro_usd: 0,
  monthly_grant_micro_usd: 0,
  updated_at: "",
  last_topup_at: null,
  free_credit_status: status,
  free_credit_micro_usd: 500_000,
});

beforeEach(() => {
  useAuthStore.setState({
    accessToken: "t",
    user: { id: "u-1", company_id: "co-1", email: "dina@tokosegar.example", role: "admin" },
  });
});

describe("credit card while free credit waits", () => {
  it("says the amount is waiting for approval, with no bar (AC2)", async () => {
    credits = waiting("pending");
    const { container } = mount();
    expect(await screen.findByText("$0.50 free credit waiting for approval")).toBeInTheDocument();
    expect(
      screen.getByText("Chat, scheduled reports and API calls start as soon as it's approved."),
    ).toBeInTheDocument();
    expect(screen.getAllByText("$0.00").length).toBeGreaterThan(0);
    expect(screen.queryByText(/of \$/)).toBeNull();
    expect(container.querySelector(".bg-primary.h-full")).toBeNull();
    expect(screen.getByRole("link", { name: "Top up" })).toHaveAttribute("href", "/top-up");
  });

  it("says it was not approved and points to top-up (AC7)", async () => {
    credits = waiting("rejected");
    mount();
    expect(
      await screen.findByText("Free credit not approved. Top up to use chat, scheduled reports and the API."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/waiting for approval/)).toBeNull();
  });
});

describe("freeCreditLines", () => {
  it("is null for a workspace with a grant and for an API older than RIZ-89", () => {
    expect(freeCreditLines(null)).toBeNull();
    expect(
      freeCreditLines({ company_id: "c", balance_micro_usd: 1, monthly_grant_micro_usd: 1, updated_at: "" }),
    ).toBeNull();
  });
});
