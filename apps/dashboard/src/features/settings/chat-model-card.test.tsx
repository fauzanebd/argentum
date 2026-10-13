// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Settings → General → AI Models (RIZ-97): the workspace chat model picker.
 *
 * Driven against an API stub that remembers what it was told, because the
 * acceptance lines are round trips — "the choice survives a reload", "a reload
 * shows the previous model" — and a stub answering one fixture before and
 * after would pass a card that never saved anything. The copy asserted here is
 * the approved mockups', word for word.
 */

const KIMI = "moonshotai/kimi-k2.6";
const DEEPSEEK = "deepseek/deepseek-v4.1-flash";

let admin = true;
vi.mock("@/store/auth", () => ({ useIsAdmin: () => admin }));

// The General tab's other cards fetch on their own; only one test renders
// the tab, and it is about Company preferences, not the business profile.
vi.mock("./business-profile-card", () => ({ BusinessProfileCard: () => null }));

let stored: string | null; // the workspace's primary row's model, null = no row
let locked: boolean;
let withChatModel: boolean;
let modelsFail: boolean;
let putError: { status: number; error: string; code?: string } | null;
const get = vi.fn();
const put = vi.fn();

const OPTIONS = [
  {
    model: KIMI,
    label: "Kimi K2.6",
    hint: "The model Argentum is tuned and tested on.",
    default: true,
    input_per_1k_usd: 0.00095,
    output_per_1k_usd: 0.004,
    pricing_known: true,
  },
  {
    model: DEEPSEEK,
    label: "DeepSeek V4.1 Flash",
    hint: "About a sixth of the cost per answer. Check a few of your usual questions after switching.",
    default: false,
    input_per_1k_usd: 0.00015,
    output_per_1k_usd: 0.0006,
    pricing_known: true,
  },
];

function setting() {
  return { model: stored ?? KIMI, default_model: KIMI, locked, options: OPTIONS };
}

function modelsBody() {
  const primary = stored ?? KIMI;
  return {
    primary: {
      role: "primary",
      model: primary,
      interface: "openai",
      input_per_1k_usd: primary === DEEPSEEK ? 0.00015 : 0.00095,
      output_per_1k_usd: primary === DEEPSEEK ? 0.0006 : 0.004,
      pricing_known: true,
    },
    light: {
      role: "light",
      model: "openai/gpt-5-mini",
      interface: "openai",
      input_per_1k_usd: 0.00025,
      output_per_1k_usd: 0.002,
      pricing_known: true,
    },
    classifier: {
      role: "classifier",
      model: "openai/gpt-5-nano",
      interface: "openai",
      input_per_1k_usd: 0.00005,
      output_per_1k_usd: 0.0004,
      pricing_known: true,
    },
    ...(withChatModel ? { chat_model: setting() } : {}),
  };
}

async function getImpl(path: string) {
  if (path === "/config/models") {
    if (modelsFail) throw new Error("network down");
    return { data: modelsBody() };
  }
  if (path === "/settings") {
    return {
      data: {
        default_currency: "IDR",
        supported_currencies: ["IDR", "USD"],
        currency_precisions: { IDR: 0, USD: 2 },
        currency_rounding: "half_up",
        pii_redaction_mode: "strict",
        pii_redaction_modes: ["strict", "contact_ok", "off"],
      },
    };
  }
  throw new Error(`unexpected GET ${path}`);
}

async function putImpl(path: string, body: { model?: string }) {
  if (path === "/settings") return { data: {} };
  if (path !== "/config/chat-model") throw new Error(`unexpected PUT ${path}`);
  if (putError) {
    const { status, ...data } = putError;
    throw Object.assign(new Error(`Request failed with status code ${status}`), {
      response: { status, data },
    });
  }
  stored = body.model === KIMI ? null : (body.model ?? null);
  return { data: setting() };
}

vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    put: (...args: unknown[]) => put(...args),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

import { AdminGate } from "@/components/layout/admin-gate";
import { useModels } from "@/lib/use-models";
import { ChatModelCard, firstSentence, perMillion } from "./chat-model-card";
import { GeneralTab } from "./general-tab";

/** The card the way General renders it: only once the query has answered. */
function Card() {
  const { data } = useModels();
  return data ? <ChatModelCard models={data} /> : null;
}

function mount(ui = <Card />) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AdminGate>{ui}</AdminGate>
    </QueryClientProvider>,
  );
}

/** A fresh mount — what a reload is, as far as this card can tell. */
async function reload() {
  cleanup();
  mount();
  return screen.findByText("AI Models");
}

// Radix Select measures and captures the pointer; jsdom does neither.
beforeAll(() => {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
});

function openSelect() {
  fireEvent.pointerDown(screen.getByRole("combobox"), { button: 0, ctrlKey: false, pointerType: "mouse" });
  return screen.findByRole("listbox");
}

async function pick(label: string) {
  const list = await openSelect();
  fireEvent.click(within(list).getByText(label));
}

function saveButton() {
  return screen.getByRole("button", { name: /^(Save|Saving…)$/ });
}

beforeEach(() => {
  admin = true;
  stored = null;
  locked = false;
  withChatModel = true;
  modelsFail = false;
  putError = null;
  get.mockReset().mockImplementation(getImpl);
  put.mockReset().mockImplementation(putImpl);
});

describe("the chat model picker (admin)", () => {
  it("AC1: shows the default model, its price, its id and both options", async () => {
    mount();
    await screen.findByText("AI Models");
    expect(
      screen.getByText(
        "The chat model writes every answer in this workspace, for every agent and every channel. The models below it run summaries and safety checks, and are set by Argentum.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveTextContent("Kimi K2.6 (default)");
    expect(screen.getByText("The model Argentum is tuned and tested on.")).toBeInTheDocument();
    expect(
      screen.getByText(/^\$0\.95 per 1M input tokens · \$4\.00 per 1M output tokens ·/),
    ).toBeInTheDocument();
    expect(screen.getByText(KIMI)).toBeInTheDocument();
    expect(
      screen.getByText("A change applies from the next message. Answers already written stay as they are."),
    ).toBeInTheDocument();
    // Nothing to save until the selection changes.
    expect(saveButton()).toBeDisabled();

    const list = await openSelect();
    const options = within(list).getAllByRole("option");
    expect(options).toHaveLength(2);
    expect(options[0]).toHaveTextContent("Kimi K2.6 (default)");
    expect(options[0]).toHaveTextContent(
      "$0.95 in · $4.00 out per 1M tokens. The model Argentum is tuned and tested on.",
    );
    expect(options[1]).toHaveTextContent("DeepSeek V4.1 Flash");
    expect(options[1]).toHaveTextContent("$0.15 in · $0.60 out per 1M tokens. About a sixth of the cost per answer.");
  });

  it("R11: Light and Classifier stay read-only, priced per 1M", async () => {
    mount();
    await screen.findByText("AI Models");
    expect(screen.getByText("Light")).toBeInTheDocument();
    expect(screen.getByText("openai/gpt-5-mini")).toBeInTheDocument();
    expect(screen.getByText("$0.25 in · $2.00 out per 1M tokens")).toBeInTheDocument();
    expect(screen.getByText("Classifier")).toBeInTheDocument();
    expect(screen.getByText("$0.05 in · $0.40 out per 1M tokens")).toBeInTheDocument();
  });

  it("AC2: picking DeepSeek updates the hint and price, saves, and survives a reload", async () => {
    mount();
    await screen.findByText("AI Models");
    await pick("DeepSeek V4.1 Flash");

    expect(screen.getByRole("combobox")).toHaveTextContent("DeepSeek V4.1 Flash");
    expect(
      screen.getByText("About a sixth of the cost per answer. Check a few of your usual questions after switching."),
    ).toBeInTheDocument();
    expect(screen.getByText(/^\$0\.15 per 1M input tokens · \$0\.60 per 1M output tokens ·/)).toBeInTheDocument();
    expect(saveButton()).toBeEnabled();

    fireEvent.click(saveButton());
    expect(await screen.findByText("New messages use DeepSeek V4.1 Flash from now on.")).toBeInTheDocument();
    expect(screen.getByText("Saved")).toBeInTheDocument();
    expect(put).toHaveBeenCalledWith("/config/chat-model", { model: DEEPSEEK });
    // Saved, so nothing to save until the selection changes again.
    expect(saveButton()).toBeDisabled();
    // The models query is refetched, which is what moves the chat header.
    await waitFor(() =>
      expect(get.mock.calls.filter(([p]) => p === "/config/models").length).toBeGreaterThanOrEqual(2),
    );

    await reload();
    expect(screen.getByRole("combobox")).toHaveTextContent("DeepSeek V4.1 Flash");
    expect(screen.queryByText("Saved")).not.toBeInTheDocument();
  });

  it("AC5: back to the default", async () => {
    stored = DEEPSEEK;
    mount();
    await screen.findByText("AI Models");
    await pick("Kimi K2.6 (default)");
    fireEvent.click(saveButton());
    expect(await screen.findByText("New messages use Kimi K2.6 from now on.")).toBeInTheDocument();
    expect(put).toHaveBeenCalledWith("/config/chat-model", { model: KIMI });
    expect(stored).toBeNull();
  });

  it("AC9: a refused save says why and which model is still in use, and keeps the selection", async () => {
    putError = {
      status: 400,
      error: "deepseek/deepseek-v4.1-flash is not offered on this workspace",
      code: "model_not_offered",
    };
    mount();
    await screen.findByText("AI Models");
    await pick("DeepSeek V4.1 Flash");
    fireEvent.click(saveButton());

    expect(
      await screen.findByText(
        "Could not save the chat model: deepseek/deepseek-v4.1-flash is not offered on this workspace. This workspace still uses Kimi K2.6.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("Saved")).not.toBeInTheDocument();
    // The selection stays so the admin can retry.
    expect(screen.getByRole("combobox")).toHaveTextContent("DeepSeek V4.1 Flash");
    expect(saveButton()).toBeEnabled();

    await reload();
    expect(screen.getByRole("combobox")).toHaveTextContent("Kimi K2.6 (default)");
  });

  it("a server sentence that ends in a full stop is not given a second one", async () => {
    putError = {
      status: 409,
      error: "This workspace runs on its own model provider. Contact support to change its chat model.",
      code: "own_provider",
    };
    mount();
    await screen.findByText("AI Models");
    await pick("DeepSeek V4.1 Flash");
    fireEvent.click(saveButton());
    expect(
      await screen.findByText(
        "Could not save the chat model: This workspace runs on its own model provider. Contact support to change its chat model. This workspace still uses Kimi K2.6.",
      ),
    ).toBeInTheDocument();
  });

  it("a failure with no response (the API stopped) still says what is in use", async () => {
    put.mockImplementation(async () => {
      throw new Error("Network Error");
    });
    mount();
    await screen.findByText("AI Models");
    await pick("DeepSeek V4.1 Flash");
    fireEvent.click(saveButton());
    expect(
      await screen.findByText(
        "Could not save the chat model: Network Error. This workspace still uses Kimi K2.6.",
      ),
    ).toBeInTheDocument();
  });
});

describe("read-only states", () => {
  it("AC7: a member sees the choice with the select and Save disabled under the banner", async () => {
    admin = false;
    stored = DEEPSEEK;
    mount();
    await screen.findByText("AI Models");
    expect(screen.getByText(/you have member access/i)).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveTextContent("DeepSeek V4.1 Flash");
    expect(screen.getByRole("combobox")).toBeDisabled();
    expect(saveButton()).toBeDisabled();
    expect(
      screen.getByText("About a sixth of the cost per answer. Check a few of your usual questions after switching."),
    ).toBeInTheDocument();
    expect(screen.getByText(/^\$0\.15 per 1M input tokens · \$0\.60 per 1M output tokens ·/)).toBeInTheDocument();
  });

  it("AC8: a workspace on its own provider shows its model, the badge and the support line, and no picker", async () => {
    locked = true;
    stored = "gpt-5";
    mount();
    await screen.findByText("AI Models");
    expect(screen.getByText("gpt-5")).toBeInTheDocument();
    expect(screen.getByText("Own provider")).toBeInTheDocument();
    expect(
      screen.getByText(
        "This workspace runs on its own model provider, set up with Argentum support. To change its chat model, contact support.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.getByText("Light")).toBeInTheDocument();
  });

  it("an API older than the picker gets today's read-only rows", async () => {
    withChatModel = false;
    mount();
    await screen.findByText("AI Models");
    expect(
      screen.getByText("Models currently powering Argentum's analytics agent. Configured server-side."),
    ).toBeInTheDocument();
    expect(screen.getByText("Primary")).toBeInTheDocument();
    expect(screen.getByText(KIMI)).toBeInTheDocument();
    expect(screen.getByText("$0.95 in · $4.00 out per 1M tokens")).toBeInTheDocument();
    expect(screen.getByText("Light")).toBeInTheDocument();
    expect(screen.getByText("Classifier")).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });
});

describe("the General tab around it (Q16)", () => {
  it("renders no AI Models card when /config/models fails, and Company preferences still saves", async () => {
    modelsFail = true;
    mount(<GeneralTab />);
    await screen.findByText("Company preferences");
    await waitFor(() => expect(get).toHaveBeenCalledWith("/config/models"));
    expect(screen.queryByText("AI Models")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/settings", {
        default_currency: "IDR",
        currency_rounding: "half_up",
        pii_redaction_mode: "strict",
      }),
    );
    expect(put).not.toHaveBeenCalledWith("/config/chat-model", expect.anything());
  });

  it("the picker's Save and Company preferences' Save are separate buttons", async () => {
    mount(<GeneralTab />);
    await screen.findByText("AI Models");
    expect(screen.getAllByRole("button", { name: "Save" })).toHaveLength(2);
  });
});

describe("price formatting", () => {
  it("writes per-1K rates per 1M", () => {
    expect(perMillion(0.00015)).toBe("$0.15");
    expect(perMillion(0.0006)).toBe("$0.60");
    expect(perMillion(0.00095)).toBe("$0.95");
    expect(perMillion(0.004)).toBe("$4.00");
    expect(perMillion(0.00005)).toBe("$0.05");
    expect(perMillion(0)).toBe("$0.00");
    // Under a cent per 1M keeps its digits rather than reading as free.
    expect(perMillion(0.000001)).toBe("$0.001");
  });

  it("takes the first sentence of a hint for the open list", () => {
    expect(firstSentence("About a sixth of the cost per answer. Check a few of your usual questions.")).toBe(
      "About a sixth of the cost per answer.",
    );
    expect(firstSentence("The model Argentum is tuned and tested on.")).toBe(
      "The model Argentum is tuned and tested on.",
    );
    expect(firstSentence("")).toBe("");
  });
});
