// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The onboarding Save (RIZ-76 R1, R2, R7): it can no longer fail silently,
 * cannot submit twice, never creates a second connection on a retry, and has a
 * way out.
 */

const navigate = vi.fn();
const toast = vi.fn();
const get = vi.fn();
const post = vi.fn();
const put = vi.fn();

let createConnection: () => Promise<unknown>;
let saveSettings: () => Promise<unknown>;

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
vi.mock("@/hooks/use-toast", () => ({ toast: (...args: unknown[]) => toast(...args) }));
// Radix Select's hidden native <select> has no options in jsdom until it is
// opened, and reports "" back through onValueChange — which would blank the
// currency and fail validation for a reason no browser has. The page's own
// behaviour is not about the selects, so they are inert here.
vi.mock("@/components/ui/select", () => {
  const Pass = ({ children }: { children?: React.ReactNode }) => <>{children}</>;
  const Nothing = () => null;
  return { Select: Pass, SelectTrigger: Pass, SelectValue: Nothing, SelectContent: Nothing, SelectItem: Nothing };
});
vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
    put: (...args: unknown[]) => put(...args),
  },
}));

import { OnboardingPage } from "./onboarding-page";

/** An axios-shaped rejection carrying the API's `{error}` body. */
function apiError(message: string) {
  return Object.assign(new Error("Request failed with status code 500"), {
    response: { status: 500, data: { error: message } },
  });
}

const connectionPosts = () => post.mock.calls.filter(([p]) => p === "/connections").length;
const saveButton = () =>
  screen.getByRole("button", { name: /^(Save and continue|Saving…)$/ }) as HTMLButtonElement;
const skipButton = () => screen.getByRole("button", { name: "Skip for now" }) as HTMLButtonElement;

let qc: QueryClient;

function mount() {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OnboardingPage />
    </QueryClientProvider>,
  );
}

/** Fills a raw DSN and passes Test, which is what enables Save. */
async function readyToSave() {
  mount();
  fireEvent.click(screen.getByRole("button", { name: "Raw DSN" }));
  fireEvent.change(screen.getByLabelText("Connection string (DSN)"), {
    target: { value: "postgres://ro:secret@db.local:5432/shop?sslmode=require" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
  await screen.findByText("Connected");
  await waitFor(() => expect(saveButton().disabled).toBe(false));
}

beforeEach(() => {
  navigate.mockReset();
  toast.mockReset();
  createConnection = async () => ({ data: { connection: { id: "c1" } } });
  saveSettings = async () => ({ data: {} });
  get.mockReset().mockImplementation(async (path: string) => {
    if (path === "/meta/supported-databases") return { data: { registered: ["postgres"] } };
    if (path === "/settings") return { data: { supported_currencies: ["IDR", "USD"], default_currency: "IDR" } };
    return { data: {} };
  });
  post.mockReset().mockImplementation(async (path: string) => {
    if (path === "/connections/test") return { data: { ok: true } };
    if (path === "/connections") return createConnection();
    return { data: {} };
  });
  put.mockReset().mockImplementation(async () => saveSettings());
});

describe("OnboardingPage Save", () => {
  it("says why a failed save failed, re-enables Save and stays put (AC1)", async () => {
    createConnection = async () => {
      throw apiError("could not reach the database.");
    };
    await readyToSave();
    fireEvent.click(saveButton());

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(
      "Could not save the connection: could not reach the database. Nothing was saved. Press Save to try again.",
    );
    await waitFor(() => expect(saveButton().disabled).toBe(false));
    expect(saveButton().textContent).toBe("Save and continue");
    expect(navigate).not.toHaveBeenCalled();
    expect(put).not.toHaveBeenCalled();

    // A retry that succeeds posts once more and moves on, and the error goes.
    createConnection = async () => ({ data: { connection: { id: "c1" } } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/chat" }));
    expect(connectionPosts()).toBe(2);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("still lands on /chat with a toast when only the currency fails, with one connection (AC2)", async () => {
    saveSettings = async () => {
      throw apiError("settings unavailable");
    };
    await readyToSave();
    fireEvent.click(saveButton());

    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/chat" }));
    expect(toast).toHaveBeenCalledWith({
      title: "Connected, but the default currency was not saved",
      description: "Set it in Settings → General.",
    });
    expect(connectionPosts()).toBe(1);
    expect(put).toHaveBeenCalledWith("/settings", { default_currency: "IDR" });
  });

  it("shows Saving…, disables Save and Skip, and posts once however often Save is pressed (R2, Q4)", async () => {
    let finish: (v: unknown) => void = () => {};
    createConnection = () => new Promise((resolve) => (finish = resolve));
    await readyToSave();

    fireEvent.click(saveButton());
    fireEvent.click(saveButton());
    await waitFor(() => expect(saveButton().textContent).toBe("Saving…"));
    expect(saveButton().disabled).toBe(true);
    expect(skipButton().disabled).toBe(true);
    fireEvent.click(saveButton());
    expect(connectionPosts()).toBe(1);

    await act(async () => finish({ data: { connection: { id: "c1" } } }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/chat" }));
    expect(connectionPosts()).toBe(1);
  });

  // Review round 1, F1: /chat cached the empty list before the admin came here
  // from its card, and that list is still fresh when they come back. Unless
  // the save marks it stale first, /chat shows the card again and never polls.
  it("marks the cached source list stale before going back to /chat (AC5)", async () => {
    await readyToSave();
    qc.setQueryData(["connections"], []);
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    fireEvent.click(saveButton());
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/chat" }));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["connections"] });
    expect(qc.getQueryState(["connections"])?.isInvalidated).toBe(true);
    expect(invalidate.mock.invocationCallOrder[0]).toBeLessThan(navigate.mock.invocationCallOrder[0]);
  });

  it("leaves the cached source list alone when the save failed", async () => {
    createConnection = async () => {
      throw apiError("could not reach the database");
    };
    await readyToSave();
    qc.setQueryData(["connections"], []);
    fireEvent.click(saveButton());
    await screen.findByRole("alert");
    expect(qc.getQueryState(["connections"])?.isInvalidated).toBe(false);
  });

  it("sends a successful save to /chat with no toast", async () => {
    await readyToSave();
    fireEvent.click(saveButton());
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/chat" }));
    expect(toast).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("OnboardingPage Skip for now (R7)", () => {
  it("goes to /chat without saving anything (AC10)", async () => {
    mount();
    fireEvent.click(skipButton());
    expect(navigate).toHaveBeenCalledWith({ to: "/chat" });
    expect(connectionPosts()).toBe(0);
    expect(put).not.toHaveBeenCalled();
  });
});
