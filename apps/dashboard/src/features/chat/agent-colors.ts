/**
 * Which colour names an agent in a room (T-N4).
 *
 * **Assigned by position in the company roster, never hashed from the id.**
 * A hash is the obvious implementation and it is wrong here: two agents
 * colliding on one hue in a room of four is roughly a one-in-three coin flip,
 * and when it happens it is silent and permanent for that tenant. Position is
 * stable, collision-free up to the ramp's length, and reproducible on every
 * screen that renders the same roster.
 *
 * The ramp is `--chart-1` … `--chart-8` from `packages/design-tokens` — the
 * same eight the report charts use, which is the set the colour-vision gate in
 * `T-R3` was run against. A second palette invented here would be a second
 * palette nobody re-tested.
 *
 * **Colour is never the attribution.** The agent's name is; the colour is
 * redundant reinforcement. A reader who cannot distinguish two hues must lose
 * nothing, which is why every surface that uses this also renders the name.
 */
const RAMP_LENGTH = 8;

/**
 * colorForAgent returns a CSS colour for the agent at `index` in the roster.
 *
 * A negative index — an agent that is in a room and no longer on the roster,
 * which is possible for the moment between a delete and a refetch — takes the
 * muted foreground rather than wrapping round to series 1. A departed agent
 * should not borrow the identity of a live one.
 */
export function colorForAgent(index: number): string {
  if (index < 0) return "var(--muted-foreground)";
  return `var(--chart-${(index % RAMP_LENGTH) + 1})`;
}

/**
 * agentColorIndex builds the roster-position lookup a room renders from.
 *
 * Taken over the whole roster rather than over the room's own members, so an
 * agent keeps its colour when it is added to a second conversation. A colour
 * that depends on who else is in the room is a colour that changes when
 * somebody leaves.
 */
export function agentColorIndex(agentIDs: readonly string[]): Map<string, number> {
  const out = new Map<string, number>();
  agentIDs.forEach((id, i) => out.set(id, i));
  return out;
}
