import { describe, expect, it } from "vitest";
import { settingsTabs } from "./tabs";

/**
 * Which settings tabs a member is offered.
 *
 * The rule this file pins is stated in `tabs.ts` and was already written down
 * in the page it came from: a panel whose **GET** is admin-only is hidden
 * rather than rendered read-only, because `AdminGate` disables the controls and
 * cannot make the list load — the member gets the 403's empty result rendered
 * as an empty state, which reads as "there are none" rather than "you may not
 * see these".
 *
 * Procedures is the case that motivated the test: every one of its routes is
 * `RoleAdmin` in `cmd/api/policy.go`, including `GET /api/skills`, and the tab
 * was offered to members anyway — so a member clicking it was told "No
 * procedures yet", of a workspace that might have twenty.
 *
 * The admin-only list is asserted whole rather than one membership at a time.
 * A new tab added to the wrong side of the condition is the failure this
 * catches, and a test naming only today's tabs would not catch it.
 */

const ADMIN_ONLY_GET = ["skills", "reports", "webhooks", "api-keys", "embed", "team"];

describe("settingsTabs", () => {
  it("offers a member no tab whose read is admin-only", () => {
    const ids = settingsTabs(false).map((t) => t.id);
    for (const id of ADMIN_ONLY_GET) {
      expect(ids).not.toContain(id);
    }
  });

  it("offers an admin every tab", () => {
    const ids = settingsTabs(true).map((t) => t.id);
    for (const id of ADMIN_ONLY_GET) {
      expect(ids).toContain(id);
    }
  });

  it("keeps the panels a member may read", () => {
    // Not an exhaustive list of the member's tabs — these are the ones whose
    // GET is member-level on purpose, so hiding them would be the opposite
    // mistake: a disabled control tells a member who to ask, a missing tab
    // tells them the feature does not exist.
    const ids = settingsTabs(false).map((t) => t.id);
    expect(ids).toEqual(
      expect.arrayContaining(["general", "data-sources", "metrics", "agents", "images", "about"]),
    );
  });

  it("labels procedures as procedures", () => {
    const skills = settingsTabs(true).find((t) => t.id === "skills");
    expect(skills?.label).toBe("Procedures");
  });
});
