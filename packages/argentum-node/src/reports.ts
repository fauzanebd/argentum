import { randomUUID } from 'node:crypto';
import type { HttpClient } from './http.js';
import { ArgentumError } from './errors.js';
import { readSSE } from './sse.js';
import type { ArgentumDocument, CreateReportRequest, RenderedDocument, Report, ReportEvent, ReportSpec } from './types.js';

export interface RenderOptions {
  signal?: AbortSignal;
  /** Supply your own so a retry after a lost response replays instead of re-rendering. */
  idempotencyKey?: string;
}

export interface WaitOptions {
  /** How often to poll. Default 2s. */
  pollMs?: number;
  /** How long to wait before giving up on a job that is still running. Default 10 minutes. */
  timeoutMs?: number;
  signal?: AbortSignal;
}

/** The two report doors, and the collection paths behind them. */
export class Reports {
  constructor(private readonly http: HttpClient) {}

  /**
   * A spec in, the file's bytes out. Deterministic, no LLM, sub-second.
   *
   * Three responses can come back and this returns a `Buffer` for all three,
   * which is the point of it being here rather than in your code:
   *
   * - the bytes, which is the ordinary case;
   * - the document object, which is what a **replay** of this call returns —
   *   the bytes are not stored anywhere to be replayed, so the API hands back
   *   the object with a fresh URL instead, and we fetch it;
   * - a `202` report, when the spec outran the server's synchronous window. We
   *   wait for the job and download what it produced.
   */
  async render(spec: ReportSpec, options: RenderOptions = {}): Promise<Buffer> {
    const res = await this.http.raw({
      method: 'POST',
      path: '/v1/reports/render',
      body: spec,
      // Any of the format content types would do; this one does not depend on
      // knowing the format up front.
      accept: 'application/octet-stream',
      idempotencyKey: options.idempotencyKey ?? randomUUID(),
      ...(options.signal ? { signal: options.signal } : {}),
    });

    if (!(res.headers.get('Content-Type') ?? '').includes('application/json')) {
      return Buffer.from(await res.arrayBuffer());
    }

    const body = (await res.json()) as RenderedDocument | Report;
    if (body.object === 'document') {
      return this.downloadDocument(body.document.id, options.signal);
    }
    if (body.object === 'report') {
      const finished = await new ReportJob(this, body).wait(options.signal ? { signal: options.signal } : {});
      return this.downloadReport(finished, options.signal);
    }
    throw new ArgentumError({
      message: `The render door answered JSON this client does not recognise: ${JSON.stringify(body).slice(0, 200)}`,
      type: 'server',
      code: 'unexpected_response',
      status: res.status,
    });
  }

  /**
   * The same call, returning the document's metadata and a short-lived
   * presigned URL instead of the bytes. Use it when you want to hand the URL
   * to a browser rather than write a file.
   */
  async renderDocument(spec: ReportSpec, options: RenderOptions = {}): Promise<ArgentumDocument> {
    const body = await this.http.json<{ document: ArgentumDocument }>({
      method: 'POST',
      path: '/v1/reports/render',
      body: spec,
      accept: 'application/json',
      idempotencyKey: options.idempotencyKey ?? randomUUID(),
      ...(options.signal ? { signal: options.signal } : {}),
    });
    return body.document;
  }

  /**
   * A prompt in, a real agent turn behind it. Returns a job you can wait on,
   * stream, or come back to later with its `id`.
   */
  async create(request: CreateReportRequest, options: RenderOptions = {}): Promise<ReportJob> {
    const report = await this.http.json<Report>({
      method: 'POST',
      path: '/v1/reports',
      body: request,
      idempotencyKey: options.idempotencyKey ?? randomUUID(),
      ...(options.signal ? { signal: options.signal } : {}),
    });
    return new ReportJob(this, report);
  }

  /** Poll one report. */
  async get(id: string, signal?: AbortSignal): Promise<Report> {
    return this.http.json<Report>({ method: 'GET', path: `/v1/reports/${encodeURIComponent(id)}`, ...(signal ? { signal } : {}) });
  }

  /** Pick a job you already have the id of back up. */
  async job(id: string, signal?: AbortSignal): Promise<ReportJob> {
    return new ReportJob(this, await this.get(id, signal));
  }

  /** Progress events for a running job. Ends on the terminal `report` frame. */
  async *stream(id: string, signal?: AbortSignal): AsyncGenerator<ReportEvent> {
    const res = await this.http.raw({
      method: 'GET',
      path: `/v1/reports/${encodeURIComponent(id)}/events`,
      accept: 'text/event-stream',
      stream: true,
      ...(signal ? { signal } : {}),
    });
    for await (const frame of readSSE(res, signal)) {
      const event = { event: frame.event, data: JSON.parse(frame.data) as never, ...(frame.id ? { id: frame.id } : {}) } as ReportEvent;
      yield event;
      if (event.event === 'report' || event.event === 'error') return;
    }
  }

  /** @internal */
  async downloadReport(report: Report, signal?: AbortSignal): Promise<Buffer> {
    if (!report.document?.id) {
      throw new ArgentumError({
        message: describeMissingDocument(report),
        type: 'server',
        code: report.status === 'failed' ? 'report_failed' : 'report_no_document',
        status: 0,
      });
    }
    return this.downloadDocument(report.document.id, signal);
  }

  /** @internal */
  async downloadDocument(id: string, signal?: AbortSignal): Promise<Buffer> {
    return this.http.bytes({
      method: 'GET',
      path: `/v1/documents/${encodeURIComponent(id)}/content`,
      accept: 'application/octet-stream',
      ...(signal ? { signal } : {}),
    });
  }
}

/**
 * Explains a report with no document to download.
 *
 * A **completed** report without one is not an error on our side and not a
 * transient state: the agent was asked for a report and answered in prose. The
 * API says so by completing the job with no `document`, and the message has to
 * say the same thing — the first version of this said "has produced no document
 * yet", which reads as "wait longer" for something that will never arrive. The
 * thread is the useful thing to hand back, because the answer is in it.
 */
function describeMissingDocument(report: Report): string {
  if (report.status === 'failed') {
    return `The report failed: ${report.error ?? 'no reason given'}`;
  }
  if (report.status === 'completed') {
    return (
      `Report ${report.id} completed without generating a document — the agent answered in prose instead. ` +
      (report.thread_id
        ? `Read what it said with client.chat.threads.messagesAll('${report.thread_id}'), or ask again with a more specific prompt.`
        : 'Ask again with a more specific prompt.')
    );
  }
  return `Report ${report.id} is ${report.status} and has produced no document yet.`;
}

/**
 * A report that is being worked on.
 *
 * It holds the last state it saw rather than re-fetching on every property
 * read: a poller that costs a request to ask what it already knows is a poller
 * that rate-limits its own caller.
 */
export class ReportJob {
  constructor(
    private readonly reports: Reports,
    public report: Report,
  ) {}

  get id(): string {
    return this.report.id;
  }

  get status(): Report['status'] {
    return this.report.status;
  }

  /** True once the job has either produced a document or failed. */
  get done(): boolean {
    return this.report.status === 'completed' || this.report.status === 'failed';
  }

  /** Ask the API for the job's current state and remember it. */
  async refresh(signal?: AbortSignal): Promise<Report> {
    this.report = await this.reports.get(this.id, signal);
    return this.report;
  }

  /**
   * Poll until the job is terminal.
   *
   * Polling rather than streaming, deliberately: the stream is the better
   * experience when you want to *show* progress, and the worse one when all
   * you want is the file — a dropped connection mid-turn would have to be
   * reconnected and reconciled, where a poll that fails is just a poll you do
   * again. `stream()` is right there when you want the other behaviour.
   */
  async wait(options: WaitOptions = {}): Promise<Report> {
    const pollMs = options.pollMs ?? 2_000;
    const deadline = Date.now() + (options.timeoutMs ?? 10 * 60_000);
    while (!this.done) {
      if (Date.now() > deadline) {
        throw new ArgentumError({
          message: `Report ${this.id} was still ${this.report.status} after the client's timeout. It is still running — poll it with client.reports.get('${this.id}').`,
          type: 'server',
          code: 'client_timeout',
          status: 0,
        });
      }
      await new Promise((resolve) => setTimeout(resolve, pollMs));
      if (options.signal?.aborted) throw new Error('aborted');
      await this.refresh(options.signal);
    }
    return this.report;
  }

  /**
   * Progress events for this job.
   *
   * The terminal `report` frame carries the whole object, so it is stored on
   * the way past. Without that, a caller who streamed to completion and then
   * called `download()` would find the job still holding the `queued` snapshot
   * it was constructed with and poll for a state it had already been told.
   */
  async *stream(signal?: AbortSignal): AsyncGenerator<ReportEvent> {
    for await (const event of this.reports.stream(this.id, signal)) {
      if (event.event === 'report') this.report = event.data;
      yield event;
    }
  }

  /** Wait for the job and hand back the file's bytes. */
  async download(options: WaitOptions = {}): Promise<Buffer> {
    if (!this.done) await this.wait(options);
    return this.reports.downloadReport(this.report, options.signal);
  }
}
