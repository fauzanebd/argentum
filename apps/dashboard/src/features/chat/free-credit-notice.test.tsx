// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The new-chat screen while free credit waits for an operator (RIZ-89 R13,
 * AC2, AC7): a notice before anybody types, the top-up way out for an admin,
 * and the server's own sentence on the 402 strip after a send.
 */

const PENDING_REFUSAL =
  "This workspace's free Argentum credit is waiting for approval, so I can't run that yet. Chat starts working as soon as it's approved — or an admin can top up now from the Usage page in the dashboard.";

let credits: () => Promise<unknown>;
const post = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({}),
  useNavigate: () => vi.fn(),
  Link: ({ to, children, ...rest }: { to: string; children?: React.ReactNode }) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/lib/api", () => ({
  api: {
    get: (path: string) => (path === "/usage/credits" ? credits() : Promise.reject(new Error("not under test"))),
    post: (...args: unknown[]) => post(...args),
  },
}));
vi.mock("./use-thread-stream", () => ({ useThreadStream: () => undefined }));

import { useAuthStore } from "@/store/auth";
import { ChatPage } from "./chat-page";
import { FreeCreditNotice } from "./credit-notices";

const waiting = (status: "pending" | "rejected") => ({
  company_id: "co-1",
  balance_micro_usd: 0,
  monthly_grant_micro_usd: 0,
  updated_at: "",
  free_credit_status: status,
  free_credit_micro_usd: 500_000,
});

function asRole(role: "admin" | "member") {
  useAuthStore.setState({
    accessToken: "t",
    user: { id: "u-1", company_id: "co-1", email: "dina@tokosegar.example", role },
  });
}

function mountChat() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ChatPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  post.mockReset();
  credits = () => Promise.resolve({ data: waiting("pending") });
});

describe("FreeCreditNotice", () => {
  it("tells an admin it is waiting and offers Top up to start now", () => {
    render(<FreeCreditNotice credits={waiting("pending")} canTopUp />);
    expect(screen.getByRole("status").textContent).toBe(
      "Your free credit is waiting for approval. We approve the $0.50 free credit for each new workspace by hand. Connect data and set up agents meanwhile — chat starts working as soon as it's approved. Top up to start now",
    );
    expect(screen.getByRole("link", { name: "Top up to start now" })).toHaveAttribute("href", "/top-up");
  });

  it("tells a member the same, with no link", () => {
    render(<FreeCreditNotice credits={waiting("pending")} />);
    expect(screen.getByRole("status").textContent).toBe(
      "Your free credit is waiting for approval. We approve the $0.50 free credit for each new workspace by hand. Chat starts working as soon as it's approved.",
    );
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("says it was not approved and links to top up", () => {
    render(<FreeCreditNotice credits={waiting("rejected")} canTopUp />);
    expect(screen.getByRole("status").textContent).toBe(
      "Your free credit was not approved. Top up to use chat, scheduled reports and the API. Top up credit",
    );
    expect(screen.getByRole("link", { name: "Top up credit" })).toHaveAttribute("href", "/top-up");
  });

  it("is nothing for a workspace with credit, or an API that does not say", () => {
    const { container } = render(
      <FreeCreditNotice
        credits={{ company_id: "co-1", balance_micro_usd: 310_000, monthly_grant_micro_usd: 500_000, updated_at: "" }}
        canTopUp
      />,
    );
    expect(container).toBeEmptyDOMElement();
    expect(render(<FreeCreditNotice credits={undefined} />).container).toBeEmptyDOMElement();
  });
});

describe("new chat", () => {
  it("shows the notice before anybody types (AC2)", async () => {
    asRole("admin");
    mountChat();
    expect(await screen.findByText("Your free credit is waiting for approval.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Top up to start now" })).toBeInTheDocument();
  });

  it("shows nothing while the balance cannot be read", async () => {
    asRole("admin");
    credits = () => Promise.reject(new Error("boom"));
    mountChat();
    await screen.findByPlaceholderText("Ask about your business…");
    expect(screen.queryByText(/free credit/)).toBeNull();
  });

  it("shows the server's pending sentence on the strip after a send", async () => {
    asRole("admin");
    post.mockImplementation((path: string) =>
      path === "/chat"
        ? Promise.reject(
            Object.assign(new Error("Request failed with status code 402"), {
              response: { status: 402, data: { error: PENDING_REFUSAL } },
            }),
          )
        : Promise.resolve({ data: {} }),
    );
    mountChat();
    await screen.findByText("Your free credit is waiting for approval.");
    fireEvent.change(screen.getByPlaceholderText("Ask about your business…"), {
      target: { value: "What were my top 5 products last month?" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(`${PENDING_REFUSAL} Top up credit`);
  });
});
