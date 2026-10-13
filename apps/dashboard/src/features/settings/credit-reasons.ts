import type { AgentGenerationInfo, ProfileSuggestionResponse } from "@argentum/api-types";

/**
 * Why a settings shortcut is off for want of credit, in words (RIZ-89).
 *
 * The chat notice sends a new admin whose free credit waits for an operator to
 * "connect data and set up agents meanwhile", and these are the two pages that
 * sentence lands them on. Since RIZ-89 a workspace without credit has usually
 * spent nothing, so each page says which of the three it is: waiting for
 * approval, not approved, or used up. They live here, out of the components,
 * so they can be tested without rendering either form.
 */

/** The agent generator's disabled-button reason; "" when the button is on. */
export function generateOffReason(generation: AgentGenerationInfo): string {
  if (!generation.available) return "Generating is not configured on this deployment.";
  if (!generation.credits_exhausted) return "";
  switch (generation.free_credit) {
    case "pending":
      return "This workspace's free Argentum credit is waiting for approval. Generating starts working as soon as it's approved — you can still write the agent yourself.";
    case "rejected":
      return "This workspace's free Argentum credit was not approved. An admin can top up the balance — you can still write the agent yourself.";
    default:
      return "This workspace has used all of its Argentum credits. An admin can top up the balance — you can still write the agent yourself.";
  }
}

/** Why the business profile has no suggestion; null when credit is not why. */
export function profileCreditNote(suggestion: ProfileSuggestionResponse | null | undefined): string | null {
  if (!suggestion?.credits_exhausted || suggestion.draft) return null;
  switch (suggestion.free_credit) {
    case "pending":
      return "We could not read your connected data to suggest a profile yet — this workspace's free credit is waiting for approval. Your sources still work; describe the business below in your own words, or press Re-scan under Settings → Data sources once it's approved.";
    case "rejected":
      return "We could not read your connected data to suggest a profile — this workspace's free credit was not approved. Your sources still work; describe the business below in your own words, or top up and press Re-scan under Settings → Data sources.";
    default:
      return "We could not read your connected data to suggest a profile — this workspace is out of credit. Your sources still work; describe the business below in your own words, or top up and press Re-scan under Settings → Data sources.";
  }
}
