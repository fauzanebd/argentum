// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import type { DerivedFiguresResponse } from "@argentum/api-types";
import { DerivedFiguresView } from "./derived-figures-panel";

/**
 * How much arithmetic still happens in the sentence (T-W3)?
 *
 * The property worth holding is the denominator. Every turn before this
 * measurement shipped is unchecked, so a screen that divided by answering turns
 * — or printed 0% when nothing could be checked — would report a product that
 * never works anything out in prose, off no evidence at all.
 */

function report(over: Partial<DerivedFiguresResponse> = {}): DerivedFiguresResponse {
  const base: DerivedFiguresResponse = {
    window_days: 30,
    from: "2026-08-13T00:00:00Z",
    answered: 0,
    checked: 0,
    unchecked: 0,
    composed: 0,
    computed: 0,
    residue: 0,
    cross_source: 0,
    unaccounted_percent: 0,
    residue_percent: 0,
  };
  return { ...base, ...over };
}

it("says nobody asked rather than showing 0%", () => {
  render(<DerivedFiguresView data={report()} />);
  expect(screen.getByText("—")).toBeTruthy();
  expect(screen.getByText(/Nobody has asked this workspace a data question/)).toBeTruthy();
});

// The state every deployment is in on the day this ships: plenty of history,
// none of it measured.
it("refuses to read unchecked history as clean", () => {
  render(<DerivedFiguresView data={report({ answered: 12, unchecked: 12, cross_source: 2 })} />);
  expect(screen.getByText("—")).toBeTruthy();
  expect(screen.queryByText("0%")).toBeNull();
  expect(screen.getByText(/None of the 12 answering turns could be checked/)).toBeTruthy();
});

it("labels a percentage off too few checked turns", () => {
  render(
    <DerivedFiguresView
      data={report({ answered: 9, checked: 8, unchecked: 1, composed: 4, unaccounted_percent: 50 })}
    />,
  );
  expect(screen.getByText("50%")).toBeTruthy();
  expect(screen.getByText(/Too few checked turns to read much into yet/)).toBeTruthy();
});

it("says how many answering turns were left out of the percentage", () => {
  render(
    <DerivedFiguresView
      data={report({
        answered: 28,
        checked: 23,
        unchecked: 5,
        composed: 7,
        computed: 12,
        residue: 4,
        cross_source: 2,
        unaccounted_percent: 47.8,
        residue_percent: 25,
      })}
    />,
  );
  expect(screen.getByText("48%")).toBeTruthy();
  expect(screen.getByText("of 23 checked turns")).toBeTruthy();
  expect(screen.getByText("Computed, then more worked out")).toBeTruthy();
  expect(screen.getByText(/5 answering turns could not be checked and are not in the percentage/)).toBeTruthy();
});

it("reports the window the server used, not the one the client asked for", () => {
  render(<DerivedFiguresView data={report({ window_days: 7 })} />);
  expect(screen.getByText(/Over the last 7 days/)).toBeTruthy();
});
