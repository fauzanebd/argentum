// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

/**
 * The post above the buttons that authorise it (T-G7).
 *
 * The two properties worth holding are the ticket's own acceptance, and they
 * pull in opposite directions: a carousel proposal must show every slide and
 * the caption *before* Approve, and a proposal about anything else must render
 * exactly as it did before this ticket. The second is the one that would break
 * quietly — a card that fetches a manifest for every action kind would show a
 * blank panel on `http_action` and nobody would file it.
 */

const get = vi.fn();

vi.mock("@/lib/api", () => ({ api: { get: (...a: unknown[]) => get(...a) } }));
vi.mock("@/store/auth", () => ({ useAuthStore: (s: unknown) => (s as (x: unknown) => unknown)({ user: { id: "u-1" } }) }));
vi.mock("@/store/composer", () => ({ useComposerStore: (s: unknown) => (s as (x: unknown) => unknown)({ prefill: vi.fn() }) }));
vi.mock("./use-actions", () => ({
  useDecideAction: () => ({ mutateAsync: vi.fn(), isPending: false }),
  usePendingActions: () => ({ data: [] }),
}));
// The slide itself fetches bytes through the API client; the strip's job here
// is to ask for one figure per page, which is what this asserts on.
vi.mock("@/hooks/use-object-url", () => ({ useObjectUrl: () => ({ url: null, failed: true }) }));

import { ApprovalCard } from "./approval-card";

const DOC = "11111111-2222-3333-4444-555555555555";

const manifest = {
  caption: "Diskon akhir pekan\n\n#promo #gelael",
  alts: ["Sampul", "Harga", "Penutup"],
  pages: 3,
};

function proposal(params: unknown) {
  return {
    id: "act-1",
    action_kind: "publish_post",
    description: "Publish a 3-slide carousel to @toko_contoh",
    status: "proposed",
    can_decide: true,
    params_redacted: params,
  } as never;
}

function renderCard(params: unknown) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ApprovalCard invocation={proposal(params)} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  get.mockReset();
  get.mockResolvedValue({ data: manifest });
});

it("shows every slide and the caption before the approve button", async () => {
  renderCard({ document_id: DOC });

  // One figure per page, each labelled with the manifest's alt — the reader
  // who cannot see the image still learns what is on the slide.
  await waitFor(() => expect(screen.getByLabelText("Sampul")).toBeTruthy());
  expect(screen.getByLabelText("Harga")).toBeTruthy();
  expect(screen.getByLabelText("Penutup")).toBeTruthy();
  expect(screen.getByLabelText("3 slides")).toBeTruthy();

  // The caption as it would be pasted, hashtags and all, and a way to take it.
  expect(screen.getByText(/Diskon akhir pekan/)).toBeTruthy();
  expect(screen.getByLabelText("Copy caption")).toBeTruthy();

  // And it is above the decision, not below it: a preview after the button is
  // a preview the approver has already scrolled past.
  const strip = screen.getByLabelText("3 slides");
  const approve = screen.getByText("Go ahead");
  expect(strip.compareDocumentPosition(approve) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

it("leaves a card for any other action kind unchanged", async () => {
  // An http_action carries no document, so nothing should be asked for.
  renderCard({ url: "https://erp.example/orders", method: "POST" });

  await waitFor(() => expect(screen.getByText("Go ahead")).toBeTruthy());
  expect(get).not.toHaveBeenCalled();
  expect(screen.queryByLabelText("Copy caption")).toBeNull();
});

// A document that is not a carousel answers 404, and the card must survive it
// as an ordinary card rather than an error state — the route is doubling as
// the format check, so a refusal is a normal answer here.
it("renders the plain card when the document is not a carousel", async () => {
  get.mockRejectedValue({ response: { status: 404 } });
  renderCard({ document_id: DOC });

  await waitFor(() => expect(screen.getByText("Go ahead")).toBeTruthy());
  expect(screen.queryByLabelText("Copy caption")).toBeNull();
});
