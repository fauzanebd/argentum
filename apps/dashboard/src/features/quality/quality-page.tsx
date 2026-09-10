import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { MessageSquareX, ShieldAlert, ThumbsDown, ThumbsUp } from "lucide-react";
import type { FeedbackWithContext } from "@argentum/api-types";
import { useIsAdmin } from "@/store/auth";
import { cn } from "@/lib/utils";
import { useFeedbackList, useFeedbackSummary } from "./use-feedback";

/**
 * Answer quality — what people said about the agent's answers (T-Q16).
 *
 * `T-Q2` built the thumbs, the table, and three admin routes to read them
 * back. It did not build a screen, so from the day it shipped the whole
 * lifecycle of a thumbs-down was: stored, used to exclude one turn from the
 * cookbook's harvest, and never seen by a human again. This is the screen.
 *
 * **It is a triage queue, not an analytics page.** The question it answers is
 * "what is the agent getting wrong", and the useful form of that answer is a
 * pattern across rows — the same metric, the same table, the same misread
 * question. So the reason and the answer sit on the row rather than behind a
 * click, and every row links into the thread where the rest of the turn is.
 */
export function QualityPage() {
  const isAdmin = useIsAdmin();
  const [onlyNegative, setOnlyNegative] = useState(true);
  const summary = useFeedbackSummary();
  const list = useFeedbackList(onlyNegative);

  // Both routes are admin on the server (policy.go), so a member does not get
  // a degraded page — they get no data at all. Saying so is better than four
  // empty panels that look like "nobody has ever complained".
  if (!isAdmin) {
    return (
      <div className="h-full overflow-y-auto">
        <div className="max-w-4xl mx-auto px-6 py-8">
          <h1 className="text-2xl font-bold mb-1">Answer quality</h1>
          <div className="mt-6 flex items-start gap-2.5 rounded-md border border-border bg-muted/40 px-3.5 py-3">
            <ShieldAlert className="h-4 w-4 mt-0.5 shrink-0 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">
              Ratings are visible to admins. You can still rate any answer in the chat — that is
              what fills this page.
            </p>
          </div>
        </div>
      </div>
    );
  }

  const rated = summary.data?.rated ?? 0;
  const rows = list.data?.feedback ?? [];

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Answer quality</h1>
        <p className="text-sm text-muted-foreground mb-6">
          What people said about the agent&rsquo;s answers. A rated answer is one somebody checked.
        </p>

        <div className="grid grid-cols-3 gap-3 mb-8">
          <Stat label="Rated" value={rated} />
          <Stat label="Marked wrong" value={summary.data?.down ?? 0} tone="bad" />
          {/*
            Over rated answers, never over turns — the server computes it for
            exactly this reason. A rate over turns would fall every time
            somebody asked a question and said nothing, which is most of them,
            and would improve on its own as usage grew.
          */}
          <Stat
            label="Wrong, of rated"
            value={rated === 0 ? "—" : `${Math.round((summary.data?.down_rate ?? 0) * 100)}%`}
            tone={rated === 0 ? undefined : "bad"}
          />
        </div>

        <div className="flex items-center gap-1 mb-4">
          {[
            { id: true, label: "Marked wrong" },
            { id: false, label: "Everything rated" },
          ].map((t) => (
            <button
              key={String(t.id)}
              onClick={() => setOnlyNegative(t.id)}
              className={cn(
                "rounded-md px-3 py-1.5 text-sm transition-colors",
                onlyNegative === t.id
                  ? "bg-muted font-medium text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>

        {list.isLoading ? (
          <p className="text-sm text-muted-foreground">Loading&hellip;</p>
        ) : rows.length === 0 ? (
          // The server's echo, not the toggle's state. `only_negative` is on
          // the response precisely so a client does not have to assume what it
          // got back, and the empty state is the one place where assuming would
          // put the wrong sentence in front of somebody: "nothing marked wrong"
          // and "nothing rated at all" are different claims about the product.
          <Empty onlyNegative={list.data?.only_negative ?? onlyNegative} />
        ) : (
          <ul className="space-y-3">
            {rows.map((f) => f && <Row key={f.id} f={f} />)}
          </ul>
        )}
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | string;
  tone?: "bad";
}) {
  return (
    <div className="rounded-lg border border-border px-4 py-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-1 text-2xl font-semibold tabular-nums",
          tone === "bad" && value !== 0 && "text-destructive",
        )}
      >
        {value}
      </div>
    </div>
  );
}

/** One verdict, with enough of the turn to recognise a pattern without opening
 *  it. The reason is first because it is the only free text a human wrote. */
function Row({ f }: { f: FeedbackWithContext }) {
  const down = f.rating === -1;
  return (
    <li className="rounded-lg border border-border px-4 py-3">
      <div className="flex items-start gap-3">
        {down ? (
          <ThumbsDown className="mt-0.5 size-4 shrink-0 text-destructive" />
        ) : (
          <ThumbsUp className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        )}
        <div className="min-w-0 flex-1">
          {f.reason ? (
            <p className="text-sm font-medium">{f.reason}</p>
          ) : (
            <p className="text-sm italic text-muted-foreground">No reason given</p>
          )}

          {f.question && (
            <p className="mt-2 truncate text-xs text-muted-foreground">
              <span className="font-medium">Asked:</span> {f.question}
            </p>
          )}
          {f.answer && (
            <p className="mt-1 line-clamp-3 text-xs text-muted-foreground">
              <span className="font-medium">Answered:</span> {f.answer}
            </p>
          )}

          <div className="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
            <time dateTime={f.created_at}>{new Date(f.created_at).toLocaleString()}</time>
            {/* actor_kind, not a name: a dashboard user, a widget visitor and an
                API caller are three different kinds of witness, and a tenant's
                own analyst noticing a wrong number is worth more than an
                anonymous thumbs-down from their customer's website. */}
            <span className="rounded bg-muted px-1.5 py-0.5">{f.actor_kind}</span>
            <Link
              to="/chat/$threadId"
              params={{ threadId: f.thread_id }}
              className="underline underline-offset-2 hover:text-foreground"
            >
              Open thread
            </Link>
          </div>
        </div>
      </div>
    </li>
  );
}

function Empty({ onlyNegative }: { onlyNegative: boolean }) {
  return (
    <div className="rounded-lg border border-dashed border-border px-4 py-10 text-center">
      <MessageSquareX className="mx-auto mb-3 size-6 text-muted-foreground" />
      {/* "Nobody has complained" and "nobody has rated anything" are different
          claims, and only one of them is good news. The empty state must not
          imply the first when the truth is the second. */}
      <p className="text-sm text-muted-foreground">
        {onlyNegative
          ? "Nothing has been marked wrong yet."
          : "Nothing has been rated yet."}
      </p>
      <p className="mt-1 text-xs text-muted-foreground">
        Ratings come from the thumbs under each answer in the chat.
      </p>
    </div>
  );
}
