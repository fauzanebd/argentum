// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import "../test/reduced-motion";
import { renderHook } from "@testing-library/react";

import { useEnter } from "./motion";

/**
 * The reduced-motion arm, in a file of its own.
 *
 * **Not a stylistic split — framer resolves `prefers-reduced-motion` once and
 * caches it in module state.** Any test that renders a motion hook before this
 * one fixes the answer for it, and `vi.resetModules()` does not undo that
 * reliably once the module graph has been touched. The first draft of this
 * suite had both arms in one file and watched the reduced arm assert against
 * the moving arm's variants — passing on a claim it was not testing.
 *
 * Vitest isolates each test file, so a file that stubs the preference before it
 * imports anything gets the preference it asked for. The stub therefore runs at
 * module scope, above the import of the module under test.
 */

describe("motion is reduced when the user asks for it", () => {
  // What "reduced" means here is the file's own claim: spatial movement is
  // removed, opacity is kept. A fade carries no vestibular risk; a slide is the
  // part that causes harm.
  it("removes movement and keeps the fade", () => {
    const { result } = renderHook(() => useEnter());
    for (const name of ["hidden", "visible", "exit"]) {
      const variant = result.current[name] as Record<string, unknown>;
      expect(variant).not.toHaveProperty("y");
      expect(variant).not.toHaveProperty("x");
      expect(variant).not.toHaveProperty("scale");
      expect(variant).toHaveProperty("opacity");
    }
  });
});
