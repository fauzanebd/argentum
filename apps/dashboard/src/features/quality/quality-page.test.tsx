// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

/**
 * Answer quality (T-Q16).
 *
 * The three properties worth holding are all about the page telling the truth
 * when it has nothing to show — because for the month before this screen
 * existed, the *product* had the same problem in a louder form: the route was
 * served, nothing read it, and the absence looked exactly like "nobody has
 * complained".
 */

let isAdmin = true;
const list = vi.fn();
const summary = vi.fn();

vi.mock("@/store/auth", () => ({ useIsAdmin: () => isAdmin }));
vi.mock("./use-feedback", () => ({
  FEEDBACK_PAGE_SIZE: 50,
  useFeedbackList: (...a: unknown[]) => list(...a),
  useFeedbackSummary: (...a: unknown[]) => summary(...a),
}));
// The coverage panel (T-F4) fetches its own data, so it is stubbed out here —
// without this every test in the file dies on "No QueryClient set", which is a
// failure about the harness rather than about the page. It has its own test
// beside this one, against the view rather than the container.
vi.mock("./metric-coverage-panel", () => ({ MetricCoveragePanel: () => null }));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, params }: { children: unknown; params?: { threadId: string } }) => (
    <a data-testid="thread-link" href={params ? `/chat/${params.threadId}` : "#"}>
      {children as never}
    </a>
  ),
}));

import { QualityPage } from "./quality-page";

function verdict(over: Record<string, unknown> = {}) {
  return {
    id: "fb-1",
    company_id: "co-1",
    thread_id: "thr-9",
    message_id: "msg-4",
    rating: -1,
    reason: "counted line items, not orders",
    actor_kind: "user",
    actor_ref: "u-1",
    created_at: "2026-09-10T04:00:00Z",
    updated_at: "2026-09-10T04:00:00Z",
    question: "how many orders last month?",
    answer: "You had 4,812 orders in August.",
    ...over,
  };
}

// A member gets no data at all from either route — both are RoleAdmin in
// policy.go. Four empty panels would read as "nobody has ever complained",
// which is the one thing this page must never imply when it does not know.
it("tells a member why the page is empty instead of showing an empty page", () => {
  isAdmin = false;
  list.mockReturnValue({ data: undefined, isLoading: false });
  summary.mockReturnValue({ data: undefined });

  render(<QualityPage />);

  expect(screen.getByText(/visible to admins/i)).toBeTruthy();
  expect(screen.queryByText(/Nothing has been marked wrong/i)).toBeNull();
  isAdmin = true;
});

// "Nothing was marked wrong" and "nothing was rated" are different claims and
// only one of them is good news.
it("distinguishes an unrated product from an unproblematic one", () => {
  summary.mockReturnValue({ data: { rated: 0, up: 0, down: 0, down_rate: 0 } });

  list.mockReturnValue({ data: { feedback: [], only_negative: true }, isLoading: false });
  const { unmount } = render(<QualityPage />);
  expect(screen.getByText(/Nothing has been marked wrong yet/i)).toBeTruthy();
  unmount();

  list.mockReturnValue({ data: { feedback: [], only_negative: false }, isLoading: false });
  render(<QualityPage />);
  expect(screen.getByText(/Nothing has been rated yet/i)).toBeTruthy();
});

// The down rate is over *rated* answers. With nothing rated there is no rate,
// and printing 0% would claim a clean record the product has not earned.
it("shows no percentage when nothing has been rated", () => {
  summary.mockReturnValue({ data: { rated: 0, up: 0, down: 0, down_rate: 0 } });
  list.mockReturnValue({ data: { feedback: [], only_negative: true }, isLoading: false });

  render(<QualityPage />);

  expect(screen.getByText("—")).toBeTruthy();
  expect(screen.queryByText("0%")).toBeNull();
});

// The row carries the turn, which is the whole reason the route was widened:
// a list of message ids is unactionable, and that is why nothing read the
// original one.
it("puts the reason, the question and a way into the thread on the row", () => {
  summary.mockReturnValue({ data: { rated: 12, up: 9, down: 3, down_rate: 0.25 } });
  list.mockReturnValue({ data: { feedback: [verdict()], only_negative: true }, isLoading: false });

  render(<QualityPage />);

  expect(screen.getByText(/counted line items, not orders/)).toBeTruthy();
  expect(screen.getByText(/how many orders last month/)).toBeTruthy();
  expect(screen.getByText(/4,812 orders in August/)).toBeTruthy();
  expect(screen.getByTestId("thread-link").getAttribute("href")).toBe("/chat/thr-9");
  expect(screen.getByText("25%")).toBeTruthy();
});

// A verdict with no written reason is still a verdict and must still appear —
// the row says so rather than rendering a blank line.
it("keeps a verdict that came with no reason", () => {
  summary.mockReturnValue({ data: { rated: 1, up: 0, down: 1, down_rate: 1 } });
  list.mockReturnValue({
    data: { feedback: [verdict({ reason: "" })], only_negative: true },
    isLoading: false,
  });

  render(<QualityPage />);

  expect(screen.getByText(/No reason given/i)).toBeTruthy();
});
