import { Plus, X, Bot } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
} from "@/components/ui/dropdown-menu";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  TooltipProvider,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { colorForAgent } from "./agent-colors";
import type { Agent, ThreadParticipant } from "@argentum/api-types";

/**
 * Who is in this conversation, and the controls for changing it (T-N4).
 *
 * **It renders only for a room**, which is a conversation with more than one
 * agent — so a company with one agent, and every conversation that has never
 * had one added, looks exactly as it did before this ticket. The `+` is the
 * exception: it appears whenever there is somebody left to add, because
 * otherwise there is no way to make the first room.
 *
 * This does not replace `AgentPicker`, and the distinction matters enough to
 * write down. The picker sets which agent a *new* conversation opens on and
 * disappears after the first message, because reinterpreting history under a
 * different persona is a decision rather than a widget. Adding a participant
 * does not reinterpret anything — it adds a reader who can see the transcript
 * and be addressed from here on.
 */
export function ParticipantBar({
  participants,
  roster,
  colorIndex,
  defaultSpeakerID,
  onAdd,
  onRemove,
  busy,
  className,
}: {
  participants: ThreadParticipant[];
  /** Every agent this person may talk to, including disabled ones — a
   *  disabled agent is offered greyed with the reason rather than hidden, so an
   *  admin can see why the one they are looking for is not available. An agent
   *  they may **not** talk to is not in this list at all (T-Z4): a disabled
   *  control says who to ask, and a restricted agent is one the product does
   *  not show them. */
  roster: Agent[];
  colorIndex: Map<string, number>;
  /** Who answers when nobody is addressed. Marked, and not removable. */
  defaultSpeakerID: string;
  onAdd: (agentID: string) => void;
  onRemove: (agentID: string) => void;
  busy?: boolean;
  className?: string;
}) {
  const inRoom = new Set(participants.map((p) => p.agent_id));
  const addable = roster.filter((a) => !inRoom.has(a.id));

  // Nothing to show and nothing to add: one agent, no room possible.
  if (participants.length < 2 && addable.length === 0) return null;

  return (
    <TooltipProvider delayDuration={300}>
      <div
        className={cn("flex w-full max-w-3xl flex-wrap items-center gap-1.5", className)}
        aria-label="Agents in this conversation"
      >
        {participants.length > 1 &&
          participants.map((p) => {
            const isDefault = p.agent_id === defaultSpeakerID;
            return (
              <span
                key={p.agent_id}
                className="inline-flex items-center gap-1.5 rounded-full border border-border/70 bg-card py-1 pl-2 pr-1 text-xs shadow-sm"
              >
                <span
                  aria-hidden
                  className="size-2 shrink-0 rounded-full"
                  style={{ backgroundColor: colorForAgent(colorIndex.get(p.agent_id) ?? -1) }}
                />
                <span className="font-medium">{p.agent_name || "Agent"}</span>
                {isDefault ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      {/* The mark is a word, not only a dot: the dot beside it
                          is the agent's colour and means something else. */}
                      <span className="rounded-full bg-muted px-1.5 text-[10px] text-muted-foreground">
                        default
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>
                      Answers when you don&rsquo;t @ anyone. Make another agent the
                      default to remove this one.
                    </TooltipContent>
                  </Tooltip>
                ) : (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => onRemove(p.agent_id)}
                    aria-label={`Remove ${p.agent_name} from this conversation`}
                    className="rounded-full p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
                  >
                    <X className="size-3" />
                  </button>
                )}
              </span>
            );
          })}

        {addable.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                disabled={busy}
                className="inline-flex items-center gap-1 rounded-full border border-dashed border-border/70 px-2 py-1 text-xs text-muted-foreground hover:border-border hover:text-foreground disabled:opacity-50"
              >
                <Plus className="size-3" />
                {participants.length > 1 ? "Add agent" : "Add another agent"}
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="max-w-xs">
              <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
                They can read this conversation and be addressed with @
              </DropdownMenuLabel>
              {addable.map((a) => (
                <DropdownMenuItem
                  key={a.id}
                  disabled={!a.enabled}
                  onSelect={() => a.enabled && onAdd(a.id)}
                  className="gap-2"
                >
                  <span
                    aria-hidden
                    className="size-2 shrink-0 rounded-full"
                    style={{ backgroundColor: colorForAgent(colorIndex.get(a.id) ?? -1) }}
                  />
                  <span className="flex-1 truncate">{a.name}</span>
                  {!a.enabled && (
                    <span className="text-[10px] text-muted-foreground">disabled</span>
                  )}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}

        {participants.length > 1 && (
          // Roadmap 12's decision 13 (T-Z4). This read "not an access boundary"
          // until grants existed, and it is replaced rather than deleted: four
          // agents in one pane still read as an org chart, so what that chart
          // enforces — and where it stops — still has to be said where it is
          // seen. Decision 4's sentence, and not the word "secure". The last
          // clause is T-Z10's: a conversation is hidden from anyone not granted
          // every agent in it, which is what the room this bar sits in means —
          // and T-Z11's, since the documents it produced go with it.
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="ml-auto inline-flex items-center gap-1 text-[11px] text-muted-foreground">
                <Bot className="size-3" />
                who can use these agents
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-xs">
              An admin can restrict an agent to the people granted it, and you
              are only offered the agents you may use. That is a boundary against
              accident and casual browsing, not against a determined admin. A
              channel answers as a restricted agent only where an admin
              acknowledged it, and the website widget never reaches one. A conversation, and every
              document it produced, is hidden from anyone not granted every agent
              in it.
            </TooltipContent>
          </Tooltip>
        )}
      </div>
    </TooltipProvider>
  );
}
