// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { MetricCoverageResponse } from "@argentum/api-types";

/**
 * Is the metric layer accumulating (T-F4)?
 *
 * Every property worth holding here is about the panel refusing to make a claim
 * it cannot support. A percentage is a structural statement about every answer
 * a workspace gives, and the three ways it can be meaningless — nobody asked,
 * too few asked, everybody's question was already covered — read identically if
 * the screen just prints a number.
 */

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: unknown }) => <a href="/settings">{children as never}</a>,
}));

import { MetricCoverageView } from "./metric-coverage-panel";

function report(over: Partial<MetricCoverageResponse> = {}): MetricCoverageResponse {
  const base: MetricCoverageResponse = {
    window_days: 30,
    from: "2026-08-12T00:00:00Z",
    certified: 0,
    ad_hoc: 0,
    mixed: 0,
    no_data: 0,
    answered: 0,
    percent: 0,
    ad_hoc_top: [],
  };
  return { ...base, ...over };
}

it("says nobody asked rather than showing 0%", () => {
  render(<MetricCoverageView data={report({ no_data: 4 })} />);
  expect(screen.getByText("—")).toBeTruthy();
  expect(screen.getByText(/Nobody has asked this workspace a data question/)).toBeTruthy();
});

// Eight turns at 50% and eighty turns at 50% are different claims, and an admin
// who reorganises their registry off the first one was misled by the screen.
it("refuses to be read as a measurement when too few turns went into it", () => {
  render(
    <MetricCoverageView
      data={report({
        certified: 4,
        ad_hoc: 4,
        answered: 8,
        percent: 50,
        ad_hoc_top: [{ question: "berapa penjualan minggu ini", turns: 4 }],
      })}
    />,
  );
  expect(screen.getByText("50%")).toBeTruthy();
  expect(screen.getByText(/Too few answers to read much into yet/)).toBeTruthy();
  // And the actionable list is withheld with it: a "define this" prompt off
  // four turns is the same over-reading in a more expensive form.
  expect(screen.queryByText(/Asked most often without a metric/)).toBeNull();
});

it("names what to define next once the number means something", () => {
  render(
    <MetricCoverageView
      data={report({
        certified: 10,
        mixed: 5,
        ad_hoc: 25,
        answered: 40,
        percent: 37.5,
        ad_hoc_top: [
          { question: "berapa penjualan minggu ini", turns: 11 },
          { question: "top 10 produk bulan lalu", turns: 6 },
        ],
      })}
    />,
  );
  expect(screen.getByText("38%")).toBeTruthy();
  expect(screen.getByText("11×")).toBeTruthy();
  expect(screen.getByText("berapa penjualan minggu ini")).toBeTruthy();
  expect(screen.getByText("Define a metric")).toBeTruthy();
});

// A mixed turn did reach the registry, so it counts as covered — and the panel
// still shows it separately rather than folding it into "from a metric", because
// a turn that read a metric and then joined something to it is a different
// thing from one that did not need to.
it("counts a mixed turn as covered and still shows it on its own", () => {
  render(
    <MetricCoverageView
      data={report({ certified: 10, mixed: 10, ad_hoc: 0, answered: 20, percent: 100 })}
    />,
  );
  expect(screen.getByText("100%")).toBeTruthy();
  expect(screen.getByText("Both")).toBeTruthy();
  expect(screen.getByText(/Every answering turn reached a defined metric/)).toBeTruthy();
});

it("reports the window the server used, not the one the client asked for", () => {
  render(<MetricCoverageView data={report({ window_days: 7, no_data: 1 })} />);
  expect(screen.getByText(/Over the last 7 days/)).toBeTruthy();
});
