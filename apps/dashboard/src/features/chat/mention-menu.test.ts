import { describe, it, expect } from "vitest";
import { mentionQueryAt, applyMention } from "./mention-menu";

// The composer half of T-N4's addressing. The server's parser is the authority
// on who answers; this decides only when a menu opens and what a pick inserts,
// and the two have to agree about what an `@` looks like.

describe("mentionQueryAt", () => {
  it("opens on an @ at the start of the text", () => {
    expect(mentionQueryAt("@fin", 4)).toEqual({ at: 0, query: "fin" });
  });

  it("opens on an @ after a space", () => {
    expect(mentionQueryAt("ask @op", 7)).toEqual({ at: 4, query: "op" });
  });

  it("opens on a bare @ with nothing typed yet", () => {
    expect(mentionQueryAt("ask @", 5)).toEqual({ at: 4, query: "" });
  });

  it("lower-cases the query so matching is case-insensitive", () => {
    expect(mentionQueryAt("@FIN", 4)?.query).toBe("fin");
  });

  // The server applies the same rule: an @ inside a word is an email address
  // or a handle, not addressing.
  it("does not open on an @ inside a word", () => {
    expect(mentionQueryAt("finance@example.com", 12)).toBeNull();
  });

  it("closes once a space has been typed after the name", () => {
    expect(mentionQueryAt("@Finance what", 13)).toBeNull();
  });

  it("is null when the caret is not in a mention at all", () => {
    expect(mentionQueryAt("what happened?", 14)).toBeNull();
  });

  it("reads the mention the caret is in, not the last one in the text", () => {
    // Caret sits just after "@op"; the later "@fin" is ahead of it.
    expect(mentionQueryAt("@op and @fin", 3)).toEqual({ at: 0, query: "op" });
  });
});

describe("applyMention", () => {
  it("replaces the partial name with the full one and a trailing space", () => {
    const q = mentionQueryAt("@fin", 4)!;
    expect(applyMention("@fin", q, "Finance")).toBe("@Finance ");
  });

  it("keeps what was already typed before the mention", () => {
    const q = mentionQueryAt("ask @op", 7)!;
    expect(applyMention("ask @op", q, "Ops")).toBe("ask @Ops ");
  });

  it("does not double the space when text already follows", () => {
    const q = mentionQueryAt("@fin", 4)!;
    expect(applyMention("@fin  what happened?", q, "Finance")).toBe(
      "@Finance what happened?",
    );
  });

  it("inserts a two-word agent name whole", () => {
    const q = mentionQueryAt("@fin", 4)!;
    expect(applyMention("@fin", q, "Finance Team")).toBe("@Finance Team ");
  });
});
