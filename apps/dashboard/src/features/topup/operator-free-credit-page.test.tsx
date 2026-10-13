// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Free credit to approve (RIZ-89 R10, R11, AC5–AC8, AC16): the queue and its
 * signals, two-click approve and reject with their toasts, a 409, the empty
 * queue, an API error, the tabs and the one sentence a non-operator gets.
 */

const get = vi.fn();
const post = vi.fn();
const toast = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children, ...rest }: { to: string; children?: React.ReactNode }) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
  },
}));
vi.mock("@/hooks/use-toast", () => ({ useToast: () => ({ toast }) }));

import { OperatorFreeCreditPage } from "./operator-free-credit-page";
import { sameIpSignal } from "./free-credit";

const row = (over: Record<string, unknown>) => ({
  id: "g-1",
  company_id: "co-1",
  status: "pending",
  amount_micro_usd: 500000,
  created_at: new Date(2026, 9, 13, 14, 32).toISOString(),
  reviewed_at: null,
  company_name: "Toko Segar",
  requested_by_email: "dina@tokosegar.id",
  reviewed_by_email: "",
  has_data_source: true,
  same_ip_signups_7d: 0,
  ...over,
});

let review: unknown[];
let reviewed: unknown[];
let failWith: unknown;

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OperatorFreeCreditPage />
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
    if (path === "/operator/free-credit") {
      return Promise.resolve({ data: { requests: cfg?.params?.view === "reviewed" ? reviewed : review } });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
});

describe("Free credit to approve", () => {
  it("tells a non-operator it is not for them, with no tabs (AC8)", async () => {
    failWith = apiError(403, "operators only");
    mount();
    expect(await screen.findByText("This page is for Argentum operators.")).toBeInTheDocument();
    expect(screen.getByText("Free credit to approve")).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByText(/Every new workspace/)).toBeNull();
  });

  it("says when there is nothing to approve, and keeps the Reviewed table (AC16)", async () => {
    reviewed = [
      row({ id: "r-1", status: "approved", company_name: "Kopi Senja", reviewed_at: new Date(2026, 9, 13, 9, 12).toISOString(), reviewed_by_email: "operator@argentum.id" }),
      row({ id: "r-2", status: "rejected", company_name: "test123", reviewed_at: new Date(2026, 9, 12, 21, 40).toISOString(), reviewed_by_email: "operator@argentum.id" }),
    ];
    mount();
    expect(await screen.findByText("Nothing to approve.")).toBeInTheDocument();
    expect(screen.getByText("New workspaces show up here as soon as they sign up.")).toBeInTheDocument();
    expect(screen.getByText("The last 50 decisions.")).toBeInTheDocument();
    expect(await screen.findByText("Added")).toBeInTheDocument();
    expect(screen.getByText("Not approved")).toBeInTheDocument();
    expect(screen.getAllByText("by operator@argentum.id").length).toBe(2);
    expect(screen.getByText("13 Oct 2026, 09:12")).toBeInTheDocument();
    // The tab carries no count when nothing waits.
    expect(screen.getByRole("link", { name: "Free credit" })).toHaveAttribute("href", "/operator/free-credit");
  });

  it("shows the API's sentence when the list fails", async () => {
    failWith = apiError(500, "the free-credit request could not be updated");
    mount();
    expect(await screen.findByText("the free-credit request could not be updated")).toBeInTheDocument();
  });

  it("shows the queue with its signals and the pending count on the tab (R11, Q21)", async () => {
    review = [
      row({}),
      row({ id: "g-2", company_name: "Apotek Sehat", requested_by_email: "wulan@apoteksehat.id", has_data_source: false }),
      row({ id: "g-3", company_name: "abc store", requested_by_email: "x9k2m@mailinator.com", has_data_source: false, same_ip_signups_7d: 2 }),
    ];
    mount();
    expect(await screen.findByText("Toko Segar")).toBeInTheDocument();
    expect(screen.getAllByText("13 Oct 2026, 14:32").length).toBe(3);
    expect(screen.getByText("dina@tokosegar.id")).toBeInTheDocument();
    expect(screen.getByText("Data source connected")).toBeInTheDocument();
    expect(screen.getAllByText("No data source yet").length).toBe(2);
    expect(screen.getByText("2 other sign-ups from this IP in 7 days")).toBeInTheDocument();
    expect(screen.getAllByText("$0.50").length).toBe(3);
    const queue = screen.getAllByRole("table")[0];
    for (const name of ["Signed up", "Workspace", "Signals", "Free credit"]) {
      expect(within(queue).getByRole("columnheader", { name })).toBeInTheDocument();
    }
    const tabs = screen.getByRole("navigation", { name: "Operator queues" });
    expect(within(tabs).getByRole("link", { name: "Top-ups" })).toHaveAttribute("href", "/operator/top-ups");
    expect(within(tabs).getByRole("link", { name: "Free credit 3" })).toBeInTheDocument();
  });

  it("approves in two steps and says what was added (AC5)", async () => {
    review = [row({})];
    post.mockResolvedValueOnce({ data: { request: row({ status: "approved" }), added_micro_usd: 500000, balance_micro_usd: 500000 } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm: add $0.50" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/operator/free-credit/g-1/approve"));
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ title: "Added $0.50 free credit to Toko Segar." }));
  });

  it("says nothing was added to a workspace that already had free credit", async () => {
    review = [row({})];
    post.mockResolvedValueOnce({ data: { request: row({ status: "approved" }), added_micro_usd: 0, balance_micro_usd: 310000 } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm: add $0.50" }));
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith({ title: "Toko Segar already had free credit; nothing was added." }),
    );
  });

  it("rejects in two steps, and Cancel goes back (AC7)", async () => {
    review = [row({})];
    post.mockResolvedValueOnce({ data: { request: row({ status: "rejected" }) } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Reject" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm: reject" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/operator/free-credit/g-1/reject"));
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ title: "Rejected free credit for Toko Segar." }));
  });

  it("shows a 409 as the API's sentence and refreshes the list (AC6)", async () => {
    review = [row({})];
    post.mockRejectedValueOnce(apiError(409, "This request was already reviewed."));
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    const gets = get.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Confirm: add $0.50" }));
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith({ title: "This request was already reviewed.", variant: "destructive" }),
    );
    await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(gets));
  });
});

describe("sameIpSignal", () => {
  it("counts in words, and says nothing for none", () => {
    expect(sameIpSignal(0)).toBeNull();
    expect(sameIpSignal(1)).toBe("1 other sign-up from this IP in 7 days");
    expect(sameIpSignal(2)).toBe("2 other sign-ups from this IP in 7 days");
  });
});
