import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, Clock, Database, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { OnboardingStatus } from "./use-onboarding-status";

/**
 * What the new-chat screen says about the workspace's data (RIZ-76), in the
 * slot between the welcome text and the composer.
 *
 * Nothing in it blocks: the composer stays usable in every state, because an
 * admin who wants to look round Settings first and a member who cannot fix it
 * are both better served by being told than by being stopped.
 */
export function OnboardingNotice({
  status,
  isAdmin,
  className,
}: {
  status: OnboardingStatus;
  isAdmin: boolean;
  className?: string;
}) {
  switch (status) {
    case "no-data":
      return <EmptyDataCard isAdmin={isAdmin} className={className} />;
    case "reading":
      return (
        <StatusNotice
          tone="info"
          icon={<Loader2 className="size-3.5 animate-spin text-primary" aria-hidden />}
          title="Reading your schema."
          rest="Answers get better once this finishes, usually within a minute."
          className={className}
        />
      );
    case "ready":
      return (
        <StatusNotice
          tone="positive"
          icon={<CheckCircle2 className="size-3.5 text-positive" aria-hidden />}
          title="Ready."
          rest="Try one of the questions below."
          className={className}
        />
      );
    case "timeout":
      return (
        <StatusNotice
          tone="warning"
          icon={<Clock className="size-3.5 text-warning" aria-hidden />}
          title="This is taking longer than usual."
          rest="You can ask now; answers may be less precise until it finishes."
          className={className}
        />
      );
    default:
      return null;
  }
}

/**
 * A workspace with no source can still be chatted to, and the answer cannot
 * use its data. Admins are sent to onboarding — the only page that connects a
 * first source in one step; members cannot (`POST /api/connections` is admin)
 * and are told who can.
 */
function EmptyDataCard({ isAdmin, className }: { isAdmin: boolean; className?: string }) {
  return (
    <div className={cn("flex items-start gap-4 rounded-xl bg-card px-6 py-5 shadow-card", className)}>
      <div
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-lg",
          isAdmin ? "bg-primary-tint text-primary-ink" : "bg-muted text-muted-foreground",
        )}
      >
        <Database className="size-4" aria-hidden />
      </div>
      <div className="min-w-0">
        <h2 className="text-[15px] font-semibold leading-snug">
          {isAdmin ? "Connect your data to start asking questions" : "No data connected yet"}
        </h2>
        <p className="mt-1 text-[13px] text-muted-foreground">
          {isAdmin
            ? "Argentum answers from your own database. Connecting one takes a few minutes and needs read-only credentials."
            : "Ask an admin of this workspace to connect a database. Until then, answers can't use your company's data."}
        </p>
        {isAdmin && (
          <Button asChild className="mt-3">
            <Link to="/onboarding">Connect your data</Link>
          </Button>
        )}
      </div>
    </div>
  );
}

const TONES = {
  info: { box: "border-primary/30 bg-primary/5", title: "" },
  positive: { box: "border-positive/30 bg-positive-tint", title: "text-positive-ink" },
  warning: { box: "border-warning/30 bg-warning-tint", title: "text-warning-ink" },
} as const;

/** BudgetWarningBanner's shape — the slot's existing tenant — with an icon in
 *  place of its dismiss button: this one goes away by itself. */
function StatusNotice({
  tone,
  icon,
  title,
  rest,
  className,
}: {
  tone: keyof typeof TONES;
  icon: ReactNode;
  title: string;
  rest: string;
  className?: string;
}) {
  return (
    <div
      role="status"
      className={cn("flex items-start gap-2.5 rounded-lg border px-4 py-2.5 text-sm", TONES[tone].box, className)}
    >
      <span className="mt-[3px] shrink-0">{icon}</span>
      <div className="min-w-0 flex-1">
        <span className={cn("font-medium", TONES[tone].title)}>{title}</span>
        <span className="text-muted-foreground"> {rest}</span>
      </div>
    </div>
  );
}
