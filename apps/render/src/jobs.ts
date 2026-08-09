import { randomUUID } from "node:crypto";
import { stat } from "node:fs/promises";

import { PlanError, render } from "./render";
import type { Plan } from "@argentum/motion";

/**
 * The job store.
 *
 * **It is in this process's memory and on this pod's disk, and that is a stated
 * limit rather than an oversight.** A second replica would answer
 * `GET /v1/jobs/:id` for a job it has never heard of. Fixing that means putting
 * results in object storage, which means giving this service a credential and
 * egress — and the whole reason it can be deployed with egress denied is that
 * it needs neither. So: one replica until somebody is waiting, then the
 * object-storage job store, which is filed with that trigger.
 */

export type JobState = "queued" | "rendering" | "done" | "failed" | "cancelled";

export type Job = {
  id: string;
  state: JobState;
  progress: number;
  createdAt: number;
  finishedAt?: number;
  error?: string;
  /** True when the failure is the caller's plan rather than our renderer. */
  clientError?: boolean;
  outputPath?: string;
  sizeBytes?: number;
  frames?: number;
  renderSeconds?: number;
};

/** How long a finished job's file survives if nobody collects it. */
export const JOB_TTL_MS = 15 * 60 * 1000;

/**
 * The wall clock over one render.
 *
 * Remotion's per-frame timeout catches a frame that hangs; this catches
 * everything else — a browser that starts and never draws, an encoder that
 * stalls. Without it a pod is healthy, useless, and holding a tenant's report
 * forever, which is the worst of the three states it could be in.
 */
export const JOB_TIMEOUT_MS = 10 * 60 * 1000;

type Entry = { job: Job; cleanup?: () => Promise<void>; abort: AbortController };

export class JobStore {
  private readonly jobs = new Map<string, Entry>();
  private sweeper: NodeJS.Timeout | null = null;

  /** Only one render runs at a time; the rest wait in this chain. */
  private tail: Promise<unknown> = Promise.resolve();

  start(plan: Plan): Job {
    const id = randomUUID();
    const abort = new AbortController();
    const job: Job = {
      id,
      state: "queued",
      progress: 0,
      createdAt: Date.now(),
    };
    this.jobs.set(id, { job, abort });

    // Serialised deliberately. Remotion already uses every core it is given, so
    // two concurrent renders on one pod are slower than two sequential ones and
    // twice as likely to be killed by a memory limit.
    this.tail = this.tail.then(() => this.run(id, plan)).catch(() => undefined);
    return job;
  }

  private async run(id: string, plan: Plan): Promise<void> {
    const entry = this.jobs.get(id);
    if (!entry || entry.job.state === "cancelled") return;

    entry.job.state = "rendering";
    const timer = setTimeout(() => entry.abort.abort(), JOB_TIMEOUT_MS);

    try {
      const result = await render({
        plan,
        signal: entry.abort.signal,
        onProgress: (p) => {
          entry.job.progress = p;
        },
      });
      const info = await stat(result.outputPath);
      entry.cleanup = result.cleanup;
      Object.assign(entry.job, {
        state: "done" satisfies JobState,
        progress: 1,
        finishedAt: Date.now(),
        outputPath: result.outputPath,
        sizeBytes: info.size,
        frames: result.frames,
        renderSeconds: result.seconds,
      });
    } catch (err) {
      const aborted = entry.abort.signal.aborted;
      Object.assign(entry.job, {
        state: (aborted ? "cancelled" : "failed") satisfies JobState,
        finishedAt: Date.now(),
        clientError: err instanceof PlanError,
        error: aborted
          ? `the render exceeded the ${JOB_TIMEOUT_MS / 1000}s wall clock and was stopped`
          : err instanceof Error
            ? err.message
            : String(err),
      });
    } finally {
      clearTimeout(timer);
    }
  }

  get(id: string): Job | undefined {
    return this.jobs.get(id)?.job;
  }

  async drop(id: string): Promise<boolean> {
    const entry = this.jobs.get(id);
    if (!entry) return false;
    entry.abort.abort();
    await entry.cleanup?.();
    this.jobs.delete(id);
    return true;
  }

  /**
   * sweep drops finished jobs past the TTL.
   *
   * The caller crashing must not fill the disk — that is the whole reason this
   * exists, and it is why the sweep looks at `finishedAt` rather than at
   * whether anybody asked.
   */
  async sweep(now = Date.now()): Promise<number> {
    let dropped = 0;
    for (const [id, entry] of this.jobs) {
      const at = entry.job.finishedAt;
      if (at !== undefined && now - at > JOB_TTL_MS) {
        await this.drop(id);
        dropped++;
      }
    }
    return dropped;
  }

  startSweeper(intervalMs = 60_000): void {
    this.sweeper ??= setInterval(() => {
      void this.sweep();
    }, intervalMs);
    this.sweeper.unref?.();
  }

  async shutdown(): Promise<void> {
    if (this.sweeper) clearInterval(this.sweeper);
    await Promise.all([...this.jobs.keys()].map((id) => this.drop(id)));
  }

  get size(): number {
    return this.jobs.size;
  }
}
