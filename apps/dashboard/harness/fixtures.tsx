/**
 * The stand-ins the harness mounts the real components against.
 *
 * **Why stubs rather than the running product.** The arms this harness is here
 * to close are about *rendering*: does the counter go red, does the amber
 * notice appear, is the Procedures tab absent for a member. Reaching those
 * through the real stack would need a production login, a tenant whose index
 * actually overflows, and — for the chat chip — a paid model turn that happens
 * to open a procedure. None of that makes the pixels more true, and two of the
 * three are things this deployment should not be asked to produce on demand.
 *
 * What it therefore does **not** prove is wiring: that `GET /api/skills` really
 * returns this shape, that the member really gets a 403. Those are proven on
 * the wire already (`docs/coverage/skills.md` §6a) and in `cmd/api/policy.go`.
 * The two halves meet in the middle; neither covers the other, and saying so is
 * the point of this comment.
 */
import type { ReactNode } from "react";
import { FORM_NAME, FORM_TRIGGER, FORM_BODY } from "./constants";

/** Whether the harness is currently pretending to be an admin. Read by the
 *  `@/store/auth` stub, set from the scene table before mount. */
export let harnessIsAdmin = true;
export function setHarnessAdmin(v: boolean) {
  harnessIsAdmin = v;
}

const LONG_TRIGGER =
  "When someone asks for revenue broken down by branch for a period, or compares two periods of it.";

/** A workspace with three procedures, one of them switched off — the state the
 *  list, the badges and the per-agent checklist all have to render. */
export const SKILLS_OK = {
  skills: [
    {
      id: "sk-1",
      company_id: "co-1",
      name: "Weekly revenue by branch",
      when_to_use: LONG_TRIGGER,
      body: "1. …",
      enabled: true,
      source: "tenant",
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    },
    {
      id: "sk-2",
      company_id: "co-1",
      name: "How a month is closed",
      when_to_use: "When a question depends on whether a period is final.",
      body: "1. …",
      enabled: true,
      source: "tenant",
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    },
    {
      id: "sk-3",
      company_id: "co-1",
      name: "Retur dan potongan",
      when_to_use: "When a revenue figure is asked for without qualification.",
      body: "1. …",
      enabled: false,
      source: "tenant",
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    },
  ],
  limits: { name_chars: 60, when_to_use_chars: 200, body_chars: 8000, per_company: 200 },
  index: { lines: 4, chars: 1204, max_chars: 4000, max_lines: 20, dropped: [] as string[] },
};

/** The same workspace, over the character bound. `dropped` is the field the
 *  amber notice exists for, and the only place a tenant is ever told that a
 *  procedure they wrote is not reaching their agents. */
export const SKILLS_OVERFLOW = {
  ...SKILLS_OK,
  index: {
    lines: 13,
    chars: 3825,
    max_chars: 4000,
    max_lines: 20,
    // Only enabled procedures are ever in the index, so only an enabled one can
    // be dropped from it. Naming the switched-off row here would put a
    // screenshot in the docs implying otherwise.
    dropped: ["How a month is closed", "Stock opname mingguan", "Harga promo dan bundling"],
  },
};

/** What `POST /api/skills/preview` answers. Written by hand here, but copied
 *  from the shape the endpoint returns — including `refusal`, which is the
 *  sentence the *save* would have used, so the two never word the rule twice. */
export const PREVIEW_OVER_CAP = {
  index_line: `- ${FORM_NAME} — ${FORM_TRIGGER}`,
  index_line_chars: [...`- ${FORM_NAME} — ${FORM_TRIGGER}`].length,
  framed_body:
    `<<<WORKSPACE_PROCEDURE name="${FORM_NAME}">>>\n` + FORM_BODY + "\n<<<END_WORKSPACE_PROCEDURE>>>",
  // `domain/skill.go:139`, word for word. The preview shows the sentence the
  // *save* would refuse with, so the form and the API never word one rule
  // twice — a fixture that paraphrased it would be the drift that rule exists
  // to prevent, photographed and filed in the docs.
  refusal: `name is ${[...FORM_NAME].length} characters; the limit is 60, because it rides in every turn's prompt`,
};

export const THREADS = {
  threads: [
    { id: "th-1", title: "Revenue by branch, August vs July", is_archived: false },
    { id: "th-2", title: "Why is Bekasi missing from the report?", is_archived: false },
  ],
};

/** The `@/lib/api` stub. Unknown paths answer an empty object rather than
 *  throwing: a tab this harness is not looking at must not be able to blank the
 *  screenshot of the one it is. */
export function makeAPI(skills: typeof SKILLS_OK) {
  const ok = (data: unknown) => Promise.resolve({ data });
  return {
    get: (path: string) => {
      if (path === "/skills") return ok(skills);
      if (path === "/threads") return ok(THREADS.threads ? THREADS : { threads: [] });
      return ok({});
    },
    // The bodies are ignored on purpose: nothing here persists, and a save in
    // the harness is only ever pressed to photograph what the button does.
    post: (path: string, _body?: unknown) => {
      if (path === "/skills/preview") return ok(PREVIEW_OVER_CAP);
      return ok({});
    },
    put: (_path: string, _body?: unknown) => ok({}),
    delete: (_path: string) => ok({}),
  };
}

/** A frame around each shot: a title, so a screenshot filed in `docs/coverage`
 *  still says what it was taken to show once it is out of this directory. */
export function Scene({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="min-h-screen bg-background p-6">
      <p className="mb-4 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {title}
      </p>
      {children}
    </div>
  );
}
