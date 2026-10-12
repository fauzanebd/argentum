// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Which starter questions a new chat offers (T-B3, RIZ-76 R6): a templated
 * agent its card's, the untemplated company default the workspace's own
 * tables, and every other agent none.
 */

const DATA_QUESTIONS = [
  "Give me an overview of our orders data.",
  "What are the most recent records in customers?",
  "What stands out in products?",
];
const TEMPLATE_QUESTIONS = ["Which SKUs are about to run out?"];

const AGENTS = [
  { id: "ag-default", name: "Analyst", enabled: true, is_default: true },
  { id: "ag-ops", name: "Operations", enabled: true, is_default: false, template_key: "operations" },
  { id: "ag-blank", name: "Notes", enabled: true, is_default: false },
  { id: "ag-default-ops", name: "Ops default", enabled: true, is_default: true, template_key: "operations" },
];

let roster: Record<string, unknown>;
const get = vi.fn();

vi.mock("@/lib/api", () => ({
  api: { get: (...args: unknown[]) => get(...args) },
}));

import { useAgents } from "./use-agents";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

async function loaded() {
  const { result } = renderHook(() => useAgents(), { wrapper });
  await waitFor(() => expect(result.current.byId.size).toBe(AGENTS.length));
  return (id: string, hasData?: boolean) =>
    result.current.starterQuestionsFor(result.current.byId.get(id), hasData);
}

beforeEach(() => {
  roster = {
    agents: AGENTS,
    reachable_agent_ids: AGENTS.map((a) => a.id),
    tools: [],
    templates: [{ key: "operations", name: "Operations", starter_questions: TEMPLATE_QUESTIONS }],
    data_starter_questions: DATA_QUESTIONS,
  };
  get.mockReset().mockImplementation(async () => ({ data: roster }));
});

describe("starterQuestionsFor", () => {
  it("offers the untemplated default the workspace's own questions once data is connected (AC6)", async () => {
    const questionsFor = await loaded();
    expect(questionsFor("ag-default", true)).toEqual(DATA_QUESTIONS);
  });

  it("offers the default nothing while no data is connected, or before that is known", async () => {
    const questionsFor = await loaded();
    expect(questionsFor("ag-default", false)).toEqual([]);
    expect(questionsFor("ag-default")).toEqual([]);
  });

  it("keeps a templated agent's card questions, default or not (AC8)", async () => {
    const questionsFor = await loaded();
    expect(questionsFor("ag-ops", true)).toEqual(TEMPLATE_QUESTIONS);
    expect(questionsFor("ag-default-ops", true)).toEqual(TEMPLATE_QUESTIONS);
    // Unchanged from before RIZ-76: the card's questions do not wait on data.
    expect(questionsFor("ag-ops", false)).toEqual(TEMPLATE_QUESTIONS);
  });

  it("offers an untemplated agent that is not the default nothing (Q13)", async () => {
    const questionsFor = await loaded();
    expect(questionsFor("ag-blank", true)).toEqual([]);
  });

  it("reads an API that predates the field as no questions — the screen as it was", async () => {
    delete roster.data_starter_questions;
    const questionsFor = await loaded();
    expect(questionsFor("ag-default", true)).toEqual([]);
  });
});
