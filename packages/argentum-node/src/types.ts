/**
 * The wire types, re-exported under names worth importing.
 *
 * Every one of these is an alias into `types.generated.ts`, which
 * `pnpm generate` writes from `apps/backend/openapi/v1.yaml` — the same file
 * the server serves at `GET /v1/openapi.json` and the same file CI diffs
 * against the gin route tree. Nothing here is hand-written, which is the point:
 * a second hand-maintained copy of these shapes is exactly the drift the spec
 * exists to stop.
 *
 * The ergonomics on top of them are hand-written. That split is deliberate —
 * generated clients are unpleasant to use, and hand-written types go stale.
 */
import type { components } from './types.generated.js';

type Schemas = components['schemas'];

export type ErrorDetail = Schemas['ErrorDetail'];
export type Me = Schemas['Me'];
export type Scope = Schemas['Scope'];
export type Credits = Schemas['Credits'];

export type UsageReport = Schemas['UsageReport'];
export type UsagePeriod = Schemas['UsagePeriod'];
export type UsageSpend = Schemas['UsageSpend'];
export type UsageModelSpend = Schemas['UsageModelSpend'];

export type DocumentFormat = Schemas['DocumentFormat'];
export type ArgentumDocument = Schemas['Document'];
export type DocumentPage = Schemas['DocumentPage'];
export type RenderedDocument = Schemas['RenderedDocument'];

export type ReportSpec = Schemas['ReportSpec'];
export type ReportSpecSection = Schemas['ReportSpecSection'];
export type ReportSpecChart = Schemas['ReportSpecChart'];
export type Report = Schemas['Report'];
export type CreateReportRequest = Schemas['CreateReportRequest'];

export type ChatRequest = Schemas['ChatRequest'];
export type Turn = Schemas['Turn'];
export type Message = Schemas['Message'];
export type MessagePage = Schemas['MessagePage'];
export type Thread = Schemas['Thread'];
export type ThreadPage = Schemas['ThreadPage'];
export type Usage = Schemas['Usage'];

/**
 * One frame of a chat stream, named by its SSE `event:`.
 *
 * A discriminated union rather than the spec's bare `oneOf`, because the
 * discriminator lives in the SSE envelope rather than in the JSON payload —
 * `event: delta` with `data: {"content":"…"}` — and a caller switching on
 * `ev.event` is the whole ergonomic difference between this and parsing SSE by
 * hand.
 *
 * `final` and `error` are terminal. `message` appears only on a resumed stream.
 * Treat an unknown `event` as ignorable: that is what lets this list grow.
 */
export type ChatEvent =
  | { event: 'started'; id?: string; data: { thread_id: string; run_id?: string; at: string } }
  | { event: 'delta'; id?: string; data: { content: string } }
  | { event: 'thinking'; id?: string; data: { step: string } }
  | { event: 'tool_call'; id?: string; data: { tool?: string } }
  | { event: 'tool_result'; id?: string; data: { tool?: string } }
  | { event: 'message'; id?: string; data: Message }
  | { event: 'error'; id?: string; data: { message: string } }
  | { event: 'final'; id?: string; data: Turn };

/** One frame of a report stream. `report` is terminal. */
export type ReportEvent =
  | {
      event: 'progress';
      id?: string;
      data: { type: 'started' | 'tool_call' | 'tool_result' | 'thinking'; at: string; tool?: string; step?: string };
    }
  | { event: 'report'; id?: string; data: Report }
  | { event: 'error'; id?: string; data: { message: string } };
