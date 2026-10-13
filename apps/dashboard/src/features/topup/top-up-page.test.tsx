// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Top up credit (RIZ-83 R17, R18): the page's states, its copy, and that the
 * browser refuses what the server would before anything is sent.
 *
 * Placeholder accounts only — the real ones are deploy config.
 */

const get = vi.fn();
const post = vi.fn();

vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
  },
}));

import { useAuthStore } from "@/store/auth";
import { TopUpPage } from "./top-up-page";

const OPTIONS = {
  enabled: true,
  min_idr: 20000,
  max_idr: 5000000,
  step_idr: 1000,
  presets_idr: [50000, 100000, 250000],
  preview_idr_per_usd_e4: 168000000, // 16.000 + 5%
  expires_after_hours: 48,
  payees: [
    { id: "bri", label: "BRI", account_number: "123456789012345", account_name: "Payee Name" },
    { id: "gopay", label: "GoPay", account_number: "081200000000", account_name: "Payee Name" },
  ],
};

const CREDITS = {
  company_id: "co-1",
  balance_micro_usd: 120000,
  monthly_grant_micro_usd: 500000,
  updated_at: "",
  last_topup_at: null,
};

const base = {
  amount_idr: 100000,
  unique_code: 347,
  expected_amount_idr: 100347,
  credit_micro_usd: 5952380,
  payee_id: null,
  proof_uploaded_at: null,
  expires_at: new Date(2026, 9, 15, 14, 20).toISOString(),
  created_at: new Date(2026, 9, 13, 14, 20).toISOString(),
  reviewed_at: null,
  reject_reason: null,
};

let topups: unknown[];
let options: () => Promise<unknown>;

let credits: unknown;

function routeGets() {
  get.mockImplementation((path: string) => {
    if (path === "/topups/options") return options();
    if (path === "/topups") return Promise.resolve({ data: { topups } });
    if (path === "/usage/credits") return Promise.resolve({ data: credits });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
}

function asRole(role: "admin" | "member") {
  useAuthStore.setState({
    accessToken: "t",
    user: { id: "u-1", company_id: "co-1", email: "dina@tokosegar.example", role },
  });
}

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TopUpPage />
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
  topups = [];
  credits = CREDITS;
  options = () => Promise.resolve({ data: OPTIONS });
  routeGets();
  asRole("admin");
});

describe("who may top up (R18, AC14)", () => {
  it("tells a member to ask an admin and calls nothing", async () => {
    asRole("member");
    mount();
    expect(
      screen.getByText("Only workspace admins can top up credit. Ask an admin on your team."),
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("says not available when top-up is off (AC13)", async () => {
    options = () => Promise.resolve({ data: { ...OPTIONS, enabled: false, payees: [] } });
    mount();
    expect(
      await screen.findByText("Top-up is not available right now. Please try again later."),
    ).toBeInTheDocument();
    expect(screen.queryByText("How much do you want to add?")).toBeNull();
  });

  it("says not available when the options call fails", async () => {
    options = () => Promise.reject(apiError(404, "404 page not found"));
    mount();
    expect(
      await screen.findByText("Top-up is not available right now. Please try again later."),
    ).toBeInTheDocument();
  });
});

describe("choose an amount (R17a, AC22)", () => {
  it("shows the balance, the presets, an empty field and no history", async () => {
    mount();
    expect(await screen.findByText("How much do you want to add?")).toBeInTheDocument();
    expect(screen.getByText("$0.12")).toBeInTheDocument();
    expect(screen.getByText("of $0.50 free credit")).toBeInTheDocument();
    for (const chip of ["Rp 50.000", "Rp 100.000", "Rp 250.000"]) {
      expect(screen.getByRole("button", { name: chip })).toBeInTheDocument();
    }
    expect(screen.getByLabelText("Amount")).toHaveValue("");
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
    expect(screen.getByText("Top-ups are not refundable.")).toBeInTheDocument();
    expect(screen.getByText("No top-ups yet.")).toBeInTheDocument();
    // No rate, spread or fee is ever named to a tenant (AC28).
    expect(screen.queryByText(/16\.000|spread|fee|17\.349/i)).toBeNull();
  });

  it("fills the field from a preset and previews the credit", async () => {
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Rp 100.000" }));
    expect(screen.getByLabelText("Amount")).toHaveValue("100.000");
    const help = screen.getByText(/credit at today's rate/);
    expect(help).toHaveTextContent(
      "You get $5.95 credit at today's rate. Between Rp 20.000 and Rp 5.000.000, in whole thousands.",
    );
    expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
  });

  it("groups digits as you type and previews $8.92 for Rp 150.000 (Q17)", async () => {
    mount();
    const field = await screen.findByLabelText("Amount");
    fireEvent.change(field, { target: { value: "150000" } });
    expect(field).toHaveValue("150.000");
    expect(screen.getByText("$8.92")).toBeInTheDocument();
  });

  it.each([
    ["7500000", "The maximum top-up is Rp 5.000.000."],
    ["19000", "The minimum top-up is Rp 20.000."],
    ["150500", "Enter an amount in whole thousands of rupiah."],
  ])("refuses %s before sending anything", async (typed, message) => {
    mount();
    const field = await screen.findByLabelText("Amount");
    fireEvent.change(field, { target: { value: typed } });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(message);
    expect(alert.className).toContain("text-destructive");
    expect(field).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByText(/You get/)).toBeNull();
    const cont = screen.getByRole("button", { name: "Continue" });
    expect(cont).toBeDisabled();
    fireEvent.click(cont);
    expect(post).not.toHaveBeenCalled();
  });

  it("sends the amount and nothing else, and shows a server refusal the same way", async () => {
    post.mockRejectedValueOnce(apiError(400, "The minimum top-up is Rp 20.000."));
    mount();
    fireEvent.change(await screen.findByLabelText("Amount"), { target: { value: "50000" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/topups", { amount_idr: 50000 }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The minimum top-up is Rp 20.000.");
  });
});

describe("waiting for payment (R17b, R17c)", () => {
  beforeEach(() => {
    topups = [{ ...base, id: "t-1", status: "awaiting_payment" }];
  });

  it("shows the exact amount, the accounts and the upload", async () => {
    mount();
    expect(await screen.findByText("Transfer exactly")).toBeInTheDocument();
    expect(screen.getAllByText("Waiting for payment").length).toBeGreaterThan(0);
    expect(screen.getByText("$5.95 credit")).toBeInTheDocument();
    expect(screen.getByText("347").tagName).toBe("MARK");
    expect(
      screen.getByText(
        "Include the last 3 digits: they are how we match your transfer to this request. Pay by 15 Oct 2026, 14:20 or this request expires.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("1234 5678 9012 345")).toBeInTheDocument();
    expect(screen.getByText("0812 0000 0000")).toBeInTheDocument();
    expect(screen.getByText("Upload proof of payment")).toBeInTheDocument();
    expect(screen.getByText("PNG, JPG, WebP or PDF, up to 5 MB.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit for review" })).toBeDisabled();
    expect(
      screen.getByText(
        "We check every top-up against our bank and GoPay statements. A screenshot on its own does not add credit.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("How much do you want to add?")).toBeNull();
  });

  it("copies digits only", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Copy amount" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("100347"));
    fireEvent.click(screen.getAllByRole("button", { name: "Copy" })[1]);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("081200000000"));
  });

  it("shows an upload refusal under the drop zone and holds Submit until the file changes (AC5)", async () => {
    post.mockRejectedValueOnce(apiError(415, "Upload a PNG, JPG, WebP or PDF file."));
    mount();
    fireEvent.change(await screen.findByLabelText("Paid to"), { target: { value: "bri" } });
    const file = new File(["not an image"], "mutasi.png", { type: "image/png" });
    fireEvent.change(screen.getByTestId("proof-file"), { target: { files: [file] } });
    expect(screen.getByText("mutasi.png")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "choose another file" })).toBeInTheDocument();
    const submit = screen.getByRole("button", { name: "Submit for review" });
    fireEvent.click(submit);
    expect(await screen.findByRole("alert")).toHaveTextContent("Upload a PNG, JPG, WebP or PDF file.");
    expect(submit).toBeDisabled();
    const [path, form] = post.mock.calls[0];
    expect(path).toBe("/topups/t-1/proof");
    expect((form as FormData).get("payee_id")).toBe("bri");

    const another = new File(["\x89PNG"], "mutasi-2.png", { type: "image/png" });
    fireEvent.change(screen.getByTestId("proof-file"), { target: { files: [another] } });
    expect(submit).toBeEnabled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("refuses a file over 5 MB with the server's sentence, without sending it", async () => {
    mount();
    fireEvent.change(await screen.findByLabelText("Paid to"), { target: { value: "bri" } });
    const big = new File(["x"], "mutasi-bri-13okt.png", { type: "image/png" });
    Object.defineProperty(big, "size", { value: 7.4 * 1024 * 1024 });
    fireEvent.change(screen.getByTestId("proof-file"), { target: { files: [big] } });
    expect(screen.getByText("7.4 MB")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Submit for review" }));
    expect(screen.getByRole("alert")).toHaveTextContent("That file is larger than 5 MB. Upload a smaller screenshot.");
    expect(screen.getByRole("button", { name: "Submit for review" })).toBeDisabled();
    expect(post).not.toHaveBeenCalled();
  });

  it("asks which account was paid before sending", async () => {
    mount();
    const file = new File(["\x89PNG"], "mutasi.png", { type: "image/png" });
    fireEvent.change(await screen.findByTestId("proof-file"), { target: { files: [file] } });
    fireEvent.click(screen.getByRole("button", { name: "Submit for review" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Choose the account you paid to.");
    expect(post).not.toHaveBeenCalled();
  });

  it("cancels the request", async () => {
    post.mockResolvedValueOnce({ data: { ...base, id: "t-1", status: "cancelled" } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Cancel request" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/topups/t-1/cancel"));
  });
});

describe("QA round 1", () => {
  it("F4: a server 400 holds Continue until the amount changes", async () => {
    post.mockRejectedValueOnce(apiError(400, "The minimum top-up is Rp 50.000."));
    mount();
    const field = await screen.findByLabelText("Amount");
    fireEvent.change(field, { target: { value: "30000" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The minimum top-up is Rp 50.000.");
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
    fireEvent.change(field, { target: { value: "60000" } });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
  });

  it("F5: choosing another account after the API refused one frees Submit", async () => {
    topups = [{ ...base, id: "t-1", status: "awaiting_payment" }];
    post.mockRejectedValueOnce(apiError(400, "Choose the account you paid to."));
    mount();
    const select = await screen.findByLabelText("Paid to");
    fireEvent.change(select, { target: { value: "gopay" } });
    const file = new File(["\x89PNG"], "mutasi.png", { type: "image/png" });
    fireEvent.change(screen.getByTestId("proof-file"), { target: { files: [file] } });
    const submit = screen.getByRole("button", { name: "Submit for review" });
    fireEvent.click(submit);
    expect(await screen.findByRole("alert")).toHaveTextContent("Choose the account you paid to.");
    expect(submit).toBeDisabled();
    fireEvent.change(select, { target: { value: "bri" } });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(submit).toBeEnabled();
  });

  it("gives the Paid to placeholder room for its whole sentence", async () => {
    topups = [{ ...base, id: "t-1", status: "awaiting_payment" }];
    mount();
    const select = await screen.findByLabelText("Paid to");
    // w-60 (240 px) clipped "Choose the account you paid to." under the arrow.
    expect(select.className).toContain("w-72");
    expect(select.className).not.toContain("w-60");
  });
});

describe("in review and history (R17d, R17e)", () => {
  it("shows the status card and every status in the history", async () => {
    topups = [
      { ...base, id: "t-5", status: "in_review", payee_id: "bri", proof_uploaded_at: new Date(2026, 9, 13, 14, 32).toISOString() },
      { ...base, id: "t-4", status: "approved", expected_amount_idr: 50211, credit_micro_usd: 2910000 },
      { ...base, id: "t-3", status: "rejected", expected_amount_idr: 50902, reject_reason: "No transfer of Rp 50.902 reached any of our accounts." },
      { ...base, id: "t-2", status: "expired", expected_amount_idr: 250618 },
      { ...base, id: "t-1", status: "cancelled", expected_amount_idr: 100074 },
    ];
    mount();
    expect(await screen.findByText("Rp 100.347 for $5.95 credit")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Proof uploaded 13 Oct 2026, 14:32, paid to BRI. We'll add the credit once we've matched your payment, usually within one working day.",
      ),
    ).toBeInTheDocument();
    const table = screen.getByRole("table");
    for (const label of ["In review", "Added", "Rejected", "Expired", "Cancelled"]) {
      expect(within(table).getByText(label)).toBeInTheDocument();
    }
    expect(within(table).getByText("No transfer of Rp 50.902 reached any of our accounts.")).toBeInTheDocument();
    expect(within(table).getByText("Rp 50.211")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Replace proof" }));
    expect(screen.getByLabelText("Paid to")).toHaveValue("bri");
    expect(screen.getByRole("button", { name: "Submit for review" })).toBeDisabled();
  });
});

// RIZ-89 R14: the balance card says what Usage says while free credit waits —
// topping up is exactly the way out it offers.
describe("balance card while free credit waits", () => {
  it("says the amount is waiting for approval, with no bar", async () => {
    credits = { ...CREDITS, balance_micro_usd: 0, monthly_grant_micro_usd: 0, free_credit_status: "pending", free_credit_micro_usd: 500000 };
    mount();
    expect(await screen.findByText("$0.50 free credit waiting for approval")).toBeInTheDocument();
    expect(
      screen.getByText("Chat, scheduled reports and API calls start as soon as it's approved."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/free credit$/)).toBeNull();
  });

  it("says it was not approved", async () => {
    credits = { ...CREDITS, balance_micro_usd: 0, monthly_grant_micro_usd: 0, free_credit_status: "rejected", free_credit_micro_usd: 500000 };
    mount();
    expect(
      await screen.findByText("Free credit not approved. Top up to use chat, scheduled reports and the API."),
    ).toBeInTheDocument();
  });
});
