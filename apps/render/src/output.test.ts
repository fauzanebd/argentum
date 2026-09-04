import assert from "node:assert/strict";
import test from "node:test";

import { SUPPORTED_VERSION } from "@argentum/motion";
import type { Plan } from "@argentum/motion";

import { checkOutput, pageName, parseJobPath, parseOutput } from "./output";

/**
 * The stills mode's own door checks (T-G5), by shape and without a browser —
 * the pattern plan.test.ts set.
 */

function plan(overrides: Partial<Plan> = {}): Plan {
  return {
    version: SUPPORTED_VERSION,
    width: 1080,
    height: 1350,
    fps: 1,
    total_frames: 2,
    locale: "id",
    title: "Test",
    metrics: {} as Plan["metrics"],
    brand: {} as Plan["brand"],
    scenes: [
      { kind: "cover", frames: 1 },
      { kind: "closing", frames: 1 },
    ],
    ...overrides,
  };
}

test("output defaults to video, so a caller written before stills is unchanged", () => {
  assert.equal(parseOutput(undefined), "video");
  assert.equal(parseOutput(null), "video");
  assert.equal(parseOutput("video"), "video");
  assert.equal(parseOutput("stills"), "stills");
});

test("an output this service does not make is refused by name", () => {
  const problem = parseOutput("gif");
  assert.match(problem, /unknown output "gif"/);
  assert.match(problem, /video, stills/);
});

test("a stills request against a video plan is refused before a browser starts", () => {
  const problem = checkOutput(plan({ still: undefined }), "stills");
  assert.ok(problem);
  assert.match(problem, /still: true/);
});

test("a video request against a still plan is refused, and told what to ask for", () => {
  const problem = checkOutput(plan({ still: true }), "video");
  assert.ok(problem);
  assert.match(problem, /output: "stills"/);
});

test("a matched pair passes", () => {
  assert.equal(checkOutput(plan({ still: true }), "stills"), null);
  assert.equal(checkOutput(plan(), "video"), null);
});

test("pages are 1-based, two digits, jpg", () => {
  assert.equal(pageName(1), "01.jpg");
  assert.equal(pageName(10), "10.jpg");
});

test("the job routes parse, with and without a page", () => {
  const id = "123e4567-e89b-12d3-a456-426614174000";
  assert.deepEqual(parseJobPath(`/v1/jobs/${id}`), { id, result: false });
  assert.deepEqual(parseJobPath(`/v1/jobs/${id}/result`), { id, result: true });
  assert.deepEqual(parseJobPath(`/v1/jobs/${id}/result/3`), { id, result: true, page: 3 });
  assert.equal(parseJobPath(`/v1/jobs/${id}/result/x`), null);
  assert.equal(parseJobPath(`/v1/jobs/not-a-uuid/result/1`), null);
  assert.equal(parseJobPath(`/v1/jobs/${id}/pages/1`), null);
});
