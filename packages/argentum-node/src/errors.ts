import type { ErrorDetail } from './types.js';

/**
 * The base of every failure this SDK throws.
 *
 * It mirrors the envelope every `/v1` response uses, field for field, so that
 * `catch (e)` gives you the same three things a `curl` would have shown you:
 * the class (`type`), the specific reason (`code`) and the id to quote at us
 * (`requestId`).
 */
export class ArgentumError extends Error {
  /** The coarse class. Switch on this. */
  readonly type: ErrorDetail['type'] | 'transport';
  /** The specific reason within that class — `insufficient_scope`, `spec_too_large`. */
  readonly code: string;
  /** HTTP status, or 0 when the request never got a response. */
  readonly status: number;
  /** The field or header that was wrong, when the API named one. */
  readonly param?: string;
  /** Quote this in a support conversation; it is the `X-Request-Id` header. */
  readonly requestId?: string;
  /** Seconds the API asked us to wait, from `Retry-After`. */
  readonly retryAfter?: number;

  constructor(init: {
    message: string;
    type: ErrorDetail['type'] | 'transport';
    code: string;
    status: number;
    param?: string;
    requestId?: string;
    retryAfter?: number;
    cause?: unknown;
  }) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause });
    this.name = new.target.name;
    this.type = init.type;
    this.code = init.code;
    this.status = init.status;
    this.param = init.param;
    this.requestId = init.requestId;
    this.retryAfter = init.retryAfter;
  }
}

/** 400 — the request was malformed. `param` names the field when there is one. */
export class InvalidRequestError extends ArgentumError {}
/** 401 — no key, or one that is not usable. */
export class AuthenticationError extends ArgentumError {}
/** 403 — a valid key without the scope this route needs. Scopes are fixed at mint time. */
export class PermissionError extends ArgentumError {}
/** 404 — no such resource *for this company*. */
export class NotFoundError extends ArgentumError {}
/** 429 — too many requests on this key. The client retries these for you. */
export class RateLimitError extends ArgentumError {}
/** 402 — the tenant is out of credit. Never retried: retrying it is a billing loop. */
export class BudgetExhaustedError extends ArgentumError {}
/** 5xx — our fault. */
export class ServerError extends ArgentumError {}

/**
 * 409 `idempotency_key_reuse` — the same key arrived with a different body.
 *
 * Almost always a bug in the caller: a key is per *logical* request, so reusing
 * one across two different requests would otherwise return the first one's
 * answer forever.
 */
export class IdempotencyConflictError extends ArgentumError {}

/**
 * The work is still running and this response says where to find it.
 *
 * Two responses land here: the `409 request_in_flight` a retry gets while the
 * original is still going, and the `504` the synchronous chat door answers when
 * a turn outruns the wait. Both carry the ids to collect with, and neither is
 * collectable by asking again — that is what would pay for the turn twice.
 */
export class WorkInProgressError extends ArgentumError {
  /** `{thread_id, run_id, started_at}` for a turn; `{report_id, status}` for a report. */
  readonly inFlight: Record<string, unknown>;

  constructor(init: ConstructorParameters<typeof ArgentumError>[0] & { inFlight?: Record<string, unknown> }) {
    super(init);
    this.inFlight = init.inFlight ?? {};
  }

  /** The thread to attach to with `client.chat.attach(threadId)`, when there is one. */
  get threadId(): string | undefined {
    const v = this.inFlight['thread_id'];
    return typeof v === 'string' ? v : undefined;
  }

  /** The report to poll with `client.reports.get(id)`, when there is one. */
  get reportId(): string | undefined {
    const v = this.inFlight['report_id'];
    return typeof v === 'string' ? v : undefined;
  }
}

/** The request never reached a response: DNS, connection, TLS, timeout, abort. */
export class TransportError extends ArgentumError {}

const byType: Record<ErrorDetail['type'], new (init: ConstructorParameters<typeof ArgentumError>[0]) => ArgentumError> = {
  invalid_request: InvalidRequestError,
  authentication: AuthenticationError,
  permission: PermissionError,
  not_found: NotFoundError,
  rate_limit: RateLimitError,
  budget_exhausted: BudgetExhaustedError,
  server: ServerError,
};

/**
 * Builds the error for one failed response.
 *
 * Two codes are dispatched on before the type is, because their `type` is not
 * what a caller needs to branch on: `request_in_flight` is typed
 * `invalid_request` (the request conflicts with one already running) and
 * `turn_in_progress` is typed `server` (a 504), but both mean the same thing —
 * the work exists, go and collect it.
 */
export function errorFromBody(status: number, body: unknown, requestId?: string, retryAfter?: number): ArgentumError {
  const detail = (body as { error?: Partial<ErrorDetail> } | null)?.error;
  const type = detail?.type ?? 'server';
  const code = detail?.code ?? 'unknown';
  const message = detail?.message ?? `Argentum answered ${status} with no error envelope.`;
  const init = {
    message,
    type,
    code,
    status,
    param: detail?.param,
    requestId: detail?.request_id ?? requestId,
    retryAfter,
  };

  if (code === 'request_in_flight' || code === 'turn_in_progress') {
    const inFlight = (body as { in_flight?: Record<string, unknown> } | null)?.in_flight;
    return new WorkInProgressError({ ...init, inFlight });
  }
  if (code === 'idempotency_key_reuse') {
    return new IdempotencyConflictError(init);
  }
  const Cls = byType[type as ErrorDetail['type']] ?? ServerError;
  return new Cls(init);
}
