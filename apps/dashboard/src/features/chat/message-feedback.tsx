import { useState } from "react";
import { ThumbsUp, ThumbsDown, Loader2 } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

/**
 * Was that answer any good? (T-Q2)
 *
 * Until this control existed, the only quality signal this product had was
 * forty synthetic questions against one demo schema — nothing anywhere
 * recorded whether a real answer on a real warehouse was right. So the button
 * is deliberately the lowest-friction write in the dashboard: one click, no
 * dialog, no confirmation. The reason box only appears after a thumbs-down,
 * because asking for prose before recording the verdict loses the verdict from
 * everyone in a hurry, which is everyone.
 *
 * The rating is sent immediately and the reason is a second, optional write to
 * the same row — the endpoint upserts on (message, actor), so the follow-up
 * replaces rather than duplicates.
 */
export function MessageFeedback({ messageId }: { messageId: string }) {
  const [rating, setRating] = useState<1 | -1 | null>(null);
  const [saving, setSaving] = useState(false);
  const [reason, setReason] = useState("");
  const [reasonSent, setReasonSent] = useState(false);
  const [failed, setFailed] = useState(false);

  async function send(next: 1 | -1, withReason?: string) {
    setSaving(true);
    setFailed(false);
    try {
      await api.post(`/messages/${messageId}/feedback`, {
        rating: next,
        reason: withReason ?? "",
      });
      setRating(next);
      if (withReason !== undefined) setReasonSent(true);
    } catch {
      // Surfaced rather than swallowed: a rating that silently failed to save
      // is worse than no button, because the person believes they reported it.
      setFailed(true);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="mt-1.5 flex flex-col gap-2">
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-label="This answer was right"
          aria-pressed={rating === 1}
          disabled={saving}
          onClick={() => void send(1)}
          className={cn(
            "rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50",
            rating === 1 && "text-emerald-600 dark:text-emerald-400",
          )}
        >
          <ThumbsUp className="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          aria-label="This answer was wrong"
          aria-pressed={rating === -1}
          disabled={saving}
          onClick={() => void send(-1)}
          className={cn(
            "rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50",
            rating === -1 && "text-red-600 dark:text-red-400",
          )}
        >
          <ThumbsDown className="h-3.5 w-3.5" />
        </button>
        {saving && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
        {failed && (
          <span className="text-[11px] text-red-600 dark:text-red-400">
            could not save — try again
          </span>
        )}
      </div>

      {/* Only after a thumbs-down, and only until it has been sent. The reason
          is the most useful column in the table for anyone tuning the agent
          and the one a UI is most tempted to omit. */}
      {rating === -1 && !reasonSent && (
        <div className="flex flex-col gap-1.5">
          <Textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="What was wrong with it? (optional)"
            rows={2}
            className="text-xs"
          />
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="secondary"
              disabled={saving || reason.trim() === ""}
              onClick={() => void send(-1, reason.trim())}
            >
              Send
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setReasonSent(true)}>
              Skip
            </Button>
          </div>
        </div>
      )}

      {rating === -1 && reasonSent && (
        <span className="text-[11px] text-muted-foreground">
          Thanks — logged for review.
        </span>
      )}
    </div>
  );
}
