import { describe, it, expect } from "vitest";
import { colorForAgent, agentColorIndex } from "./agent-colors";

// The property that made this a function rather than a hash: no two agents in
// a room share a colour, and an agent's colour does not depend on who else is
// in the room with it.

describe("colorForAgent", () => {
  it("gives the first eight agents eight distinct ramp colours", () => {
    const seen = new Set(Array.from({ length: 8 }, (_, i) => colorForAgent(i)));
    expect(seen.size).toBe(8);
  });

  it("uses the design tokens' categorical ramp, not an invented palette", () => {
    expect(colorForAgent(0)).toBe("var(--chart-1)");
    expect(colorForAgent(7)).toBe("var(--chart-8)");
  });

  it("wraps past the end of the ramp rather than running out", () => {
    expect(colorForAgent(8)).toBe(colorForAgent(0));
  });

  // An agent in a room and no longer on the roster — the window between a
  // delete and a refetch. It must not borrow a live agent's identity.
  it("gives an unknown agent the muted foreground, not series 1", () => {
    expect(colorForAgent(-1)).toBe("var(--muted-foreground)");
    expect(colorForAgent(-1)).not.toBe(colorForAgent(0));
  });
});

describe("agentColorIndex", () => {
  it("assigns by roster position", () => {
    const idx = agentColorIndex(["a", "b", "c"]);
    expect(idx.get("a")).toBe(0);
    expect(idx.get("c")).toBe(2);
  });

  // The reason the index is taken over the whole roster rather than over the
  // room: an agent keeps its colour when it joins a second conversation.
  it("gives an agent the same colour regardless of which room it is in", () => {
    const roster = agentColorIndex(["fin", "ops", "hr"]);
    const inRoomA = colorForAgent(roster.get("ops") ?? -1);
    const inRoomB = colorForAgent(roster.get("ops") ?? -1);
    expect(inRoomA).toBe(inRoomB);
    expect(inRoomA).toBe("var(--chart-2)");
  });

  it("returns undefined for an agent that is not on the roster", () => {
    expect(agentColorIndex(["a"]).get("b")).toBeUndefined();
  });
});
