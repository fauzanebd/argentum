// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Top-ups to review (RIZ-83 R21, AC17, AC20, AC28): the queue, the two-step
 * approve, the inline reject, and the one sentence a non-operator gets.
 */

const get = vi.fn();
const post = vi.fn();
const toast = vi.fn();

vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
  },
}));
vi.mock("@/hooks/use-toast", () => ({ useToast: () => ({ toast }) }));

import { OperatorTopupsPage } from "./operator-topups-page";

const row = (over: Record<string, unknown>) => ({
  id: "t-1",
  status: "in_review",
  amount_idr: 100000,
  unique_code: 347,
  expected_amount_idr: 100347,
  credit_micro_usd: 5763831,
  payee_id: "bri",
  payee_label: "BRI",
  proof_uploaded_at: new Date(2026, 9, 13, 14, 32).toISOString(),
  expires_at: "",
  created_at: new Date(2026, 9, 13, 14, 20).toISOString(),
  reviewed_at: null,
  reject_reason: null,
  company_name: "Toko Segar",
  requested_by_email: "dina@tokosegar.example",
  reviewed_by_email: null,
  fx_source: "market",
  fx_market_idr_per_usd_e4: 165234000,
  fx_rate_date: "2026-10-12",
  fx_spread_bp: 500,
  fx_effective_idr_per_usd_e4: 173495700,
  duplicate_of: null,
  ...over,
});

let review: unknown[];
let reviewed: unknown[];
let failWith: unknown;

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OperatorTopupsPage />
    </QueryClientProvider>,
  );
}

function apiError(status: number, error: string) {
  return Object.assign(new Error(`Request failed with status code ${status}`), {
    response: { status, data: { error } },
  });
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  toast.mockReset();
  failWith = null;
  review = [];
  reviewed = [];
  get.mockImplementation((path: string, cfg?: { params?: { view?: string } }) => {
    if (failWith) return Promise.reject(failWith);
    if (path === "/operator/topups") {
      return Promise.resolve({ data: { topups: cfg?.params?.view === "reviewed" ? reviewed : review } });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
});

describe("Top-ups to review", () => {
  it("tells a non-operator, company admins included, that it is not for them (AC7)", async () => {
    failWith = apiError(403, "operators only");
    mount();
    expect(await screen.findByText("This page is for Argentum operators.")).toBeInTheDocument();
    expect(screen.queryByText(/Match each one/)).toBeNull();
  });

  it("says when there is nothing to review, and keeps the Reviewed table", async () => {
    reviewed = [
      row({ id: "r-1", status: "approved", reviewed_at: new Date(2026, 9, 12, 19, 2).toISOString(), reviewed_by_email: "owner@example.com", company_name: "Kopi Senja" }),
      row({ id: "r-2", status: "rejected", reviewed_at: new Date(2026, 8, 29, 8, 47).toISOString(), reject_reason: "No transfer of Rp 50.902 reached any of our accounts." }),
    ];
    mount();
    expect(await screen.findByText("Nothing to review.")).toBeInTheDocument();
    expect(screen.getByText("New top-ups show up here when a customer uploads proof of payment.")).toBeInTheDocument();
    expect(screen.getByText("The last 50 decisions.")).toBeInTheDocument();
    expect(await screen.findByText("by owner@example.com")).toBeInTheDocument();
    expect(screen.getByText("No transfer of Rp 50.902 reached any of our accounts.")).toBeInTheDocument();
    expect(screen.getByText("Added")).toBeInTheDocument();
  });

  it("shows the market rate, the fallback badge and a duplicate image (AC17, AC28)", async () => {
    review = [
      row({}),
      row({
        id: "t-2", company_name: "Bengkel Maju", payee_label: "GoPay", expected_amount_idr: 250583, credit_micro_usd: 13880000,
        fx_source: "fallback", fx_market_idr_per_usd_e4: null, fx_rate_date: null, fx_spread_bp: null, fx_effective_idr_per_usd_e4: 180000000,
      }),
      row({
        id: "t-3", company_name: "Apotek Sehat", payee_label: "Jenius", expected_amount_idr: 50019,
        duplicate_of: { company_name: "Kopi Senja", expected_amount_idr: 250140, created_at: new Date(2026, 9, 12, 10, 0).toISOString() },
      }),
    ];
    mount();
    expect(await screen.findByText("Rp 100.347")).toBeInTheDocument();
    expect(screen.getByText("Rate (IDR per $1)")).toBeInTheDocument();
    expect(screen.getAllByText("16.523,40").length).toBe(2);
    expect(screen.getAllByText("market, ECB 12 Oct").length).toBe(2);
    expect(screen.getAllByText("17.349,57").length).toBe(2);
    expect(screen.getByText("Fallback")).toBeInTheDocument();
    expect(screen.getByText("18.000,00")).toBeInTheDocument();
    expect(screen.getByText("no market rate")).toBeInTheDocument();
    expect(screen.getByText("Same image as a Kopi Senja top-up (Rp 250.140, 12 Oct).")).toBeInTheDocument();
    expect(screen.getByText("GoPay")).toBeInTheDocument();
    // A flag, not a block: the duplicate can still be decided.
    const dupRow = screen.getByText("Apotek Sehat").closest("tr") as HTMLElement;
    expect(within(dupRow).getByRole("button", { name: "Approve" })).toBeEnabled();
  });

  it("approves in two steps and says what was added (AC20)", async () => {
    review = [row({})];
    post.mockResolvedValueOnce({ data: { topup: row({ status: "approved" }), balance_micro_usd: 5883831 } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm: add $5.76" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/operator/topups/t-1/approve"));
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ title: "Added $5.76 to Toko Segar." }));
  });

  it("rejects inline with a reason", async () => {
    review = [row({})];
    post.mockResolvedValueOnce({ data: row({ status: "rejected" }) });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Reject" }));
    const box = screen.getByLabelText("Reason the customer will see");
    const send = screen.getByRole("button", { name: "Reject top-up" });
    expect(send).toBeDisabled();
    fireEvent.change(box, { target: { value: "No transfer of Rp 100.347 reached our BRI account." } });
    fireEvent.click(send);
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/operator/topups/t-1/reject", {
        reason: "No transfer of Rp 100.347 reached our BRI account.",
      }),
    );
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ title: "Rejected the top-up from Toko Segar." }));
  });

  it("shows a 409 as the API's sentence and refreshes the list (AC9)", async () => {
    review = [row({})];
    post.mockRejectedValueOnce(apiError(409, "This top-up was already reviewed."));
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    const gets = get.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Confirm: add $5.76" }));
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith({ title: "This top-up was already reviewed.", variant: "destructive" }),
    );
    await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(gets));
  });

  it("opens the proof's presigned link in a new tab", async () => {
    review = [row({})];
    const tab = { opener: {}, location: { href: "" }, close: vi.fn() };
    const open = vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    get.mockImplementation((path: string, cfg?: { params?: { view?: string } }) => {
      if (path === "/operator/topups/t-1/proof") {
        return Promise.resolve({ data: { url: "https://storage.example/topups/x.png?sig=1", content_type: "image/png", expires_in: 300 } });
      }
      return Promise.resolve({ data: { topups: cfg?.params?.view === "reviewed" ? [] : review } });
    });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "View proof" }));
    await waitFor(() => expect(tab.location.href).toBe("https://storage.example/topups/x.png?sig=1"));
    expect(open).toHaveBeenCalledWith("", "_blank");
    expect(tab.opener).toBeNull();
    open.mockRestore();
  });
});
