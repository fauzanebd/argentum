import { useEffect, useMemo, useState } from "react";
import { cn } from "@/lib/utils";
import { colorForAgent } from "./agent-colors";
import type { ThreadParticipant } from "@argentum/api-types";

/**
 * The `@` menu in the composer (T-N4).
 *
 * **It offers participants only, never the whole roster.** `T-N3` refuses a
 * message naming an agent that is on the roster and not in this conversation,
 * so an autocomplete that suggested one would be a menu of refusals. The way to
 * address an agent that is not here is to add it, which is the participant bar
 * two elements up.
 */
export type MentionQuery = {
  /** Where the `@` is, so a pick knows what to replace. */
  at: number;
  /** What has been typed after it, lower-cased. */
  query: string;
};

/**
 * mentionQueryAt finds the `@…` the caret is currently inside, or null.
 *
 * The `@` must open a word — preceded by start-of-text or whitespace — which is
 * the same rule the server's parser applies, and is what stops an email address
 * from opening a menu mid-typing.
 */
export function mentionQueryAt(text: string, caret: number): MentionQuery | null {
  for (let i = caret - 1; i >= 0; i--) {
    const ch = text[i];
    if (ch === "@") {
      if (i > 0 && !/\s/.test(text[i - 1])) return null;
      const query = text.slice(i + 1, caret);
      // A space ends it: "@Finance " has been chosen and the menu should close
      // rather than keep matching on the rest of the sentence. Two-word agent
      // names are still reachable — the menu filters on the prefix before the
      // space, and picking inserts the whole name.
      if (/\s/.test(query)) return null;
      return { at: i, query: query.toLowerCase() };
    }
    if (/\s/.test(ch)) return null;
  }
  return null;
}

/** applyMention replaces the in-progress `@…` with the agent's full name. */
export function applyMention(text: string, q: MentionQuery, name: string): string {
  const before = text.slice(0, q.at);
  const after = text.slice(q.at + 1 + q.query.length);
  return `${before}@${name} ${after.replace(/^\s+/, "")}`;
}

export function MentionMenu({
  participants,
  query,
  colorIndex,
  onPick,
  className,
}: {
  participants: ThreadParticipant[];
  query: MentionQuery;
  /** Roster-position colours, the same map the participant bar and the
   *  message bubbles read. Colouring by position in *this menu* instead would
   *  give an agent one dot here and a different one on its own answer. */
  colorIndex: Map<string, number>;
  onPick: (name: string) => void;
  className?: string;
}) {
  const matches = useMemo(
    () =>
      participants.filter((p) =>
        (p.agent_name ?? "").toLowerCase().startsWith(query.query),
      ),
    [participants, query.query],
  );

  const [active, setActive] = useState(0);
  useEffect(() => setActive(0), [query.query]);

  if (matches.length === 0) return null;

  return (
    <div
      className={cn(
        "absolute bottom-full left-0 z-30 mb-2 w-64 overflow-hidden rounded-xl border border-border bg-popover shadow-lg",
        className,
      )}
      role="listbox"
      aria-label="Address an agent"
    >
      {matches.map((p, i) => (
        <button
          key={p.agent_id}
          type="button"
          role="option"
          aria-selected={i === active}
          onMouseEnter={() => setActive(i)}
          // onMouseDown, not onClick: the composer must not lose focus before
          // the pick lands, or the caret position the replacement needs is gone.
          onMouseDown={(e) => {
            e.preventDefault();
            onPick(p.agent_name ?? "");
          }}
          className={cn(
            "flex w-full items-center gap-2 px-3 py-2 text-left text-sm",
            // `bg-secondary`, which is what command.tsx uses for a highlighted
            // typeahead row — and deliberately not `bg-accent`, which
            // dropdown-menu.tsx uses: `--accent` in this design system is the
            // brand red (#F25C5C), so an active row rendered with it is a solid
            // red fill that swallows the agent's own colour dot. The first
            // screenshot of this menu showed exactly that.
            i === active && "bg-secondary text-foreground",
          )}
        >
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-full"
            style={{ backgroundColor: colorForAgent(colorIndex.get(p.agent_id) ?? -1) }}
          />
          <span className="truncate">{p.agent_name}</span>
        </button>
      ))}
    </div>
  );
}
