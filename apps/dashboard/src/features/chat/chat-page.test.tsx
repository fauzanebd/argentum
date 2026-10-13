// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RIZ-79 QA B2: a send refused before any thread exists is shown. The thread
 * view always drew `error`; the new-chat screen set it and drew nothing, so a
 * workspace out of free credit pressed Send on its first message and saw the
 * text sit in the box with no reason given.
 */

const navigate = vi.fn();
const post = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({}),
  useNavigate: () => navigate,
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
}));
// Every read fails: the page has to render its new-chat screen with nothing
// loaded, and none of the reads are what this test is about.
vi.mock("@/lib/api", () => ({
  api: {
    get: () => Promise.reject(new Error("not under test")),
    post: (...args: unknown[]) => post(...args),
  },
}));
// The socket is opened per thread; there is none on a new chat, and jsdom has
// no server to hold one open to.
vi.mock("./use-thread-stream", () => ({ useThreadStream: () => undefined }));

import { ChatPage } from "./chat-page";

const REFUSAL =
  "This workspace has used all of its Argentum credits, so I can't run that right now. Ask an admin to top up the balance — current usage is on the Usage page in the dashboard.";

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ChatPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  navigate.mockReset();
  post.mockReset();
});

describe("ChatPage, new chat", () => {
  it("shows the 402 refusal of a first message and keeps what was typed", async () => {
    post.mockRejectedValue(
      Object.assign(new Error("Request failed with status code 402"), {
        response: { status: 402, data: { error: REFUSAL } },
      }),
    );
    mount();

    const box = screen.getByPlaceholderText("Ask about your business…") as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "How many orders this week?" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(REFUSAL);
    expect(alert.className).toContain("text-destructive");
    expect(box.value).toBe("How many orders this week?");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("shows nothing before a send fails", async () => {
    mount();
    await waitFor(() => expect(screen.getByPlaceholderText("Ask about your business…")).toBeInTheDocument());
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
