import { Bot } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type { Agent } from "@argentum/api-types";

/**
 * Which agent a new conversation opens on (T-S3).
 *
 * It renders only before the first message. Changing the agent mid-thread is
 * deliberately not offered: the history in the model's context was produced
 * under a different persona, a different tool allowlist and different sources,
 * and reinterpreting it under new ones is a decision, not a widget.
 *
 * One agent means no control at all rather than a select with a single option
 * — a company that has never opened Settings → Agents has exactly the backfilled
 * default, and a picker that cannot pick anything is furniture.
 */
export function AgentPicker({
  agents,
  value,
  onChange,
  className,
}: {
  agents: Agent[];
  /** null is "the company default", which is also what an empty roster gives. */
  value: string | null;
  onChange: (agentID: string) => void;
  className?: string;
}) {
  if (agents.length < 2) return null;

  const selected = agents.find((a) => a.id === value);

  return (
    <div className={cn("w-full max-w-3xl", className)}>
      <Select value={value ?? undefined} onValueChange={onChange}>
        <SelectTrigger
          className="h-8 w-auto gap-2 rounded-full border-border/70 bg-card px-3 text-xs shadow-sm"
          aria-label="Agent"
        >
          <Bot className="size-3.5 shrink-0 opacity-60" />
          <SelectValue placeholder="Choose an agent" />
        </SelectTrigger>
        <SelectContent align="start" className="max-w-sm">
          {agents.map((a) => (
            <SelectItem
              key={a.id}
              value={a.id}
              // Not a child of the item: Radix clones the item's text into the
              // closed trigger, so a description passed as a child would render
              // inside the pill as well as in the list.
              description={a.description || undefined}
            >
              <span className="font-medium">{a.name}</span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selected?.description && (
        <p className="mt-1.5 px-1 text-xs text-muted-foreground">
          {selected.description}
        </p>
      )}
    </div>
  );
}

/**
 * What a thread already runs as. A label rather than a disabled Select: a
 * control that cannot be operated still reads as one that might be, and this
 * one never becomes editable.
 */
export function AgentBadge({
  name,
  className,
}: {
  name: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-xs text-muted-foreground",
        className,
      )}
    >
      <Bot className="size-3 opacity-60" />
      {name}
    </span>
  );
}
