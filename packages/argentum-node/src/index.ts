import { Chat } from './chat.js';
import { Documents } from './documents.js';
import { HttpClient, type ArgentumOptions } from './http.js';
import { Reports } from './reports.js';
import type { Me } from './types.js';

export { ReportJob, Reports, type RenderOptions, type WaitOptions } from './reports.js';
export { Chat, Threads, type PageOptions, type SendOptions, type StreamOptions } from './chat.js';
export { Documents, type ListDocumentsOptions } from './documents.js';
export { readSSE, type SSEFrame } from './sse.js';
export type { ArgentumOptions } from './http.js';
export * from './errors.js';
export * from './types.js';

/**
 * The Argentum client.
 *
 * ```ts
 * import { Argentum } from '@argentum/sdk';
 *
 * const client = new Argentum(); // reads ARGENTUM_API_KEY and ARGENTUM_BASE_URL
 * const pdf = await client.reports.render(spec);
 * ```
 *
 * The three things it does that a `fetch` wrapper does not: it retries 429s and
 * 5xx with backoff on the server's own `Retry-After`, it puts an
 * `Idempotency-Key` on every write and **reuses it across retries**, and it
 * throws errors that carry the API's envelope — type, code, param and the
 * request id to quote at us.
 */
export class Argentum {
  readonly reports: Reports;
  readonly chat: Chat;
  readonly documents: Documents;
  private readonly http: HttpClient;

  constructor(options: ArgentumOptions = {}) {
    this.http = new HttpClient(options);
    this.reports = new Reports(this.http);
    this.chat = new Chat(this.http);
    this.documents = new Documents(this.http);
  }

  /**
   * Who this key is, what it can do, and what the tenant has left to spend.
   *
   * Call it first. It needs no scope, so it answers even for a key that can do
   * nothing else, and its output is the one paste that makes a support question
   * answerable.
   */
  async me(signal?: AbortSignal): Promise<Me> {
    return this.http.json<Me>({ method: 'GET', path: '/v1/me', ...(signal ? { signal } : {}) });
  }

  /** The origin this client is talking to. Useful in a log line. */
  get baseUrl(): string {
    return this.http.baseUrl;
  }
}

export default Argentum;
