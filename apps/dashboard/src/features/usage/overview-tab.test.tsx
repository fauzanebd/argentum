// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Usage → Overview's credit card (RIZ-79 R7, R8): a one-time free credit is
 * captioned as one, a negative balance is shown as $0.00, and a workspace
 * that has used its credit is told what that stops.
 */

const USED_UP = "Free credit used up. New chats, scheduled reports and API calls are paused.";

let credits: () => Promise<unknown>;

vi.mock("@/lib/api", () => ({
  api: {
    get: (path: string) => {
      if (path === "/usage/summary") {
        return Promise.resolve({
          data: {
            from: "2026-10-01T00:00:00Z",
            to: "2026-11-01T00:00:00Z",
            // Not zero, so "$0.00" on this page can only be the balance.
            total_cost_usd: 0.53,
            total_tokens_in: 0,
            total_tokens_out: 0,
            event_counts: {},
            cost_by_event_type_usd: {},
            cost_by_model_usd: {},
          },
        });
      }
      if (path === "/usage/credits") return credits();
      return Promise.reject(new Error(`unexpected GET ${path}`));
    },
  },
}));

import { OverviewTab } from "./overview-tab";

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OverviewTab />
    </QueryClientProvider>,
  );
}

const withBalance = (balance: number, grant: number) => () =>
  Promise.resolve({
    data: { company_id: "co-1", balance_micro_usd: balance, monthly_grant_micro_usd: grant, updated_at: "" },
  });

/** The bar's fill is the only element whose width the card sets inline. */
function barWidth(container: HTMLElement): string | null {
  const fill = container.querySelector<HTMLElement>("[style*='width']");
  return fill ? fill.style.width : null;
}

beforeEach(() => {
  credits = withBalance(500_000, 500_000);
});

describe("credit card", () => {
  it("shows a new workspace's full free credit (AC4)", async () => {
    const { container } = mount();
    expect(await screen.findByText("of $0.50 free credit")).toBeInTheDocument();
    expect(screen.getByText("$0.50")).toBeInTheDocument();
    expect(barWidth(container)).toBe("100%");
    expect(screen.queryByText(USED_UP)).toBeNull();
    expect(screen.queryByText(/monthly grant/)).toBeNull();
    expect(screen.getByText("No usage recorded yet.")).toBeInTheDocument();
  });

  it("shows a part-spent credit against its grant", async () => {
    credits = withBalance(310_000, 500_000);
    const { container } = mount();
    expect(await screen.findByText("$0.31")).toBeInTheDocument();
    expect(screen.getByText("of $0.50 free credit")).toBeInTheDocument();
    expect(barWidth(container)).toBe("62%");
    expect(screen.queryByText(USED_UP)).toBeNull();
  });

  it("shows an overdrawn balance as $0.00, an empty bar and the used-up line (AC5)", async () => {
    credits = withBalance(-30_000, 500_000);
    const { container } = mount();
    expect(await screen.findByText("$0.00")).toBeInTheDocument();
    expect(screen.queryByText(/-\$/)).toBeNull();
    expect(screen.getByText("of $0.50 free credit")).toBeInTheDocument();
    expect(barWidth(container)).toBe("0%");
    const line = screen.getByText(USED_UP);
    expect(line.className).toContain("text-destructive");
  });

  it("says used up at exactly zero too", async () => {
    credits = withBalance(0, 500_000);
    mount();
    expect(await screen.findByText(USED_UP)).toBeInTheDocument();
  });

  it("captions a legacy $25 grant as free credit as well (AC8)", async () => {
    credits = withBalance(18_000_000, 25_000_000);
    mount();
    expect(await screen.findByText("of $25.00 free credit")).toBeInTheDocument();
    expect(screen.getByText("$18.00")).toBeInTheDocument();
  });

  it("does not call a balance with no grant used up", async () => {
    // What /usage/credits answers with enforcement off for a company never
    // credited: there is nothing to have used up, and no bar to draw.
    credits = () => Promise.resolve({ data: { company_id: "co-1", balance_micro_usd: 0 } });
    mount();
    expect(await screen.findByText("$0.00")).toBeInTheDocument();
    expect(screen.queryByText(USED_UP)).toBeNull();
    expect(screen.queryByText(/free credit/)).toBeNull();
  });

  it("shows a dash and no bar when the credits call fails", async () => {
    credits = () => Promise.reject(new Error("Request failed with status code 500"));
    const { container } = mount();
    expect(await screen.findByText("Credit balance")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(barWidth(container)).toBeNull();
  });
});
