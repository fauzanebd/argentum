import { describe, expect, it } from "vitest";
import { generateOffReason, profileCreditNote } from "./credit-reasons";

/**
 * Review round 1 (RIZ-89 F1): the agent generator and the business-profile
 * suggestion tell a workspace whose free credit is waiting, or was not
 * approved, exactly that — not that it used credit it never had. A workspace
 * that spent a grant reads as it always did.
 */

const USED_UP_AGENT =
  "This workspace has used all of its Argentum credits. An admin can top up the balance — you can still write the agent yourself.";

describe("generateOffReason", () => {
  it("is empty while the button works, and says when it is not configured", () => {
    expect(generateOffReason({ available: true, credits_exhausted: false })).toBe("");
    expect(generateOffReason({ available: false, credits_exhausted: true })).toBe(
      "Generating is not configured on this deployment.",
    );
  });

  it("says the free credit is waiting, not used up", () => {
    const reason = generateOffReason({ available: true, credits_exhausted: true, free_credit: "pending" });
    expect(reason).toBe(
      "This workspace's free Argentum credit is waiting for approval. Generating starts working as soon as it's approved — you can still write the agent yourself.",
    );
    expect(reason).not.toMatch(/used all/);
  });

  it("says the free credit was not approved", () => {
    expect(generateOffReason({ available: true, credits_exhausted: true, free_credit: "rejected" })).toBe(
      "This workspace's free Argentum credit was not approved. An admin can top up the balance — you can still write the agent yourself.",
    );
  });

  it("is today's sentence for a workspace that spent a grant", () => {
    expect(generateOffReason({ available: true, credits_exhausted: true })).toBe(USED_UP_AGENT);
  });
});

describe("profileCreditNote", () => {
  const base = { sources: 1, credits_exhausted: true, truncated: false };

  it("is nothing when credit is not why, or a draft exists", () => {
    expect(profileCreditNote(undefined)).toBeNull();
    expect(profileCreditNote({ ...base, credits_exhausted: false })).toBeNull();
    expect(profileCreditNote({ ...base, free_credit: "pending", draft: {} as never })).toBeNull();
  });

  it("says the free credit is waiting and to re-scan once approved", () => {
    const note = profileCreditNote({ ...base, free_credit: "pending" });
    expect(note).toBe(
      "We could not read your connected data to suggest a profile yet — this workspace's free credit is waiting for approval. Your sources still work; describe the business below in your own words, or press Re-scan under Settings → Data sources once it's approved.",
    );
    expect(note).not.toMatch(/out of credit/);
  });

  it("says the free credit was not approved", () => {
    expect(profileCreditNote({ ...base, free_credit: "rejected" })).toMatch(
      /this workspace's free credit was not approved\. .*top up and press Re-scan/,
    );
  });

  it("is today's sentence for a workspace that spent a grant", () => {
    expect(profileCreditNote(base)).toBe(
      "We could not read your connected data to suggest a profile — this workspace is out of credit. Your sources still work; describe the business below in your own words, or top up and press Re-scan under Settings → Data sources.",
    );
  });
});
