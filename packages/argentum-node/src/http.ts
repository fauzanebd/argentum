import { randomUUID } from 'node:crypto';
import { ArgentumError, TransportError, errorFromBody } from './errors.js';

/** How the client is configured. Everything has a default worth having. */
export interface ArgentumOptions {
  /** `arg_…`. Defaults to `process.env.ARGENTUM_API_KEY`. */
  apiKey?: string;
  /** Origin only, no path. Defaults to `process.env.ARGENTUM_BASE_URL`, then `http://localhost:8080`. */
  baseUrl?: string;
  /**
   * Per-attempt timeout. It bounds one HTTP attempt, never a turn: an agentic
   * report takes minutes, and the poller waits on its own clock.
   *
   * Streams are exempt — a stream that has not sent a byte for `timeoutMs` is
   * a stream sending heartbeats every 15 seconds, and cutting it off would
   * break the feature.
   */
  timeoutMs?: number;
  /** How many times a retryable failure is retried. Default 2 (three attempts). */
  maxRetries?: number;
  /** Swap in a different fetch — a test double, or an agent with a proxy. */
  fetch?: typeof globalThis.fetch;
  /** Extra headers on every request. */
  headers?: Record<string, string>;
}

export interface RequestOptions {
  method: string;
  path: string;
  query?: Record<string, string | number | undefined>;
  body?: unknown;
  headers?: Record<string, string>;
  accept?: string;
  /**
   * Supplied only when the caller wants to control the key — a retry of a
   * request whose response was lost, say. Otherwise one is generated, once,
   * and reused across this call's own retries.
   */
  idempotencyKey?: string;
  signal?: AbortSignal;
  /** A streaming response: no per-attempt timeout, and the body is not read. */
  stream?: boolean;
}

const DEFAULT_BASE_URL = 'http://localhost:8080';

/**
 * The transport. Everything the resource classes do goes through `request`.
 *
 * It owns the three things the ticket asks a client to own so an integrator
 * does not have to: retries with backoff that honour `Retry-After`, an
 * `Idempotency-Key` on every write, and errors that carry the envelope instead
 * of a status code and a string.
 */
export class HttpClient {
  readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly timeoutMs: number;
  private readonly maxRetries: number;
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly extraHeaders: Record<string, string>;

  constructor(options: ArgentumOptions = {}) {
    const apiKey = options.apiKey ?? process.env['ARGENTUM_API_KEY'] ?? '';
    if (!apiKey) {
      throw new ArgentumError({
        message:
          'No API key. Pass `new Argentum({ apiKey })` or set ARGENTUM_API_KEY. Mint one in the dashboard under Settings → API Keys.',
        type: 'authentication',
        code: 'missing_api_key',
        status: 0,
      });
    }
    this.apiKey = apiKey;
    this.baseUrl = (options.baseUrl ?? process.env['ARGENTUM_BASE_URL'] ?? DEFAULT_BASE_URL).replace(/\/+$/, '');
    this.timeoutMs = options.timeoutMs ?? 60_000;
    this.maxRetries = options.maxRetries ?? 2;
    this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.extraHeaders = options.headers ?? {};
  }

  /** A request whose JSON body you want parsed. */
  async json<T>(options: RequestOptions): Promise<T> {
    const res = await this.raw({ ...options, accept: options.accept ?? 'application/json' });
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  /** A request whose body you want as bytes — a rendered document, usually. */
  async bytes(options: RequestOptions): Promise<Buffer> {
    const res = await this.raw(options);
    return Buffer.from(await res.arrayBuffer());
  }

  /**
   * One request, retried, with the response left unread.
   *
   * The **idempotency key is minted here, before the retry loop**, and that
   * placement is the whole point of it: a key generated per attempt would make
   * every retry a new logical request, which is precisely the duplicate billing
   * the header exists to prevent.
   */
  async raw(options: RequestOptions): Promise<Response> {
    const url = new URL(this.baseUrl + options.path);
    for (const [k, v] of Object.entries(options.query ?? {})) {
      if (v !== undefined && v !== '') url.searchParams.set(k, String(v));
    }

    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.apiKey}`,
      ...this.extraHeaders,
      ...options.headers,
    };
    if (options.accept) headers['Accept'] = options.accept;
    if (options.body !== undefined) headers['Content-Type'] = 'application/json';
    if (options.method !== 'GET' && options.method !== 'DELETE') {
      headers['Idempotency-Key'] = options.idempotencyKey ?? randomUUID();
    }

    const body = options.body === undefined ? undefined : JSON.stringify(options.body);

    let lastError: ArgentumError | undefined;
    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      const timeout = options.stream ? undefined : AbortSignal.timeout(this.timeoutMs);
      const signal = combineSignals(options.signal, timeout);

      let res: Response;
      try {
        res = await this.fetchImpl(url, { method: options.method, headers, body, signal });
      } catch (cause) {
        if (options.signal?.aborted) throw cause;
        lastError = new TransportError({
          message: `${options.method} ${options.path} did not reach Argentum: ${(cause as Error).message}`,
          type: 'transport',
          code: 'transport_error',
          status: 0,
          cause,
        });
        if (attempt === this.maxRetries) throw lastError;
        await sleep(backoffMs(attempt, undefined));
        continue;
      }

      if (res.ok) return res;

      const retryAfter = parseRetryAfter(res.headers.get('Retry-After'));
      const requestId = res.headers.get('X-Request-Id') ?? undefined;
      const payload = await readErrorBody(res);
      lastError = errorFromBody(res.status, payload, requestId, retryAfter);

      if (attempt === this.maxRetries || !isRetryable(res.status)) throw lastError;
      await sleep(backoffMs(attempt, retryAfter));
    }
    // Unreachable: the loop either returns or throws.
    throw lastError ?? new TransportError({ message: 'request failed', type: 'transport', code: 'transport_error', status: 0 });
  }
}

/**
 * Which failures are worth sending again.
 *
 * 429 and 5xx, with two exclusions that matter more than the rule:
 *
 * - **504 is not retried.** On `POST /v1/chat` it does not mean the turn
 *   failed; it means the *wait* ran out while the turn keeps running and keeps
 *   being billed. Retrying under the same key gets a `409 request_in_flight`,
 *   and under a new one it would start a second turn. The caller gets a
 *   `WorkInProgressError` carrying the thread id instead — attach to it.
 * - **501 is not retried.** Nothing about sending it again makes an
 *   unimplemented route implemented.
 */
function isRetryable(status: number): boolean {
  if (status === 429) return true;
  if (status === 501 || status === 504) return false;
  return status >= 500;
}

/**
 * Exponential backoff with full jitter, unless the server said when.
 *
 * `Retry-After` wins because the rate limiter computes it from the bucket's
 * own state — it knows when a token actually appears. The jitter matters for
 * the same reason the API floors `Retry-After` at one second: every refused
 * client waking at the same instant is how a rate limit becomes a synchronised
 * thundering herd.
 */
function backoffMs(attempt: number, retryAfterSeconds: number | undefined): number {
  if (retryAfterSeconds !== undefined) return retryAfterSeconds * 1000;
  const ceiling = Math.min(8_000, 250 * 2 ** attempt);
  return Math.round(ceiling * (0.5 + Math.random() / 2));
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds)) return Math.max(0, seconds);
  // The HTTP-date form. Rare from this API, valid per the RFC, and cheap to
  // accept rather than silently ignore.
  const at = Date.parse(value);
  if (Number.isNaN(at)) return undefined;
  return Math.max(0, (at - Date.now()) / 1000);
}

/**
 * Reads a failure body without letting the read become the failure.
 *
 * A 502 from a load balancer is HTML, and a connection that dies mid-body
 * throws here — in both cases the status is the real information, so this
 * returns null and lets the envelope-less path produce the error.
 */
async function readErrorBody(res: Response): Promise<unknown> {
  try {
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

function combineSignals(a: AbortSignal | undefined, b: AbortSignal | undefined): AbortSignal | undefined {
  if (!a) return b;
  if (!b) return a;
  return AbortSignal.any([a, b]);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
