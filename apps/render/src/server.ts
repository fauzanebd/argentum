import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { timingSafeEqual } from "node:crypto";
import { pipeline } from "node:stream/promises";

import { partition, validate } from "@argentum/motion";
import type { Plan } from "@argentum/motion";

import { JobStore } from "./jobs";
import { checkOutput, parseJobPath, parseOutput } from "./output";
import { serveUrl } from "./render";

/**
 * The render service.
 *
 * No framework: six routes, two of which stream a file. A router would be a
 * dependency to keep current on a service whose whole security posture is that
 * it has almost nothing in it.
 *
 * **What this service can reach: nothing.** The plan is self-contained — chart
 * images and the tenant logo arrive as data URIs, because Go has already drawn
 * both — so there is no object storage credential, no database, and no outbound
 * request. That is what makes an egress-deny NetworkPolicy a correctness-
 * preserving configuration rather than a compromise, and it means the SSRF
 * class that T-M1's gate spent three findings on cannot exist here.
 */

const PORT = Number(process.env.RENDER_PORT ?? 8090);
const SECRET = process.env.RENDER_SHARED_SECRET ?? "";
const MAX_BODY_BYTES = Number(process.env.RENDER_MAX_BODY_BYTES ?? 32 * 1024 * 1024);

const jobs = new JobStore();

/**
 * healthy is set by the boot render. The readiness probe reads it, so a pod
 * whose browser cannot start never receives a tenant's report — it fails the
 * probe instead, which is the difference between an alert and a refund.
 */
let healthy = false;
let bootError = "";

async function boot(): Promise<void> {
  try {
    await serveUrl();
    healthy = true;
    log("ready", { port: PORT });
  } catch (err) {
    bootError = err instanceof Error ? err.message : String(err);
    log("boot failed", { error: bootError });
  }
}

const server = createServer((req, res) => {
  handle(req, res).catch((err: unknown) => {
    log("unhandled", { error: String(err) });
    if (!res.headersSent) send(res, 500, { error: "internal error" });
  });
});

async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
  const url = new URL(req.url ?? "/", `http://localhost:${PORT}`);
  const path = url.pathname;

  if (path === "/healthz") {
    return healthy
      ? send(res, 200, { status: "ok", jobs: jobs.size })
      : send(res, 503, { status: "starting", error: bootError });
  }

  if (!authorised(req)) {
    return send(res, 401, { error: "unauthorised" });
  }

  if (req.method === "POST" && path === "/v1/render") {
    return startRender(req, res);
  }

  const job = parseJobPath(path);
  if (job) {
    const { id, result, page } = job;
    if (req.method === "GET" && result && page !== undefined) return sendPage(res, id, page);
    if (req.method === "GET" && result) return sendResult(res, id);
    if (req.method === "GET") return sendStatus(res, id);
    if (req.method === "DELETE") {
      return send(res, (await jobs.drop(id)) ? 200 : 404, { id });
    }
  }

  send(res, 404, { error: "no such route" });
}

async function startRender(req: IncomingMessage, res: ServerResponse): Promise<void> {
  let body: unknown;
  try {
    body = JSON.parse(await readBody(req));
  } catch (err) {
    return send(res, 400, { error: `invalid body: ${String(err)}` });
  }

  const { plan, output: rawOutput } = (body as { plan?: Plan; output?: unknown }) ?? {};
  if (!plan) return send(res, 400, { error: "no `plan` in the body" });

  // Validated before a browser is started, not inside the render. The caller
  // gets a sentence it can act on in milliseconds instead of a failed job in
  // minutes — and the version check is the one that matters: refuse a version
  // you do not know, ignore a field you do not know.
  const problem = validate(plan);
  if (problem) return send(res, 400, { error: problem });

  // The output is checked against the plan the same way (T-G5): a still plan
  // rendered as a video and a video plan rendered as stills are both wrong
  // artifacts that take a minute to notice.
  const output = parseOutput(rawOutput);
  if (output !== "video" && output !== "stills") return send(res, 400, { error: output });
  const mismatch = checkOutput(plan, output);
  if (mismatch) return send(res, 400, { error: mismatch });

  const { unknown } = partition(plan);
  if (unknown.length > 0) {
    // Not a refusal. A newer backend sending a beat this bundle cannot draw
    // should still get the rest of its video — but it is told, so nobody has to
    // diff two renders to find the gap.
    log("unknown scene kinds", { kinds: unknown });
  }

  const job = jobs.start(plan, output);
  log("accepted", { id: job.id, output, frames: plan.total_frames, scenes: plan.scenes.length });
  send(res, 202, { job_id: job.id, unknown_kinds: unknown });
}

function sendStatus(res: ServerResponse, id: string): void {
  const job = jobs.get(id);
  if (!job) return send(res, 404, { error: "no such job" });
  send(res, 200, {
    id: job.id,
    output: job.output,
    state: job.state,
    progress: Number(job.progress.toFixed(4)),
    error: job.error,
    size_bytes: job.sizeBytes,
    frames: job.frames,
    // Absent on a video job rather than 0, so a status shape written against
    // the video never sees a field it has to explain.
    ...(job.pages !== undefined ? { pages: job.pages } : {}),
    render_seconds: job.renderSeconds,
  });
}

async function sendResult(res: ServerResponse, id: string): Promise<void> {
  const job = jobs.get(id);
  if (!job) return send(res, 404, { error: "no such job" });
  if (job.state === "failed" || job.state === "cancelled") {
    // A caller's bad plan is a 400 even when it surfaces late: the distinction
    // between "your input" and "our renderer" is what decides whether an
    // integrator fixes their spec or opens a ticket.
    return send(res, job.clientError ? 400 : 500, { error: job.error });
  }
  if (job.state !== "done") {
    return send(res, 409, { error: `job is ${job.state}`, progress: job.progress });
  }
  if (job.output === "stills") {
    // The result of a stills job is N files, and this route answers with one.
    // A 409 rather than a zip: decision 5, and the sentence says where to go.
    return send(res, 409, {
      error: "a stills job has pages; fetch /result/:page",
      pages: job.pages,
    });
  }
  if (!job.outputPath) {
    return send(res, 409, { error: `job is ${job.state}`, progress: job.progress });
  }

  res.writeHead(200, {
    "content-type": "video/mp4",
    "content-length": String(job.sizeBytes ?? 0),
  });
  await pipeline(createReadStream(job.outputPath), res);
}

/**
 * sendPage streams one page of a done stills job. Out of range is a 404 like a
 * missing job: page 0 and page N+1 are things that do not exist, not
 * conflicts. A video job has no pages, so the same answer.
 */
async function sendPage(res: ServerResponse, id: string, page: number): Promise<void> {
  const job = jobs.get(id);
  if (!job) return send(res, 404, { error: "no such job" });
  if (job.state === "failed" || job.state === "cancelled") {
    return send(res, job.clientError ? 400 : 500, { error: job.error });
  }
  if (job.state !== "done") {
    return send(res, 409, { error: `job is ${job.state}`, progress: job.progress });
  }
  const path = jobs.pagePath(id, page);
  if (!path) {
    return send(res, 404, {
      error: job.output === "stills" ? `no page ${page}; this job has ${job.pages}` : "a video job has no pages",
    });
  }

  const info = await stat(path);
  res.writeHead(200, {
    "content-type": "image/jpeg",
    "content-length": String(info.size),
  });
  await pipeline(createReadStream(path), res);
}

/**
 * The shared secret is not the security boundary — the ClusterIP Service and
 * the NetworkPolicy are. It is what stops a misconfiguration being silently
 * exploitable, which is a different and smaller job, and it is compared in
 * constant time because doing otherwise here would be a decision to explain
 * rather than a line to write.
 */
function authorised(req: IncomingMessage): boolean {
  if (SECRET === "") return true; // unset: a developer machine, or compose
  const given = String(req.headers["x-render-secret"] ?? "");
  const a = Buffer.from(given);
  const b = Buffer.from(SECRET);
  return a.length === b.length && timingSafeEqual(a, b);
}

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    size += (chunk as Buffer).length;
    if (size > MAX_BODY_BYTES) {
      throw new Error(`body over ${MAX_BODY_BYTES} bytes`);
    }
    chunks.push(chunk as Buffer);
  }
  return Buffer.concat(chunks).toString("utf8");
}

function send(res: ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    "content-type": "application/json",
    "content-length": String(Buffer.byteLength(payload)),
  });
  res.end(payload);
}

/**
 * The log carries job ids, durations and frame counts — never a plan.
 *
 * A plan is a customer's business figures. This service has no tenant, no user
 * and no thread, and it must not acquire them by way of a log line.
 */
function log(event: string, fields: Record<string, unknown> = {}): void {
  // eslint-disable-next-line no-console
  console.log(JSON.stringify({ svc: "render", event, ...fields }));
}

async function shutdown(signal: string): Promise<void> {
  log("shutting down", { signal });
  server.close();
  await jobs.shutdown();
  process.exit(0);
}

process.on("SIGTERM", () => void shutdown("SIGTERM"));
process.on("SIGINT", () => void shutdown("SIGINT"));

jobs.startSweeper();
server.listen(PORT, () => void boot());
