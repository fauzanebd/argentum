import type { Plan, Scene } from "@argentum/api-types/videoplan";

/**
 * The plan, as this package sees it.
 *
 * The types are generated from `internal/report/videoplan/plan.go` — a
 * hand-written interface matching a Go struct is the exact defect T-02b deleted
 * four files to end — and re-exported here so no component imports the
 * generated module directly. That indirection buys one thing: when the contract
 * moves, it moves in one import.
 */
export type { Plan, Scene };
export type {
  Brand,
  Chart,
  Fact,
  KPI,
  Metrics,
  Table,
  Tone,
  TypeScale,
} from "@argentum/api-types/videoplan";

/** The plan version this package draws. */
export const SUPPORTED_VERSION = 1;

/**
 * Scene kinds, as strings rather than as an enum: the plan is JSON and a kind
 * this build does not know has to be survivable, not a type error.
 */
export const KIND = {
  cover: "cover",
  section: "section",
  statement: "statement",
  quote: "quote",
  kpi: "kpi",
  table: "table",
  chart: "chart",
  closing: "closing",
} as const;

/**
 * The scenes, paired with the frame each one starts at.
 *
 * Derived rather than carried in the plan: the plan already states each scene's
 * length, and a second field holding the running total is a field that can
 * disagree with the first. That is the same argument the plan's own TotalFrames
 * comment makes in the other direction — it is carried precisely because the
 * renderer must not re-derive the composition's length from a list it might
 * filter.
 */
export function timeline(plan: Plan): Array<{ scene: Scene; from: number }> {
  let from = 0;
  return (plan.scenes ?? []).map((scene) => {
    const entry = { scene, from };
    from += Math.max(1, scene.frames);
    return entry;
  });
}

/**
 * The scenes this build can draw, and the ones it cannot.
 *
 * A newer backend may send a kind this bundle has never heard of. The rule is
 * the mirror image of the version check: **refuse a version you do not know,
 * ignore a field you do not know.** A kind is a field — drawing the rest of the
 * video minus one beat is a better failure than a blank frame or a crash, and
 * the caller is told which kinds were dropped so it does not have to diff two
 * videos to find out.
 */
export function partition(plan: Plan): { known: string[]; unknown: string[] } {
  const kinds = new Set((plan.scenes ?? []).map((s) => s.kind));
  const known: string[] = [];
  const unknown: string[] = [];
  for (const kind of kinds) {
    (kind in KIND ? known : unknown).push(kind);
  }
  return { known, unknown };
}

/**
 * validate is what apps/render calls before it starts a browser.
 *
 * It returns a message rather than throwing, because the caller turns it into
 * an HTTP response and a stack trace is not one.
 */
export function validate(plan: Plan): string | null {
  if (!plan || typeof plan !== "object") return "no plan";
  if (plan.version !== SUPPORTED_VERSION) {
    return `plan version ${plan.version} is not supported; this renderer draws version ${SUPPORTED_VERSION}`;
  }
  if (!Array.isArray(plan.scenes) || plan.scenes.length === 0) {
    return "the plan has no scenes";
  }
  const sum = plan.scenes.reduce((n, s) => n + Math.max(1, s.frames), 0);
  if (sum !== plan.total_frames) {
    return `the plan's scenes total ${sum} frames and it declares ${plan.total_frames}`;
  }
  if (!plan.metrics || !plan.metrics.type || !plan.brand) {
    return "the plan carries no metrics or no brand";
  }
  return null;
}
