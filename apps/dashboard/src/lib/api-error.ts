/**
 * Shared handling for a failed API call.
 *
 * Every catch clause in the dashboard used to read the axios error's nested
 * `response.data.error` field, falling back to the Error's own message, with
 * the caught value typed `any`. That worked, but it typed twenty-odd catch
 * clauses as `any` and it had a quiet failure of its own: a thrown value that
 * is not an Error has no `message`, so it surfaced in the UI as the literal
 * string "undefined" — the least useful thing a toast can say.
 *
 * apiErrorMessage narrows instead of asserting, and always returns something
 * a user can read.
 */

/** The `{"error": "…"}` envelope every handler in the Go API returns. */
type ApiErrorBody = { error?: unknown }

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null
}

/**
 * apiErrorMessage extracts the most specific message available from a thrown
 * value: the API's own `error` field first, then the Error's message, then the
 * supplied fallback.
 */
export function apiErrorMessage(e: unknown, fallback = 'Something went wrong'): string {
  if (isRecord(e) && isRecord(e.response)) {
    const data = e.response.data as ApiErrorBody | undefined
    if (data && typeof data.error === 'string' && data.error !== '') {
      return data.error
    }
  }
  if (e instanceof Error && e.message !== '') {
    return e.message
  }
  if (typeof e === 'string' && e !== '') {
    return e
  }
  return fallback
}

/**
 * apiErrorStatus returns the HTTP status a failed request came back with, or
 * 0 when the throw was not an HTTP response at all — a network failure, an
 * aborted request, or a bug in our own handler. Callers that branch on 404
 * need to tell those apart: treating a dropped connection as "not found" is
 * how a transient outage becomes a rendered empty state.
 */
export function apiErrorStatus(e: unknown): number {
  if (isRecord(e) && isRecord(e.response) && typeof e.response.status === 'number') {
    return e.response.status
  }
  return 0
}
