import { useCallback, useEffect, useState } from "react";
import axios from "axios";
import type { Result } from "@argentum/api-types/dashboard";

import { DashboardPanel } from "@/features/dashboards/panel";
import { cn } from "@/lib/utils";

/**
 * A shared dashboard, opened by a stranger (T-D13/T-D21).
 *
 * **No `api` client here, deliberately, and for the reason the report player's
 * page gives:** the shared client attaches an access token and refreshes it on
 * a 401, and neither is right for somebody who has neither. A bare axios call
 * is the whole of this page's transport.
 *
 * Nothing about this page implies the visitor is inside somebody's workspace —
 * no shell, no sidebar, no navigation. They were sent one dashboard and that is
 * what they get.
 *
 * **There is no refresh control**, and its absence is a decision rather than an
 * omission. A bearer link that can spend a customer's warehouse on demand is a
 * leaked link that costs money forever; the server bounds it per hour anyway,
 * and a button inviting a visitor to spend it would be arguing with that.
 */

type SharedDashboard = {
  share: {
    id: string;
    allow_filters: boolean;
    expires_at: string;
    locked_params?: Record<string, string>;
  };
  dashboard: { title: string };
  result: Result;
};

type State =
  | { kind: "loading" }
  | { kind: "gone" }
  | { kind: "throttled" }
  | { kind: "password"; wrong: boolean }
  | { kind: "ready"; view: SharedDashboard };

export function DashboardSharePage({ token }: { token: string }) {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [password, setPassword] = useState("");
  // Filters the visitor has moved. Only ever sent when the share allows them;
  // the server ignores them otherwise, and pinned values win regardless.
  const [filters, setFilters] = useState<Record<string, string>>({});

  const load = useCallback(
    (pw: string, params: Record<string, string>, hadPassword: boolean) => {
      const query = new URLSearchParams(params);
      if (pw) query.set("password", pw);
      const qs = query.toString();
      axios
        .get<SharedDashboard>(
          `/share/dashboard/${encodeURIComponent(token)}${qs ? `?${qs}` : ""}`,
        )
        .then((res) => setState({ kind: "ready", view: res.data }))
        .catch((err) => {
          const status = axios.isAxiosError(err) ? err.response?.status : undefined;
          if (status === 401) {
            // `wrong` only once the visitor has actually tried one: the first
            // 401 is the page asking, not the page refusing.
            setState({ kind: "password", wrong: hadPassword });
            return;
          }
          if (status === 429) {
            setState({ kind: "throttled" });
            return;
          }
          // One outcome for every other failure, matching the API: a visitor
          // cannot tell an expired link from a wrong one, and neither can this
          // page.
          setState({ kind: "gone" });
        });
    },
    [token],
  );

  useEffect(() => {
    load("", {}, false);
  }, [load]);

  if (state.kind === "loading") return <Centered>Opening…</Centered>;

  if (state.kind === "gone") {
    return (
      <Centered>
        <h1 className="text-xl font-semibold">This link is not available.</h1>
        <p className="mt-2 max-w-md text-sm text-muted-foreground">
          It may have expired, or been revoked by whoever shared it. Ask them for
          a new one.
        </p>
      </Centered>
    );
  }

  if (state.kind === "throttled") {
    return (
      <Centered>
        <h1 className="text-xl font-semibold">This link is busy.</h1>
        <p className="mt-2 max-w-md text-sm text-muted-foreground">
          It has been opened too many times in the past hour. Try again shortly.
        </p>
      </Centered>
    );
  }

  if (state.kind === "password") {
    return (
      <Centered>
        <form
          className="w-full max-w-xs text-left"
          onSubmit={(e) => {
            e.preventDefault();
            setState({ kind: "loading" });
            load(password, filters, true);
          }}
        >
          <h1 className="text-lg font-semibold">This dashboard is password-protected.</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Whoever sent you the link has the password.
          </p>
          <input
            type="password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="mt-3 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
            placeholder="Password"
          />
          {state.wrong && (
            <p className="mt-2 text-sm text-destructive">
              That password did not work.
            </p>
          )}
          <button
            type="submit"
            className="mt-3 w-full rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            Open
          </button>
        </form>
      </Centered>
    );
  }

  const { view } = state;
  const panels = (view.result.panels ?? []).filter((p) => p !== undefined && p !== null);
  const applied = Object.entries(view.result.applied_filters ?? {});
  const locked = view.share.locked_params ?? {};

  return (
    <main className="mx-auto min-h-dvh w-full max-w-6xl px-4 py-8">
      <header className="mb-4">
        <h1 className="text-lg font-semibold text-foreground">
          {view.dashboard.title}
        </h1>
        {/* Which window answered. A dashboard that does not say this leaves the
            reader to assume it is the one they would have picked. */}
        {applied.length > 0 && (
          <p className="mt-1 text-xs text-muted-foreground">
            {applied.map(([name, value]) => `${name}: ${value}`).join(" · ")}
          </p>
        )}
      </header>

      {/* Filters appear only when the share allows them, and a pinned one is
          shown as fixed rather than hidden: a visitor who can see that a value
          was chosen for them is better informed than one who cannot tell. */}
      {view.share.allow_filters && applied.length > 0 && (
        <div className="mb-4 flex flex-wrap items-center gap-2">
          {applied.map(([name, value]) =>
            name in locked ? (
              <span
                key={name}
                className="rounded-md border border-border bg-muted px-2 py-1 text-xs text-muted-foreground"
                title="Fixed by whoever shared this dashboard"
              >
                {name}: {String(value)}
              </span>
            ) : (
              <input
                key={name}
                defaultValue={String(value)}
                aria-label={name}
                className="w-40 rounded-md border border-border bg-background px-2 py-1 text-xs"
                onKeyDown={(e) => {
                  if (e.key !== "Enter") return;
                  const next = { ...filters, [name]: e.currentTarget.value };
                  setFilters(next);
                  setState({ kind: "loading" });
                  load(password, next, Boolean(password));
                }}
              />
            ),
          )}
        </div>
      )}

      {panels.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          This dashboard has no panels.
        </p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {panels.map((panel) => (
            <DashboardPanel
              key={panel.panel_id}
              panel={panel}
              height={240}
              className={panel.viz === "kpi" ? "sm:col-span-1" : "sm:col-span-2"}
            />
          ))}
        </div>
      )}

      <footer className="mt-8 text-[11px] text-muted-foreground">
        Shared dashboard · access expires{" "}
        {new Date(view.share.expires_at).toLocaleDateString()}
      </footer>
    </main>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className={cn("flex min-h-dvh flex-col items-center justify-center px-6 text-center")}>
      {children}
    </div>
  );
}
