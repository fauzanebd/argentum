import type { ReactNode } from "react";
import { ShieldAlert } from "lucide-react";
import { useIsAdmin } from "@/store/auth";

/**
 * AdminGate renders a settings panel read-only for members.
 *
 * T-04 moved every credential- and config-mutating route behind an admin
 * check. Without something here a member would still see "Save" and "Delete"
 * and get a 403 toast for their trouble, so the controls are disabled at the
 * source: a disabled <fieldset> disables every button, input and select
 * beneath it natively, which is both fewer edits than threading a prop through
 * a dozen components and harder to forget when a new control is added.
 *
 * The data stays visible because reading it is a member's right — the API
 * classifies every GET in these panels as member-accessible.
 *
 * One limit to know about: a Radix Sheet or Dialog renders through a portal
 * into document.body, outside this fieldset, so its contents are not disabled
 * by it. That is fine today because every such overlay in Settings is opened by
 * a button inside the fieldset, which is itself disabled. Something that opened
 * one from outside — a shortcut, a URL parameter — would need its own check.
 *
 * This is presentation only. The server decides.
 */
export function AdminGate({ children }: { children: ReactNode }) {
  const isAdmin = useIsAdmin();
  if (isAdmin) return <>{children}</>;

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-2.5 rounded-md border border-border bg-muted/40 px-3.5 py-3">
        <ShieldAlert className="h-4 w-4 mt-0.5 shrink-0 text-muted-foreground" />
        <p className="text-sm text-muted-foreground">
          You have member access, so these settings are read-only. Ask an admin on your team to make
          changes.
        </p>
      </div>
      <fieldset disabled className="m-0 min-w-0 border-0 p-0 opacity-70">
        {children}
      </fieldset>
    </div>
  );
}
