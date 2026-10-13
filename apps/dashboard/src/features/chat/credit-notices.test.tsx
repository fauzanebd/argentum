// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The way out of a credit notice (RIZ-83 R20, AC16): an admin is offered Top
 * up under the low-credit banner and Top up credit under the 402 refusal; a
 * member sees today's text with no link, and the refusal's own sentence is the
 * server's, unchanged.
 */

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
    get: () => Promise.reject(new Error("not under test")),
    post: (...args: unknown[]) => post(...args),
  },
}));
vi.mock("./use-thread-stream", () => ({ useThreadStream: () => undefined }));

import { useAuthStore } from "@/store/auth";
import { ChatPage } from "./chat-page";
import { BudgetWarningBanner } from "./credit-notices";

const REFUSAL =
  "This workspace has used all of its Argentum credits, so I can't run that right now. Ask an admin to top up the balance — current usage is on the Usage page in the dashboard.";

const WARNING = {
  verdict: "warning",
  balance_micro_usd: 720_000,
  grant_micro_usd: 5_000_000,
  remaining_pct: 14,
  byo_llm: false,
  enforced: true,
} as const;

function asRole(role: "admin" | "member") {
  useAuthStore.setState({
    accessToken: "t",
    user: { id: "u-1", company_id: "co-1", email: "dina@tokosegar.example", role },
  });
}

beforeEach(() => post.mockReset());

/** The send is refused; anything else a signed-in page posts succeeds. */
function refuseChat(status: number, error: string) {
  post.mockImplementation((path: string) =>
    path === "/chat"
      ? Promise.reject(
          Object.assign(new Error(`Request failed with status code ${status}`), {
            response: { status, data: { error } },
          }),
        )
      : Promise.resolve({ data: {} }),
  );
}

describe("low-credit banner", () => {
  it("ends with Top up for an admin", () => {
    render(<BudgetWarningBanner warning={WARNING} onDismiss={() => {}} canTopUp />);
    const banner = screen.getByRole("status");
    expect(banner).toHaveTextContent("14% of your Argentum credit is left");
    expect(banner).toHaveTextContent(
      "— $0.72 remaining. Top up before it runs out to keep chat, scheduled reports and the API working.",
    );
    expect(screen.getByRole("link", { name: "Top up" })).toHaveAttribute("href", "/top-up");
  });

  it("is today's text with no link for a member", () => {
    render(<BudgetWarningBanner warning={WARNING} onDismiss={() => {}} />);
    expect(screen.getByRole("status")).toHaveTextContent("Top up before it runs out");
    expect(screen.queryByRole("link")).toBeNull();
  });
});

describe("402 refusal", () => {
  function sendAndRefuse() {
    refuseChat(402, REFUSAL);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <ChatPage />
      </QueryClientProvider>,
    );
    fireEvent.change(screen.getByPlaceholderText("Ask about your business…"), {
      target: { value: "How many orders this week?" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
  }

  it("ends with Top up credit for an admin, the sentence unchanged", async () => {
    asRole("admin");
    sendAndRefuse();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(`${REFUSAL} Top up credit`);
    expect(screen.getByRole("link", { name: "Top up credit" })).toHaveAttribute("href", "/top-up");
  });

  it("is the sentence alone for a member", async () => {
    asRole("member");
    sendAndRefuse();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(REFUSAL);
    expect(screen.queryByRole("link", { name: "Top up credit" })).toBeNull();
  });

  it("offers no top-up for a refusal that is not about credit", async () => {
    asRole("admin");
    refuseChat(429, "Too many requests.");
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <ChatPage />
      </QueryClientProvider>,
    );
    fireEvent.change(screen.getByPlaceholderText("Ask about your business…"), { target: { value: "hi" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many requests.");
    expect(screen.queryByRole("link", { name: "Top up credit" })).toBeNull();
  });
});
